package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

type serverLaunchConfig struct {
	dir          string
	dsn          string
	configSource string
	sqlitePath   string
	dbType       string
	port         int
}

func resolveServerLaunchConfig(cmd *cobra.Command) (serverLaunchConfig, error) {
	baseID, _ := cmd.Flags().GetString("id")
	if baseID == "" {
		cfg := serverLaunchConfig{}
		cfg.dir, _ = cmd.Flags().GetString("project")
		cfg.dsn = dsnFromFlags(cmd)
		cfg.sqlitePath, _ = cmd.Flags().GetString("sqlite")
		cfg.port, _ = cmd.Flags().GetInt("port")
		cfg.configSource, _ = cmd.Flags().GetString("config-source")
		if cfg.sqlitePath != "" {
			cfg.dbType = "sqlite"
		}
		return cfg, nil
	}

	store, err := launcher.NewStore()
	if err != nil {
		return serverLaunchConfig{}, fmt.Errorf("ibases store: %w", err)
	}
	base, err := store.Get(baseID)
	if err != nil {
		return serverLaunchConfig{}, fmt.Errorf("база не найдена: %w\nИспользуйте 'onebase ibases list' для просмотра зарегистрированных баз", err)
	}
	cfg := serverLaunchConfig{
		dir: base.Path, dsn: base.DB, port: base.Port, configSource: base.ConfigSource,
		dbType: base.DBType, sqlitePath: base.DBPath,
	}
	// Old registry entries used an empty DB field for SQLite.
	if cfg.dbType == "" && cfg.dsn == "" {
		cfg.dbType = "sqlite"
		if cfg.sqlitePath == "" {
			cfg.sqlitePath = filepath.Join(os.TempDir(), "onebase_"+base.ID+".db")
		}
	}
	outf("Запуск базы: %s\n", base.Name)
	return cfg, nil
}

type openServerDatabaseResult struct {
	db    *storage.DB
	lease dblock.Lease
}

func openServerDatabase(ctx context.Context, cfg *serverLaunchConfig, log *slog.Logger) (*openServerDatabaseResult, error) {
	acquireLease := func(shared bool) (dblock.Lease, error) {
		lease, canonical, err := acquireServerDatabaseLease(ctx, cfg.dbType, cfg.sqlitePath, cfg.dsn, shared)
		if err == nil && cfg.dbType == "sqlite" {
			cfg.sqlitePath = canonical
		}
		return lease, err
	}
	lease, err := acquireLease(true)
	if err != nil {
		return nil, fmt.Errorf("database lifetime lock: %w", err)
	}
	fail := func(err error) (*openServerDatabaseResult, error) {
		if closeErr := lease.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("release database lifetime lock: %w", closeErr))
		}
		return nil, err
	}

	exclusive, err := recoverServerRestoreIfPending(ctx, cfg.dbType, &cfg.sqlitePath, cfg.dsn, cfg.configSource, cfg.dir, &lease)
	if err != nil {
		return fail(err)
	}
	prepareAfterOpen, err := prepareServerSchemaRevision(ctx, cfg.dbType, &cfg.sqlitePath, cfg.dsn, &lease, exclusive)
	if err != nil {
		return fail(err)
	}

	poolCfg := loadServerPoolConfig(cfg.dbType, cfg.configSource, cfg.dir)
	var db *storage.DB
	if cfg.dbType == "sqlite" {
		db, err = storage.ConnectSQLite(ctx, cfg.sqlitePath)
	} else {
		db, err = storage.ConnectWithPool(ctx, cfg.dsn, poolCfg)
	}
	if err != nil {
		return fail(err)
	}
	if ps := db.PoolStats(); ps != nil {
		log.Info("postgresql pool configured", "max_conns", ps.MaxConns)
	}
	if err := finishServerSchemaOpen(ctx, db, prepareAfterOpen); err != nil {
		db.Close()
		return fail(err)
	}
	return &openServerDatabaseResult{db: db, lease: lease}, nil
}

func (r *openServerDatabaseResult) close(resultErr *error, log *slog.Logger) {
	if r == nil {
		return
	}
	if r.db != nil {
		r.db.Close()
	}
	if r.lease == nil {
		return
	}
	if err := r.lease.Close(); err != nil {
		wrapped := fmt.Errorf("release database lifetime lock: %w", err)
		var resetRequest *scheduledDemoResetRequest
		if errors.As(*resultErr, &resetRequest) {
			*resultErr = wrapped
		} else {
			*resultErr = errors.Join(*resultErr, wrapped)
		}
		log.Warn("database lifetime lock release failed", "err", err)
	}
}

type preparedServerProject struct {
	authRepo              *auth.Repo
	project               *project.Project
	appConfig             *project.AppConfig
	configRepo            *configdb.Repo
	loadedConfigVersionID string
	registry              *runtime.Registry
}

func prepareServerProject(ctx context.Context, db *storage.DB, cfg serverLaunchConfig, log *slog.Logger) (*preparedServerProject, error) {
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("auth schema: %w", err)
	}
	if migrated, err := authRepo.MigratePlaintextTOTP(ctx); err != nil {
		log.Warn("миграция открытых TOTP-seed'ов не выполнена", "err", err)
	} else if migrated > 0 {
		log.Info("TOTP-seed'ы перешифрованы мастер-ключом", "мигрировано", migrated)
	}
	if plaintext, err := authRepo.CountPlaintextTOTP(ctx); err == nil && plaintext > 0 {
		log.Warn("открытые (незашифрованные) TOTP-seed'ы в базе — задайте мастер-ключ",
			"количество", plaintext,
			"выход", "ONEBASE_MASTER_KEY или ONEBASE_MASTER_KEY_FILE, затем перезапуск (план 83)")
	}
	if policy := authRepo.AuthPolicy(ctx); policy.Enabled() {
		if cohort, err := authRepo.TwoFactorLockoutRisk(ctx, policy); err != nil {
			log.Warn("проверка риска блокировки вторым фактором не выполнена", "err", err)
		} else if cohort != "" {
			log.Warn("политика требует второй фактор от когорты, у которой он не привязан ни у кого, при выключенной самопривязке — войти не сможет никто",
				"когорта", cohort,
				"выход", "onebase user 2fa self-enroll on — разрешить привязку второго фактора на входе")
		}
	}

	var proj *project.Project
	var cfgRepo *configdb.Repo
	var loadedVersion string
	var err error
	if cfg.configSource == "database" {
		cfgRepo = configdb.New(db)
		if err := cfgRepo.EnsureSchema(ctx); err != nil {
			return nil, fmt.Errorf("configdb schema: %w", err)
		}
		if err := cfgRepo.MigrateContent(ctx); err != nil {
			return nil, fmt.Errorf("configdb migrate content: %w", err)
		}
		loadedVersion, err = latestConfigVersionID(ctx, cfgRepo)
		if err != nil {
			return nil, fmt.Errorf("read current config version: %w", err)
		}
		proj, err = project.LoadFromDB(ctx, cfgRepo)
	} else {
		proj, err = project.Load(cfg.dir)
	}
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	fail := func(err error) (*preparedServerProject, error) {
		proj.Close()
		return nil, err
	}
	appCfg, err := project.LoadConfig(proj.Dir)
	if err != nil {
		return fail(fmt.Errorf("load app config: %w", err))
	}
	if err := migrateServerSchema(ctx, db, proj); err != nil {
		return fail(err)
	}
	roles, err := auth.LoadRolesYAML(filepath.Join(proj.Dir, "roles"))
	if err != nil {
		return fail(fmt.Errorf("load roles: %w", err))
	}
	if len(roles) > 0 {
		if err := authRepo.SyncRoles(ctx, roles); err != nil {
			return fail(fmt.Errorf("sync roles: %w", err))
		}
	}
	if os.Getenv("ONEBASE_STRICT_RLS") != "" {
		guarded := access.GuardedEntitiesFromRoles(roles)
		db.SetStrictRLSGuard(func(name string) bool { return guarded[name] })
	}
	if err := db.EnsureAccountsTable(ctx); err != nil {
		return fail(fmt.Errorf("accounts table: %w", err))
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		return fail(fmt.Errorf("sync accounts: %w", err))
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return fail(fmt.Errorf("migrate account registers: %w", err))
	}

	reg := registryFromProject(proj)
	extRepo := extform.New(db)
	if err := extRepo.EnsureSchema(ctx); err != nil {
		return fail(fmt.Errorf("extform schema: %w", err))
	}
	if forms, layouts, err := extRepo.LoadEnabledPrintForms(ctx); err != nil {
		log.Warn("external print forms load failed", "err", err)
	} else {
		reg.SetExternalPrintForms(forms)
		reg.SetExternalLayoutForms(layouts)
	}
	reports := extform.NewReports(db)
	if err := reports.EnsureSchema(ctx); err != nil {
		return fail(fmt.Errorf("extform reports schema: %w", err))
	}
	if values, err := reports.LoadEnabledReports(ctx); err != nil {
		log.Warn("external reports load failed", "err", err)
	} else {
		reg.SetExternalReports(values)
	}
	processors := extform.NewProcessors(db)
	if err := processors.EnsureSchema(ctx); err != nil {
		return fail(fmt.Errorf("extform processors schema: %w", err))
	}
	if values, programs, err := processors.LoadEnabled(ctx); err != nil {
		log.Warn("external processors load failed", "err", err)
	} else {
		reg.SetExternalProcessors(values, programs)
	}
	return &preparedServerProject{
		authRepo: authRepo, project: proj, appConfig: appCfg, configRepo: cfgRepo,
		loadedConfigVersionID: loadedVersion, registry: reg,
	}, nil
}
