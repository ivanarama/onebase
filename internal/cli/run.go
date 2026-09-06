package cli

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/jobqueue"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
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
	db, err := openCLIStorageReadOnly(ctx, request.dbType, request.sqlitePath, request.dsn)
	if err != nil {
		return fmt.Errorf("record scheduled demo reset result open database: %w", err)
	}
	completionErr := updateScheduledDemoResetRun(request, db, runErr)
	db.Close()
	return completionErr
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

	// Open the exact canonical target returned with the exclusive lease. The
	// helper recovers a prior durable intent first, then publishes/checks the
	// schema barrier before DemoReset performs destructive import/migration.
	db, err := openExclusiveRecoveryStorage(ctx, request.dbType, sqlitePath, request.dsn, request.filesDir)
	if err != nil {
		openErr := errors.Join(err, lease.Close())
		warnScheduledDemoResetResult(request, openErr)
		return nil, openErr
	}
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

// migrateServerSchema приводит схему базы к конфигурации на старте сервера и
// докладывает администратору о том, что применить не удалось.
//
// Вынесено из runServerGeneration отдельной фазой: сборка сервера растёт под
// бюджетом (#787, план 137), и каждая новая строка в ней делает отложенный
// рефакторинг дороже. Фаза цельная — миграции и отчёт о них, — поэтому
// отделяется без остатка.
//
// Реструктуризация (план 81) при старте идёт без права терять данные и не
// молча. Прежде опции здесь были нулевыми: изменение, которое округляет числа
// или удаляет колонку, применялось бы на ближайшем рестарте — без флага, без
// --dry-run и без строчки в выводе. Теперь такие изменения откладываются
// (данные и прежний тип колонки остаются на месте), а администратор видит, что
// схема расходится с конфигурацией и чем это лечится. Сужение точности числа с
// этой правкой тоже считается потерей данных, см. SchemaChange.Destructive.
func migrateServerSchema(ctx context.Context, db *storage.DB, proj *project.Project) error {
	var deferredSchema []string
	db.SetSchemaOptions(storage.SchemaOptions{
		Report: func(c storage.SchemaChange, applied bool) {
			if !applied {
				deferredSchema = append(deferredSchema, c.String())
			}
		},
	})
	steps := []struct {
		what string
		fn   func() error
	}{
		{"migrate", func() error { return db.Migrate(ctx, proj.Entities) }},
		{"migrate registers", func() error { return db.MigrateRegisters(ctx, proj.Registers) }},
		{"migrate info registers", func() error { return db.MigrateInfoRegisters(ctx, proj.InfoRegisters) }},
		{"migrate constants", func() error { return db.MigrateConstants(ctx, proj.Constants) }},
		{"audit schema", func() error { return db.EnsureAuditSchema(ctx) }},
		{"stage history schema", func() error { return db.EnsureStageHistorySchema(ctx) }},
		{"exchange schema", func() error { return db.EnsureExchangeSchema(ctx) }},
		{"intake schema", func() error { return db.EnsureIntakeSchema(ctx) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.what, err)
		}
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
	return nil
}

func probeServerSchemaRevision(ctx context.Context, dbType, sqlitePath, dsn string) (storage.SchemaRevisionState, error) {
	if dbType == "sqlite" {
		return storage.ProbeSQLiteSchemaRevision(ctx, sqlitePath)
	}
	return storage.ProbePostgresSchemaRevision(ctx, dsn)
}

func guardKnownServerSchemaRevision(ctx context.Context, dbType, sqlitePath, dsn string) error {
	state, err := probeServerSchemaRevision(ctx, dbType, sqlitePath, dsn)
	if err != nil || !state.Known {
		return err
	}
	return guardProbedSchemaRevision(state)
}

func probeServerRestoreMarker(ctx context.Context, dbType, sqlitePath, dsn string) (bool, error) {
	if dbType == "sqlite" {
		return backup.HasPendingRestoreSQLite(ctx, sqlitePath)
	}
	return backup.HasPendingRestorePostgres(ctx, dsn)
}

// recoverServerRestoreIfPending upgrades the continuously-held shared lifetime
// lease only when a raw marker requires recovery. It keeps the resulting
// exclusive lease for schema-barrier publication by the next startup phase.
func recoverServerRestoreIfPending(ctx context.Context, dbType string, sqlitePath *string, dsn, configSource, dir string, lease *dblock.Lease) (leaseExclusive bool, resultErr error) {
	pending, err := probeServerRestoreMarker(ctx, dbType, *sqlitePath, dsn)
	if err != nil {
		return false, fmt.Errorf("inspect restore recovery marker before database open: %w", err)
	}
	if !pending {
		return false, nil
	}
	if err := (*lease).Close(); err != nil {
		return false, fmt.Errorf("release shared database lifetime lock for recovery: %w", err)
	}
	*lease = nil
	exclusiveLease, canonical, err := acquireServerDatabaseLease(ctx, dbType, *sqlitePath, dsn, false)
	if err != nil {
		return false, fmt.Errorf("database recovery requires exclusive lifetime lock: %w", err)
	}
	*lease = exclusiveLease
	if dbType == "sqlite" {
		*sqlitePath = canonical
	}
	pending, err = probeServerRestoreMarker(ctx, dbType, *sqlitePath, dsn)
	if err != nil {
		return true, fmt.Errorf("recheck restore recovery marker under exclusive lease: %w", err)
	}
	if !pending {
		return true, nil
	}
	if err := guardKnownServerSchemaRevision(ctx, dbType, *sqlitePath, dsn); err != nil {
		return true, err
	}
	var recoveryDB *storage.DB
	if dbType == "sqlite" {
		recoveryDB, err = storage.ConnectSQLite(ctx, *sqlitePath)
	} else {
		recoveryDB, err = storage.ConnectWithPool(ctx, dsn, storage.PoolConfig{})
	}
	if err != nil {
		return true, err
	}
	destinations := []string{recoveryDB.FilesDir()}
	if configSource != "database" {
		destinations = append(destinations, dir)
	}
	recoveryErr := backup.RecoverPendingRestore(ctx, recoveryDB, destinations...)
	recoveryDB.Close()
	if recoveryErr != nil {
		return true, fmt.Errorf("recover interrupted restore: %w", recoveryErr)
	}
	return true, nil
}

// prepareServerSchemaRevision publishes a minimum-reader barrier under an
// exclusive lifetime lease before normal Connect/schema setup. leaseExclusive
// is true when startup recovery already owns that lease; keeping it across the
// recovery-to-revision handoff prevents an older consumer entering in between.
// Pointers make ownership transfer explicit: after a successful shared release
// *lease is nil until the exclusive lease has been acquired.
func prepareServerSchemaRevision(ctx context.Context, dbType string, sqlitePath *string, dsn string, lease *dblock.Lease, leaseExclusive bool) (prepareAfterOpen bool, resultErr error) {
	probeRestore := func() (bool, error) {
		return probeServerRestoreMarker(ctx, dbType, *sqlitePath, dsn)
	}
	probeRevision := func() (storage.SchemaRevisionState, error) {
		return probeServerSchemaRevision(ctx, dbType, *sqlitePath, dsn)
	}
	prepareRevision := func() (storage.SchemaRevisionState, error) {
		if dbType == "sqlite" {
			return storage.PrepareSQLiteSchemaRevision(ctx, *sqlitePath)
		}
		return storage.PreparePostgresSchemaRevision(ctx, dsn)
	}

	if leaseExclusive {
		pending, err := probeRestore()
		if err != nil {
			return false, fmt.Errorf("recheck restore recovery marker before schema upgrade: %w", err)
		}
		if pending {
			return false, fmt.Errorf("%w: restore marker remains before schema upgrade", backup.ErrRestoreRecoveryRequired)
		}
	}
	state, err := probeRevision()
	if err != nil {
		return false, err
	}
	if state.Known && state.Revision < 0 {
		return false, guardProbedSchemaRevision(state)
	}
	prepareAfterOpen = dbType == "sqlite" && storage.IsInMemorySQLitePath(*sqlitePath) && state.NeedsUpgrade()
	if prepareAfterOpen {
		return true, guardProbedSchemaRevision(state)
	}
	for {
		if state.Known && state.Revision < 0 {
			return false, guardProbedSchemaRevision(state)
		}
		if !leaseExclusive {
			if !state.NeedsUpgrade() {
				return false, guardProbedSchemaRevision(state)
			}
			if err := (*lease).Close(); err != nil {
				return false, fmt.Errorf("release shared database lifetime lock for schema upgrade: %w", err)
			}
			*lease = nil
			exclusiveLease, canonical, err := acquireServerDatabaseLease(ctx, dbType, *sqlitePath, dsn, false)
			if err != nil {
				return false, fmt.Errorf("schema revision upgrade requires exclusive database access: %w", err)
			}
			*lease = exclusiveLease
			leaseExclusive = true
			if dbType == "sqlite" {
				*sqlitePath = canonical
			}
			pending, err := probeRestore()
			if err != nil {
				return false, fmt.Errorf("recheck restore recovery marker before schema upgrade: %w", err)
			}
			if pending {
				return false, fmt.Errorf("%w: restore marker appeared before schema upgrade", backup.ErrRestoreRecoveryRequired)
			}
			state, err = probeRevision()
			if err != nil {
				return false, err
			}
			continue
		}
		if state.Known {
			if err := guardProbedSchemaRevision(state); err != nil {
				return false, err
			}
		}
		if state.NeedsUpgrade() {
			state, err = prepareRevision()
			if err != nil {
				return false, err
			}
		}
		if err := guardProbedSchemaRevision(state); err != nil {
			return false, err
		}
		// No pool is open across Downgrade: every implementation may have a
		// conversion gap. Recheck both durable markers while holding the resulting
		// shared lease and repeat if a competing restore replaced the generation.
		if err := (*lease).Downgrade(ctx); err != nil {
			return false, fmt.Errorf("downgrade schema-upgrade database lease: %w", err)
		}
		leaseExclusive = false
		pending, err := probeRestore()
		if err != nil {
			return false, fmt.Errorf("recheck restore marker after schema-upgrade lease downgrade: %w", err)
		}
		if pending {
			return false, fmt.Errorf("%w: restore marker appeared during schema-upgrade lease downgrade", backup.ErrRestoreRecoveryRequired)
		}
		state, err = probeRevision()
		if err != nil {
			return false, err
		}
	}
}

func finishServerSchemaOpen(ctx context.Context, db *storage.DB, prepareAfterOpen bool) error {
	if prepareAfterOpen {
		if err := stampSchemaRevision(ctx, db); err != nil {
			return err
		}
	}
	return guardSchemaRevision(ctx, db)
}

func loadServerPoolConfig(dbType, configSource, dir string) storage.PoolConfig {
	if dbType == "sqlite" || configSource == "database" {
		return storage.PoolConfig{}
	}
	if ac, err := project.LoadConfig(dir); err == nil && ac.DB != nil {
		return storage.PoolConfig{MaxConns: ac.DB.PoolMaxConns, MinConns: ac.DB.PoolMinConns}
	}
	return storage.PoolConfig{}
}

func runServerGeneration(ctx context.Context, cmd *cobra.Command, _ []string, browserOnce *sync.Once) (resultErr error) {
	runLog := oblog.Component("cli.run")
	launchCfg, err := resolveServerLaunchConfig(cmd)
	if err != nil {
		return err
	}
	database, err := openServerDatabase(ctx, &launchCfg, runLog)
	if err != nil {
		return err
	}
	defer database.close(&resultErr, runLog)
	db := database.db
	dir, configSource, port := launchCfg.dir, launchCfg.configSource, launchCfg.port

	prepared, err := prepareServerProject(ctx, db, launchCfg, runLog)
	if err != nil {
		return err
	}
	defer prepared.project.Close()
	cfgRepo, loadedConfigVersionID, reg := prepared.configRepo, prepared.loadedConfigVersionID, prepared.registry

	runtimeState, err := prepareServerRuntime(ctx, cmd, db, launchCfg, prepared, runLog)
	if err != nil {
		return err
	}
	application, sched := runtimeState.application, runtimeState.scheduler
	demoResetRequests := runtimeState.demoResetRequests

	host, _ := cmd.Flags().GetString("host")
	srv := application.Server()

	watchEnabled, _ := cmd.Flags().GetBool("watch")
	stopWatch := startServerWatch(ctx, watchEnabled, configSource, dir, cfgRepo, loadedConfigVersionID, reg, sched, srv, runLog)
	defer func() {
		if stopWatch != nil {
			stopWatch()
		}
	}()

	application.SetBeforeDrain(func() {
		if stopWatch != nil {
			stopWatch()
			stopWatch = nil
		}
	})
	application.SetQueueErrorHandler(func(queueErr error) {
		runLog.Warn("job queue drain", "err", queueErr)
	})
	if err := application.Run(ctx); err != nil {
		return fmt.Errorf("start app on %s:%d: %w", host, port, err)
	}

	outf("onebase running on %s:%d\n", host, port)
	if srv.H2CEnabled() {
		outln("  HTTP/2 без TLS (h2c) включён для апстрима (ONEBASE_H2C) — см. docs/reverse-proxy.md")
	}
	if openBrowser, _ := cmd.Flags().GetBool("open"); openBrowser && browserOnce != nil {
		browserOnce.Do(func() { openInBrowser(host, port) })
	}
	var demoResetRequest *scheduledDemoResetRequest
	terminalStop := false
	select {
	case <-ctx.Done():
		terminalStop = true
	case <-srv.Done():
		terminalStop = true
		// Аутентифицированный launcher попросил базу завершиться. Дальше идёт
		// тот же graceful shutdown, что и для SIGTERM: задания и HTTP-запросы
		// получают время закончить работу, база не убивается по номеру порта.
	case <-application.Stopped():
		terminalStop = true
		// Bind/listener failure is terminal too. Waiting only for a signal here
		// would leave a headless process holding the DB and scheduler forever.
	case demoResetRequest = <-demoResetRequests:
		// The Go job has only handed off a request. The destructive work starts
		// after this generation fully stops and all its deferred cleanup runs.
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	shutdownErr := application.Close(shutdownCtx)
	if demoResetRequest == nil {
		// Scheduler drain proves no later reset handoff can still appear.
		select {
		case demoResetRequest = <-demoResetRequests:
		default:
		}
	}
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

// runtimeQueueConfigFromApp переводит блок `queue:` из app.yaml в настройки
// пула. Нет блока — значения по умолчанию (4 исполнителя); `workers: 0` —
// очередь выключена совсем, и постановка задачи честно отказывает.
func runtimeQueueConfigFromApp(app *project.AppConfig) jobqueue.Config {
	cfg := jobqueue.DefaultConfig()
	if app == nil || app.Queue == nil {
		return cfg
	}
	q := app.Queue
	if q.Workers != nil {
		cfg.Workers = *q.Workers
	}
	if q.PollIntervalSec > 0 {
		cfg.PollInterval = time.Duration(q.PollIntervalSec) * time.Second
	}
	if q.LeaseSec > 0 {
		cfg.Lease = time.Duration(q.LeaseSec) * time.Second
	}
	if q.MaxAttempts > 0 {
		cfg.MaxAttempts = q.MaxAttempts
	}
	if q.RetryBackoffSec > 0 {
		cfg.RetryBackoff = time.Duration(q.RetryBackoffSec) * time.Second
	}
	if q.RetentionDays > 0 {
		cfg.Retention = time.Duration(q.RetentionDays) * 24 * time.Hour
	}
	if q.DrainTimeoutSec > 0 {
		cfg.DrainTimeout = time.Duration(q.DrainTimeoutSec) * time.Second
	}
	return cfg
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
