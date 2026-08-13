package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/launcher"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/version"
	"github.com/ivantit66/onebase/internal/webhook"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the server in production mode",
	RunE:  runServer,
}

func init() {
	runCmd.Flags().String("id", "", "run a base from the ibases registry by ID")
	runCmd.Flags().String("project", ".", "path to project directory")
	runCmd.Flags().String("db", "", "PostgreSQL DSN (overrides DATABASE_URL env)")
	runCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	runCmd.Flags().Int("port", 8080, "HTTP server port")
	// Secure-by-default (план 53): наружу сервер выставляется только явно.
	runCmd.Flags().String("host", "127.0.0.1", "интерфейс прослушивания (0.0.0.0 — все интерфейсы)")
	runCmd.Flags().Bool("allow-insecure-bootstrap", false,
		"разрешить старт на не-loopback адресе без пользователей (НЕБЕЗОПАСНО; только для осознанной первичной настройки)")
	runCmd.Flags().String("config-source", "file", "configuration source: file or database")
	// hot reload .os/.yaml без перезапуска. По умолчанию off,
	// для прода обычно не нужен. Включается флагом --watch.
	runCmd.Flags().Bool("watch", false, "reload project metadata, DSL and scheduled jobs when configuration changes")
	// Открытие браузера — по явному флагу: `run` запускают и службой, и из
	// скриптов, где открывать вкладку некому и незачем.
	runCmd.Flags().Bool("open", false, "открыть базу в браузере, когда сервер будет готов")
	// Демо-режим через флаги — работает независимо от источника конфигурации.
	// Удобно для --config-source database, где app.yaml не лежит файлом и
	// блок demo: некуда вписать. Флаги имеют приоритет над app.yaml.
	runCmd.Flags().String("demo-backup", "", "путь к .obz; включает демо-режим (сброс данных по расписанию)")
	runCmd.Flags().String("demo-schedule", "", "cron-расписание сброса демо-данных (по умолчанию '0 2 * * *')")
	runCmd.Flags().String("demo-message", "", "текст баннера демо-режима")
}

func acquireServerDatabaseLease(ctx context.Context, dbType, sqlitePath, dsn string, shared bool) (dblock.Lease, string, error) {
	if dbType == "sqlite" {
		if sqlitePath == "" {
			return nil, "", fmt.Errorf("--sqlite path is required for sqlite databases")
		}
		if shared {
			return dblock.AcquireSQLiteSharedTarget(sqlitePath)
		}
		return dblock.AcquireSQLiteTarget(sqlitePath)
	}
	if shared {
		lease, err := dblock.AcquirePostgresShared(ctx, dsn)
		return lease, sqlitePath, err
	}
	lease, err := dblock.AcquirePostgres(ctx, dsn)
	return lease, sqlitePath, err
}

// scheduledDemoResetRequest is returned by one server generation only after
// its HTTP listener and scheduler have stopped. The outer runServer loop then
// lets the generation's deferred DB/lease cleanup run before performing the
// destructive reset under an exclusive database lifetime lease.
type scheduledDemoResetRequest struct {
	dbType     string
	dsn        string
	sqlitePath string
	filesDir   string
	backupPath string
	run        scheduler.RunInfo
}

func (r *scheduledDemoResetRequest) Error() string {
	return "scheduled demo reset requested"
}

func updateScheduledDemoResetRun(request *scheduledDemoResetRequest, db *storage.DB, runErr error) error {
	if request == nil || request.run.ID == uuid.Nil || request.run.StartedAt.IsZero() {
		return nil
	}
	status := "success"
	errText := ""
	if runErr != nil {
		status = "error"
		errText = runErr.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.UpdateScheduledRun(ctx, request.run.ID, status, "", errText, time.Since(request.run.StartedAt).Milliseconds())
}

func recordScheduledDemoResetResult(request *scheduledDemoResetRequest, runErr error) error {
	if request == nil || request.run.ID == uuid.Nil || request.run.StartedAt.IsZero() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, sqlitePath, err := acquireServerDatabaseLease(ctx, request.dbType, request.sqlitePath, request.dsn, true)
	if err != nil {
		return fmt.Errorf("record scheduled demo reset result lock: %w", err)
	}
	var db *storage.DB
	if request.dbType == "sqlite" {
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, request.dsn)
	}
	if err != nil {
		return errors.Join(fmt.Errorf("record scheduled demo reset result open database: %w", err), lease.Close())
	}
	completionErr := updateScheduledDemoResetRun(request, db, runErr)
	db.Close()
	return errors.Join(completionErr, lease.Close())
}

func warnScheduledDemoResetResult(request *scheduledDemoResetRequest, runErr error) {
	if err := recordScheduledDemoResetResult(request, runErr); err != nil {
		oblog.Component("cli.run").Warn("scheduled demo reset history remains accepted because its actual result could not be recorded", "err", err)
	}
}

func performScheduledDemoReset(ctx context.Context, request *scheduledDemoResetRequest) (*backup.ImportReport, error) {
	if request == nil {
		return nil, errors.New("scheduled demo reset request is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.filesDir == "" {
		filesErr := errors.New("scheduled demo reset files directory is empty")
		warnScheduledDemoResetResult(request, filesErr)
		return nil, filesErr
	}

	lease, sqlitePath, err := acquireServerDatabaseLease(ctx, request.dbType, request.sqlitePath, request.dsn, false)
	if err != nil {
		lockErr := fmt.Errorf("scheduled demo reset exclusive database lock: %w", err)
		warnScheduledDemoResetResult(request, lockErr)
		return nil, lockErr
	}

	var db *storage.DB
	if request.dbType == "sqlite" {
		// Open the exact canonical target returned with the exclusive lease. In
		// particular, do not resolve a registry symlink again after locking it.
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, request.dsn)
	}
	if err != nil {
		openErr := errors.Join(err, lease.Close())
		warnScheduledDemoResetResult(request, openErr)
		return nil, openErr
	}
	db.SetFilesDir(request.filesDir)

	report, resetErr := backup.DemoReset(ctx, db, request.backupPath)
	// The database pool must be gone before another shared/exclusive lifetime
	// lease can observe this database as available.
	db.Close()
	operationErr := errors.Join(resetErr, lease.Close())
	// Refine accepted -> success only after the destructive lease was cleanly
	// released. On any operation/cleanup error, do not reopen the database ahead
	// of startup restore-journal recovery; accepted remains truthful and the
	// concrete failure is logged by the outer loop.
	if operationErr == nil {
		warnScheduledDemoResetResult(request, nil)
	}
	return report, operationErr
}

// bootstrapRefusal возвращает причину отказа в старте, если сервер слушает
// не-loopback адрес без единого пользователя (auth в этом состоянии выключен
// целиком) и небезопасный bootstrap не разрешён явно (SEC-03, issue #778).
// Пустая строка — старт разрешён.
func bootstrapRefusal(host string, hasUsers, allowInsecure bool) string {
	if hasUsers || allowInsecure || api.IsLoopbackHost(host) {
		return ""
	}
	return fmt.Sprintf(
		"отказ в старте: сервер слушает %s без настроенных пользователей — база и "+
			"консоль кода были бы доступны без аутентификации. Создайте пользователя, "+
			"слушайте loopback (--host 127.0.0.1) или явно разрешите небезопасный "+
			"первичный bootstrap флагом --allow-insecure-bootstrap.", host)
}

func runServer(cmd *cobra.Command, args []string) error {
	runLog := oblog.Component("cli.run")
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var browserOnce sync.Once
	for {
		err := runServerGeneration(ctx, cmd, args, &browserOnce)
		var request *scheduledDemoResetRequest
		if !errors.As(err, &request) {
			return err
		}
		if ctx.Err() != nil {
			if recordErr := recordScheduledDemoResetResult(request, errors.New("scheduled demo reset canceled by process signal")); recordErr != nil {
				runLog.Error("record canceled scheduled demo reset failed", "err", recordErr)
			}
			return nil
		}

		report, resetErr := performScheduledDemoReset(ctx, request)
		if resetErr != nil {
			// A malformed backup or a competing consumer must not turn one failed
			// scheduled reset into permanent demo-site downtime. The next server
			// generation also performs restore-journal recovery before serving.
			runLog.Error("scheduled demo reset failed; restarting server without reporting reset success", "err", resetErr)
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		rows := 0
		for _, count := range report.Tables {
			rows += count
		}
		runLog.Info("scheduled demo reset completed; restarting server", "tables", len(report.Tables), "rows", rows)
		if ctx.Err() != nil {
			return nil
		}
	}
}

func runServerGeneration(ctx context.Context, cmd *cobra.Command, _ []string, browserOnce *sync.Once) (resultErr error) {
	runLog := oblog.Component("cli.run")
	baseID, _ := cmd.Flags().GetString("id")

	var dir, dsn, configSource, sqlitePath, dbType string
	var port int

	// If --id given, load settings from the ibases registry
	if baseID != "" {
		store, err := launcher.NewStore()
		if err != nil {
			return fmt.Errorf("ibases store: %w", err)
		}
		base, err := store.Get(baseID)
		if err != nil {
			return fmt.Errorf("база не найдена: %w\nИспользуйте 'onebase ibases list' для просмотра зарегистрированных баз", err)
		}
		dir = base.Path
		dsn = base.DB
		port = base.Port
		configSource = base.ConfigSource
		dbType = base.DBType
		sqlitePath = base.DBPath
		// Registry entries created before db_type was introduced used an empty
		// DB field for SQLite. Keep the lock target identical to runner.runArgs
		// and launcher.OpenDB, including their deterministic fallback path.
		if dbType == "" && dsn == "" {
			dbType = "sqlite"
			if sqlitePath == "" {
				sqlitePath = filepath.Join(os.TempDir(), "onebase_"+base.ID+".db")
			}
		}
		outf("Запуск базы: %s\n", base.Name)
	} else {
		dir, _ = cmd.Flags().GetString("project")
		dsn = dsnFromFlags(cmd)
		sqlitePath, _ = cmd.Flags().GetString("sqlite")
		port, _ = cmd.Flags().GetInt("port")
		configSource, _ = cmd.Flags().GetString("config-source")
		if sqlitePath != "" {
			dbType = "sqlite"
		}
	}

	var (
		db            *storage.DB
		databaseLease dblock.Lease
		poolCfg       storage.PoolConfig
		err           error
	)
	acquireDatabaseLease := func(shared bool) (dblock.Lease, error) {
		lease, canonical, leaseErr := acquireServerDatabaseLease(ctx, dbType, sqlitePath, dsn, shared)
		if leaseErr == nil && dbType == "sqlite" {
			sqlitePath = canonical
		}
		return lease, leaseErr
	}
	openDatabase := func(cfg storage.PoolConfig) (*storage.DB, error) {
		if dbType == "sqlite" {
			return storage.ConnectSQLite(ctx, sqlitePath)
		}
		return storage.ConnectWithPool(ctx, dsn, cfg)
	}

	// The normal start path is a database consumer, not a destructive writer.
	// Keep a shared lease continuously from marker inspection through serving so
	// configurator/CLI pools can coexist and no restore can start in between.
	// Upgrade to an exclusive recovery phase only when a durable marker proves
	// that startup recovery is actually required.
	databaseLease, err = acquireDatabaseLease(true)
	if err != nil {
		return fmt.Errorf("database lifetime lock: %w", err)
	}
	// Registered before db.Close below: LIFO cleanup closes all application
	// connections before publishing the database as available to another process.
	defer func() {
		if closeErr := databaseLease.Close(); closeErr != nil {
			wrapped := fmt.Errorf("release database lifetime lock: %w", closeErr)
			var resetRequest *scheduledDemoResetRequest
			if errors.As(resultErr, &resetRequest) {
				// Never preserve the typed reset request across an uncertain shared
				// lease release: errors.As in the outer loop is the reset permit.
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
			runLog.Warn("database lifetime lock release failed", "err", closeErr)
		}
	}()

	recoverRestore := func(recoveryDB *storage.DB) error {
		destinations := []string{recoveryDB.FilesDir()}
		if configSource != "database" {
			destinations = append(destinations, dir)
		}
		if recoveryErr := backup.RecoverPendingRestore(ctx, recoveryDB, destinations...); recoveryErr != nil {
			return fmt.Errorf("recover interrupted restore: %w", recoveryErr)
		}
		return nil
	}
	probeRestoreMarker := func() (bool, error) {
		if dbType == "sqlite" {
			return backup.HasPendingRestoreSQLite(ctx, sqlitePath)
		}
		return backup.HasPendingRestorePostgres(ctx, dsn)
	}
	// Do not use storage.Connect for this preflight: normal SQLite setup changes
	// journal mode and normal PostgreSQL setup performs compatibility DDL. A
	// pending restore must be detected before either kind of mutation occurs.
	markerPending, err := probeRestoreMarker()
	if err != nil {
		return fmt.Errorf("inspect restore recovery marker read-only: %w", err)
	}

	if markerPending {
		if closeErr := databaseLease.Close(); closeErr != nil {
			return fmt.Errorf("release shared database lifetime lock for recovery: %w", closeErr)
		}
		exclusiveLease, leaseErr := acquireDatabaseLease(false)
		if leaseErr != nil {
			return fmt.Errorf("database recovery requires exclusive lifetime lock: %w", leaseErr)
		}
		databaseLease = exclusiveLease

		recoveryDB, openErr := openDatabase(storage.PoolConfig{})
		if openErr != nil {
			return openErr
		}
		recoveryErr := recoverRestore(recoveryDB)
		recoveryDB.Close()
		if recoveryErr != nil {
			return recoveryErr
		}
		// No database connection may span the exclusive-to-shared handoff. A
		// competing restore can win the conversion gap on platforms that cannot
		// atomically downgrade; the final marker check below then observes its
		// completed generation or fails closed on its recovery journal.
		if err := databaseLease.Downgrade(ctx); err != nil {
			return fmt.Errorf("database lifetime lock downgrade: %w", err)
		}
		markerPending, err = probeRestoreMarker()
		if err != nil {
			return fmt.Errorf("recheck restore recovery marker read-only: %w", err)
		}
		if markerPending {
			return fmt.Errorf("%w: marker remains after startup recovery", backup.ErrRestoreRecoveryRequired)
		}
	}

	if dbType != "sqlite" && configSource != "database" {
		if ac, loadErr := project.LoadConfig(dir); loadErr == nil && ac.DB != nil {
			poolCfg = storage.PoolConfig{MaxConns: ac.DB.PoolMaxConns, MinConns: ac.DB.PoolMinConns}
		}
	}
	db, err = openDatabase(poolCfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if ps := db.PoolStats(); ps != nil {
		runLog.Info("postgresql pool configured", "max_conns", ps.MaxConns)
	}

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("auth schema: %w", err)
	}
	// Шифрование TOTP-seed'ов (SEC-04, #779): при заданном мастер-ключе тихо
	// перешифровываем открытые seed'ы существующим ключом (миграция не создаёт
	// новых ключей и не добавляет скрытых зависимостей). Если после этого
	// открытые seed'ы остаются — ключа нет; это постоянный admin-сигнал, что
	// конфиденциальность второго фактора ниже ожидаемой.
	if migrated, err := authRepo.MigratePlaintextTOTP(ctx); err != nil {
		runLog.Warn("миграция открытых TOTP-seed'ов не выполнена", "err", err)
	} else if migrated > 0 {
		runLog.Info("TOTP-seed'ы перешифрованы мастер-ключом", "мигрировано", migrated)
	}
	if plaintext, err := authRepo.CountPlaintextTOTP(ctx); err == nil && plaintext > 0 {
		runLog.Warn("открытые (незашифрованные) TOTP-seed'ы в базе — задайте мастер-ключ",
			"количество", plaintext,
			"выход", "ONEBASE_MASTER_KEY или ONEBASE_MASTER_KEY_FILE, затем перезапуск (план 83)")
	}
	// Сценарий обновления (#620): база, где второй фактор требовался, а привязка
	// шла на входе (до #577), после обновления получает SelfEnroll2FA=false — и
	// все, кто не успел привязать фактор, теряют вход, снять политику нечем.
	// Автоматически политику не меняем (это выбор администратора), но громко
	// предупреждаем и называем офлайн-выход.
	if policy := authRepo.AuthPolicy(ctx); policy.Enabled() {
		if cohort, err := authRepo.TwoFactorLockoutRisk(ctx, policy); err != nil {
			runLog.Warn("проверка риска блокировки вторым фактором не выполнена", "err", err)
		} else if cohort != "" {
			// Оба прежних совета были неверны: в админку в этом состоянии не
			// войти (круговой), а `user 2fa reset` не снимает требование
			// политики и потому из тупика не выводит — он в него ВВОДИТ (#615).
			runLog.Warn("политика требует второй фактор от когорты, у которой он не привязан ни у кого, при выключенной самопривязке — войти не сможет никто",
				"когорта", cohort,
				"выход", "onebase user 2fa self-enroll on — разрешить привязку второго фактора на входе")
		}
	}

	var proj *project.Project
	var cfgRepo *configdb.Repo
	var loadedConfigVersionID string
	if configSource == "database" {
		cfgRepo = configdb.New(db)
		if err := cfgRepo.EnsureSchema(ctx); err != nil {
			return fmt.Errorf("configdb schema: %w", err)
		}
		if err := cfgRepo.MigrateContent(ctx); err != nil {
			return fmt.Errorf("configdb migrate content: %w", err)
		}
		// Capture the version before loading. If deploy lands between this read
		// and watcher startup, the watcher will still observe and apply it.
		loadedConfigVersionID, err = latestConfigVersionID(ctx, cfgRepo)
		if err != nil {
			return fmt.Errorf("read current config version: %w", err)
		}
		proj, err = project.LoadFromDB(ctx, cfgRepo)
	} else {
		proj, err = project.Load(dir)
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	appCfg, err := project.LoadConfig(proj.Dir)
	if err != nil {
		return fmt.Errorf("load app config: %w", err)
	}

	// Реструктуризация схемы (план 81) при старте сервера идёт без права терять
	// данные и не молча. Прежде опции здесь были нулевыми: изменение, которое
	// округляет числа или удаляет колонку, применялось бы на ближайшем
	// рестарте — без флага, без --dry-run и без строчки в выводе. Теперь такие
	// изменения откладываются (данные и прежний тип колонки остаются на месте),
	// а администратор видит, что схема расходится с конфигурацией и чем это
	// лечится. Сужение точности числа с этой правкой тоже считается потерей
	// данных, см. SchemaChange.Destructive.
	var deferredSchema []string
	db.SetSchemaOptions(storage.SchemaOptions{
		Report: func(c storage.SchemaChange, applied bool) {
			if !applied {
				deferredSchema = append(deferredSchema, c.String())
			}
		},
	})
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return fmt.Errorf("migrate registers: %w", err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		return fmt.Errorf("migrate info registers: %w", err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		return fmt.Errorf("migrate constants: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}
	if err := db.EnsureStageHistorySchema(ctx); err != nil {
		return fmt.Errorf("stage history schema: %w", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		return fmt.Errorf("exchange schema: %w", err)
	}
	if err := db.EnsureIntakeSchema(ctx); err != nil {
		return fmt.Errorf("intake schema: %w", err)
	}
	// Отложенное печатаем после всех миграций: изменения приходят из разных
	// вызовов (сущности, регистры, ТЧ), и одним списком администратору понятнее.
	if len(deferredSchema) > 0 {
		errln("Схема базы расходится с конфигурацией — эти изменения потеряли бы данные и НЕ применены:")
		for _, s := range deferredSchema {
			errln("  " + s)
		}
		errln("Колонки остались как есть, сервер работает на прежней схеме.")
		errln("Посмотреть план: onebase migrate --project <кат> --dry-run")
		errln("Применить осознанно (сначала резервная копия): onebase migrate --allow-destructive")
	}

	// Sync roles from YAML. Malformed or unreadable role files must not leave
	// stale permissions active while startup appears successful.
	roles, err := auth.LoadRolesYAML(filepath.Join(proj.Dir, "roles"))
	if err != nil {
		return fmt.Errorf("load roles: %w", err)
	}
	if len(roles) > 0 {
		if err := authRepo.SyncRoles(ctx, roles); err != nil {
			return fmt.Errorf("sync roles: %w", err)
		}
	}

	// План 79F: strict-RLS чокпоинт (defense-in-depth). Под флагом
	// ONEBASE_STRICT_RLS storage.List fail-closed отклоняет список сущности со
	// строковой политикой, запрошенный без вычисленного доступа — чтобы обход
	// RLS новым list-хендлером всплывал сразу. По умолчанию выключено.
	if os.Getenv("ONEBASE_STRICT_RLS") != "" {
		guarded := access.GuardedEntitiesFromRoles(roles)
		db.SetStrictRLSGuard(func(name string) bool { return guarded[name] })
	}

	if err := db.EnsureAccountsTable(ctx); err != nil {
		return fmt.Errorf("accounts table: %w", err)
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		return fmt.Errorf("sync accounts: %w", err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return fmt.Errorf("migrate account registers: %w", err)
	}

	reg := registryFromProject(proj)

	// Внешний контур: печатные формы и отчёты из БД (вне конфигурации проекта).
	extRepo := extform.New(db)
	if err := extRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("extform schema: %w", err)
	}
	if extForms, extLayouts, err := extRepo.LoadEnabledPrintForms(ctx); err != nil {
		runLog.Warn("external print forms load failed", "err", err)
	} else {
		reg.SetExternalPrintForms(extForms)
		reg.SetExternalLayoutForms(extLayouts)
	}
	extRepRepo := extform.NewReports(db)
	if err := extRepRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("extform reports schema: %w", err)
	}
	if extReps, err := extRepRepo.LoadEnabledReports(ctx); err != nil {
		runLog.Warn("external reports load failed", "err", err)
	} else {
		reg.SetExternalReports(extReps)
	}
	extProcRepo := extform.NewProcessors(db)
	if err := extProcRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("extform processors schema: %w", err)
	}
	if extProcs, extPrograms, err := extProcRepo.LoadEnabled(ctx); err != nil {
		runLog.Warn("external processors load failed", "err", err)
	} else {
		reg.SetExternalProcessors(extProcs, extPrograms)
	}

	// app.yaml может задавать конфиг ИИ-помощника (llm, ключи через ${env:...})
	// и non-secret policy-настройки (ai). Применяем их к базе при старте:
	// таблица _settings не входит в .obz, поэтому для демо/прод это способ
	// донести настройки вместе с конфигурацией.
	for _, err := range applyAppAISettings(ctx, db, appCfg) {
		runLog.Warn("apply app ai setting failed", "err", err)
	}
	// file_storage.s3 (план 110, этап 2): подключаем S3-бэкенд image-блобов.
	// Ошибка конфигурации фатальна — иначе картинки молча перестанут работать.
	if err := applyFileStorageS3(db, appCfg); err != nil {
		return err
	}
	uiCfg := ui.Config{
		DSN:              dsn,
		DatabaseType:     runtimeDatabaseType(dbType),
		DatabaseLocation: runtimeDatabaseLocation(dbType, dsn, sqlitePath),
		ConfigSource:     configSource,
		ConfigLocation:   runtimeConfigLocation(configSource, dir, dbType, dsn, sqlitePath),
		PlatVersion:      version.String(),
		PlatCommit:       version.Commit(),
		PlatDate:         version.CommitDate(),
		PlatAuthor:       version.Author,
		PlatLicense:      version.License,
	}
	if appCfg != nil {
		uiCfg.AppName = appCfg.Name
		uiCfg.AppVersion = appCfg.Version
		uiCfg.AppAuthor = appCfg.Author
		uiCfg.AppCopyright = appCfg.Copyright
		uiCfg.AppLicense = appCfg.License
		uiCfg.AppSupport = appCfg.Support
		uiCfg.Lang = appCfg.Lang
		if appCfg.Logo != "" {
			uiCfg.Logo = filepath.Join(proj.Dir, appCfg.Logo)
		}
		if appCfg.Attachments != nil {
			if appCfg.Attachments.MaxFileSizeMB > 0 {
				uiCfg.MaxFileSizeMB = appCfg.Attachments.MaxFileSizeMB
			}
			uiCfg.AllowedTypes = appCfg.Attachments.AllowedTypes
		}
		uiCfg.Limits = runtimeLimitsFromApp(appCfg.Limits)
	}

	bundle, err := i18n.Load(i18n.EmbeddedLocales, filepath.Join(proj.Dir, "locales"))
	if err != nil {
		runLog.Warn("i18n load failed", "err", err)
	}
	uiCfg.Bundle = bundle

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	interp.StrictLexicalScope = appDSLStrictLexicalScope(appCfg)

	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		return fmt.Errorf("scheduled runs schema: %w", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return fmt.Errorf("attachments table: %w", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return fmt.Errorf("blobs table: %w", err)
	}
	sched := scheduler.New(db, reg, interp)
	if err := sched.LoadJobs(proj.ScheduledJobs); err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}
	demoResetRequests := make(chan *scheduledDemoResetRequest, 1)
	var demoResetRequestMu sync.Mutex
	demoResetRequested := false

	// Флаги --demo-* включают демо-режим независимо от источника конфига и
	// имеют приоритет над блоком demo: в app.yaml.
	demoBackupFlag, _ := cmd.Flags().GetString("demo-backup")
	demoScheduleFlag, _ := cmd.Flags().GetString("demo-schedule")
	demoMessageFlag, _ := cmd.Flags().GetString("demo-message")
	if demoBackupFlag != "" || demoScheduleFlag != "" || demoMessageFlag != "" {
		if appCfg == nil {
			appCfg = &project.AppConfig{}
		}
		if appCfg.Demo == nil {
			appCfg.Demo = &project.DemoConfig{}
		}
		appCfg.Demo.Enabled = true
		if demoBackupFlag != "" {
			appCfg.Demo.ResetBackup = demoBackupFlag
		}
		if demoScheduleFlag != "" {
			appCfg.Demo.ResetSchedule = demoScheduleFlag
		}
		if demoMessageFlag != "" {
			appCfg.Demo.Message = demoMessageFlag
		}
	}

	if appCfg != nil && appCfg.Demo != nil && appCfg.Demo.Enabled {
		// защита от случайной активации демо-режима на проде.
		if err := checkDemoEnv(os.Getenv("ONEBASE_ENV")); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "⚠️  ONEBASE: ДЕМО-РЕЖИМ. Данные сбрасываются по расписанию.")

		uiCfg.DemoMode = true
		// Безопасность: в демо-режиме обработки исполняет недоверенный
		// пользователь — ограничиваем файловые builtins каталогом базы,
		// чтобы DSL не мог читать/писать произвольные файлы на сервере.
		interpreter.SetFileSandbox(proj.Dir)
		msg := appCfg.Demo.Message
		if msg == "" {
			msg = "Данные сбрасываются каждую ночь в 02:00"
		}
		uiCfg.DemoMessage = msg

		schedule := appCfg.Demo.ResetSchedule
		if schedule == "" {
			schedule = "0 2 * * *"
		}
		backupPath := ""
		if appCfg.Demo.ResetBackup != "" {
			// Абсолютный путь берём как есть; относительный — от каталога проекта.
			// Важно для --config-source database (dir = "."), где иначе абсолютный
			// путь превратился бы в относительный.
			if filepath.IsAbs(appCfg.Demo.ResetBackup) {
				backupPath = filepath.Clean(appCfg.Demo.ResetBackup)
			} else {
				backupPath, err = filepath.Abs(filepath.Join(dir, appCfg.Demo.ResetBackup))
				if err != nil {
					return fmt.Errorf("resolve demo reset backup path: %w", err)
				}
			}
		}
		if err := sched.RegisterGoJob("DemoReset", "Сброс демо-данных", schedule, func(ctx context.Context) error {
			if backupPath == "" {
				return nil
			}
			runInfo, ok := scheduler.CurrentRun(ctx)
			if !ok {
				return errors.New("scheduled demo reset run identity is unavailable")
			}
			request := &scheduledDemoResetRequest{
				dbType:     dbType,
				dsn:        dsn,
				sqlitePath: sqlitePath,
				filesDir:   db.FilesDir(),
				backupPath: backupPath,
				run:        runInfo,
			}
			demoResetRequestMu.Lock()
			defer demoResetRequestMu.Unlock()
			if demoResetRequested {
				return errors.New("scheduled demo reset is already pending")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case demoResetRequests <- request:
				demoResetRequested = true
				// Finalize the scheduler callback as accepted, never as a false
				// reset success. The offline phase later refines this same row.
				return scheduler.Accepted("offline demo reset request accepted")
			}
		}); err != nil {
			runLog.Warn("demo reset job registration failed", "err", err)
		}
	}

	if appCfg != nil && appCfg.Backup != nil {
		target := backup.AutoTarget{
			DBType:     dbType,
			DSN:        dsn,
			SQLitePath: sqlitePath,
			ProjectDir: dir,
		}
		if err := backup.RegisterAutoBackup(appCfg.Backup, target, sched); err != nil {
			runLog.Warn("auto backup job registration failed", "err", err)
		}
	}

	if appCfg != nil && appCfg.Email != nil {
		m := mailer.New(mailer.Config{
			SMTPHost:    appCfg.Email.SMTPHost,
			SMTPPort:    appCfg.Email.SMTPPort,
			SMTPUser:    appCfg.Email.SMTPUser,
			SMTPPass:    appCfg.Email.SMTPPass,
			FromName:    appCfg.Email.FromName,
			FromAddress: appCfg.Email.FromAddress,
		})
		uiCfg.Mailer = m
		sched.SetMailer(m)
	}

	// Исходящие веб-хуки из app.yaml (план 29): асинхронная отправка с retry,
	// журнал — в _webhook_log.
	if appCfg != nil && len(appCfg.Webhooks) > 0 {
		dbRef := db
		d := webhook.New(appCfg.Webhooks, func(e webhook.LogEntry) {
			logCtx, logCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer logCancel()
			dbRef.LogWebhook(logCtx, storage.WebhookLogEntry{
				Webhook: e.Webhook, Event: e.Event, Entity: e.Entity, RecordID: e.RecordID,
				URL: e.URL, StatusCode: e.StatusCode, Error: e.Error,
				Duration: e.Duration, Attempts: e.Attempts,
			})
		})
		// Предохранитель сети (план 62): хуки уходят только при разрешённой сети.
		d.SetGuard(func() bool { return dbRef.GetNetworkEnabled(context.Background()) })
		uiCfg.Webhooks = d
		outf("веб-хуки: настроено %d\n", len(appCfg.Webhooks))
		if !db.GetNetworkEnabled(ctx) {
			outln("  ⚠ сеть заблокирована предохранителем — хуки не будут отправляться,\n" +
				"    пока не включить «Разрешить сетевые операции» в конфигураторе\n" +
				"    или командой: onebase settings set net.enabled вкл")
		}
	}

	host, _ := cmd.Flags().GetString("host")
	allowInsecureBootstrap, _ := cmd.Flags().GetBool("allow-insecure-bootstrap")
	// Footgun-страж (план 53, анализ §2.7; ужесточён по SEC-03): без
	// пользователей auth выключен целиком (включая консоль кода). Слушать в
	// таком виде не-loopback адрес почти всегда ошибка оператора, поэтому по
	// умолчанию ОТКАЗЫВАЕМ в старте. Осознанный первичный bootstrap на внешнем
	// адресе разрешается явным флагом --allow-insecure-bootstrap.
	if !api.IsLoopbackHost(host) {
		hasUsers, _ := authRepo.HasUsers(ctx)
		if refusal := bootstrapRefusal(host, hasUsers, allowInsecureBootstrap); refusal != "" {
			return errors.New(refusal)
		}
		if !hasUsers { // разрешено флагом — но предупреждаем громко
			fmt.Fprintf(os.Stderr, "ПРЕДУПРЕЖДЕНИЕ (--allow-insecure-bootstrap): сервер слушает %s\n"+
				"без настроенных пользователей — база и консоль кода доступны без\n"+
				"аутентификации. Создайте администратора немедленно.\n", host)
		}
	}

	srv := api.New(reg, db, interp, authRepo, host, port, uiCfg, sched)

	// Опциональный hot reload (см. --watch). Перечитываем project metadata/DSL
	// и scheduled jobs. Статические app.yaml-настройки, роли, локали и DDL не
	// меняем в живом процессе: для них нужен restart/deploy.
	//   file     → fsnotify по каталогу проекта (.yaml/.os).
	//   database → опрос _config_versions: после `onebase deploy`/rollback
	//              появляется новая версия. Схему трогать не нужно — миграции
	//              выполняет deploy ДО создания версии конфигурации.
	var stopWatch func()
	defer func() {
		if stopWatch != nil {
			stopWatch()
		}
	}()
	if watchEnabled, _ := cmd.Flags().GetBool("watch"); watchEnabled {
		var reloadMu sync.Mutex
		applyProject := func(newProj *project.Project) error {
			defer newProj.Close()
			reloadMu.Lock()
			defer reloadMu.Unlock()
			if _, err := project.LoadConfig(newProj.Dir); err != nil {
				return fmt.Errorf("validate app config: %w", err)
			}
			return reloadProjectRuntime(reg, sched, srv, newProj)
		}

		switch configSource {
		case "file":
			watchCtx, watchCancel := context.WithCancel(ctx)
			reload := func() {
				newProj, err := project.Load(dir)
				if err != nil {
					runLog.Warn("watch reload failed", "err", err)
					return
				}
				if err := applyProject(newProj); err != nil {
					runLog.Warn("watch publish failed", "err", err)
					return
				}
				outln("[watch] метаданные и расписания перезагружены; app.yaml/roles/locales требуют рестарта")
			}
			watchDone, err := devserver.WatchProjectContext(watchCtx, dir, reload)
			if err != nil {
				watchCancel()
				runLog.Warn("watch init failed", "err", err)
			} else {
				var stopOnce sync.Once
				stopWatch = func() {
					stopOnce.Do(func() {
						watchCancel()
						<-watchDone
					})
				}
				outf("[watch] отслеживаем %s — metadata/DSL/scheduled подхватятся без рестарта\n", dir)
			}
		case "database":
			reloadCtx, reloadCancel := context.WithCancel(ctx)
			reloadDone := make(chan struct{})
			go func() {
				defer close(reloadDone)
				watchConfigVersions(reloadCtx, cfgRepo, loadedConfigVersionID, configReloadInterval, func() error {
					newProj, err := project.LoadFromDB(reloadCtx, cfgRepo)
					if err != nil {
						runLog.Warn("db watch reload failed", "err", err)
						return err
					}
					if err := applyProject(newProj); err != nil {
						runLog.Warn("db watch publish failed", "err", err)
						return err
					}
					outln("[watch] metadata/DSL/scheduled перезагружены из БД; app.yaml/roles/locales требуют рестарта")
					return nil
				})
			}()
			var stopOnce sync.Once
			stopWatch = func() {
				stopOnce.Do(func() {
					reloadCancel()
					<-reloadDone
				})
			}
			outln("[watch] отслеживаем версии конфигурации в БД — deploy подхватится без рестарта")
		}
	}

	listener, err := srv.Listen()
	if err != nil {
		return fmt.Errorf("listen on %s:%d: %w", host, port, err)
	}
	defer func() { _ = listener.Close() }()

	schedCtx, schedCancel := context.WithCancel(ctx)
	defer schedCancel()
	schedDone := make(chan error, 1)
	schedReady := make(chan struct{})
	go func() {
		schedDone <- sched.RunReady(schedCtx, schedReady)
	}()
	select {
	case <-schedReady:
	case startErr := <-schedDone:
		if startErr == nil {
			return nil
		}
		return fmt.Errorf("start scheduler: %w", startErr)
	}

	outf("onebase running on %s:%d\n", host, port)
	if srv.H2CEnabled() {
		outln("  HTTP/2 без TLS (h2c) включён для апстрима (ONEBASE_H2C) — см. docs/reverse-proxy.md")
	}
	serveErr := make(chan error, 1)
	go func() {
		listenErr := srv.Serve(listener)
		if errors.Is(listenErr, http.ErrServerClosed) {
			listenErr = nil
		} else if listenErr != nil {
			runLog.Error("server failed", "err", listenErr)
		}
		serveErr <- listenErr
	}()
	if openBrowser, _ := cmd.Flags().GetBool("open"); openBrowser && browserOnce != nil {
		browserOnce.Do(func() { openInBrowser(host, port) })
	}
	var listenErr error
	var demoResetRequest *scheduledDemoResetRequest
	serveResultReceived := false
	terminalStop := false
	select {
	case <-ctx.Done():
		terminalStop = true
	case <-srv.Done():
		terminalStop = true
		// Аутентифицированный launcher попросил базу завершиться. Дальше идёт
		// тот же graceful shutdown, что и для SIGTERM: задания и HTTP-запросы
		// получают время закончить работу, база не убивается по номеру порта.
	case listenErr = <-serveErr:
		serveResultReceived = true
		terminalStop = true
		// Bind/listener failure is terminal too. Waiting only for a signal here
		// would leave a headless process holding the DB and scheduler forever.
	case demoResetRequest = <-demoResetRequests:
		// The Go job has only handed off a request. The destructive work starts
		// after this generation fully stops and all its deferred cleanup runs.
	}
	// Permanently reject cron callbacks and HTTP RunNow requests before either
	// drain starts. Existing jobs still have live UI/webhook dependencies while
	// the scheduler waits for them; finishShutdown cannot reopen this seal.
	sched.BeginQuiesce()
	if stopWatch != nil {
		stopWatch()
		stopWatch = nil
	}
	schedCancel()
	schedulerErr := <-schedDone
	if demoResetRequest == nil {
		// The terminal event may have won the first select concurrently with the
		// handoff. Scheduler drain proves no later enqueue can still appear.
		select {
		case demoResetRequest = <-demoResetRequests:
		default:
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	err = srv.Shutdown(shutdownCtx)
	if !serveResultReceived {
		// Shutdown closes the listener, so ListenAndServe must now report. Waiting
		// here also captures a listener failure that raced with the reset request.
		listenErr = <-serveErr
	}
	shutdownErr := errors.Join(listenErr, err, schedulerErr)
	if demoResetRequest == nil {
		return shutdownErr
	}
	abortReset := func(cause error) error {
		return updateScheduledDemoResetRun(demoResetRequest, db, cause)
	}
	if terminalStop {
		return errors.Join(shutdownErr, abortReset(errors.New("scheduled demo reset canceled because server is stopping")))
	}
	if shutdownErr != nil {
		abortErr := errors.New("scheduled demo reset aborted because server shutdown was not clean")
		return errors.Join(shutdownErr, abortReset(errors.Join(abortErr, shutdownErr)))
	}
	// A signal or authenticated launcher quit always wins over an overlapping
	// cron reset, even if the reset request happened to win the first select.
	if ctx.Err() != nil {
		return abortReset(errors.New("scheduled demo reset canceled by process signal"))
	}
	select {
	case <-srv.Done():
		return abortReset(errors.New("scheduled demo reset canceled by launcher stop request"))
	default:
	}
	return demoResetRequest
}

// configReloadInterval — период опроса истории версий конфигурации в
// database-режиме при --watch.
const configReloadInterval = 5 * time.Second

// latestConfigVersionID возвращает ID самой свежей версии конфигурации или "",
// если история пока пуста.
func latestConfigVersionID(ctx context.Context, cfgRepo *configdb.Repo) (string, error) {
	vs, err := cfgRepo.ListVersions(ctx, 1)
	if err != nil {
		return "", err
	}
	if len(vs) == 0 {
		return "", nil
	}
	return vs[0].ID, nil
}

// watchConfigVersions опрашивает историю версий конфигурации раз в interval и
// вызывает onChange при появлении новой версии (её создают `onebase deploy` и
// rollback). Возвращается при отмене ctx. Схему БД не трогает — DDL-миграции
// выполняет deploy ДО создания версии.
func watchConfigVersions(ctx context.Context, cfgRepo *configdb.Repo, last string, interval time.Duration, onChange func() error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := latestConfigVersionID(ctx, cfgRepo)
			if err != nil {
				oblog.Component("cli.hotreload").Warn("config version poll failed", "err", err)
				continue
			}
			if cur != "" && cur != last {
				if err := onChange(); err == nil {
					last = cur
				}
			}
		}
	}
}

func runtimeLimitsFromApp(l *project.LimitsConfig) ui.RuntimeLimits {
	if l == nil {
		return ui.RuntimeLimits{}
	}
	return ui.RuntimeLimits{
		RequestTimeoutSec:      l.RequestTimeoutSec,
		ReportTimeoutSec:       l.ReportTimeoutSec,
		ReportMaxRows:          l.ReportMaxRows,
		ReportConcurrency:      l.ReportConcurrency,
		ExportTimeoutSec:       l.ExportTimeoutSec,
		ExportMaxRows:          l.ExportMaxRows,
		ExportConcurrency:      l.ExportConcurrency,
		ProcessorTimeoutSec:    l.ProcessorTimeoutSec,
		ProcessorConcurrency:   l.ProcessorConcurrency,
		HTTPServiceTimeoutSec:  l.HTTPServiceTimeoutSec,
		HTTPServiceConcurrency: l.HTTPServiceConcurrency,
		SlowOperationMS:        l.SlowOperationMS,
	}
}
