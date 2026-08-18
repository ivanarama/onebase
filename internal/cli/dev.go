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

	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/jobqueue"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/version"
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Start the server in dev mode with hot reload",
	RunE:  runDev,
}

func init() {
	registerDevFlags(devCmd)
}

// registerDevFlags объявляет флаги команды. Отдельной функцией — чтобы тест
// собирал аргументы дочернего процесса на тех же флагах, что и сама команда, а
// не на своей копии, которая могла бы с ней разойтись.
func registerDevFlags(cmd *cobra.Command) {
	cmd.Flags().String("project", ".", "path to project directory")
	cmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	cmd.Flags().String("sqlite", "", "путь к файлу базы SQLite (вместо --db)")
	cmd.Flags().Int("port", 8080, "HTTP server port")
	cmd.Flags().String("config-source", "file", "configuration source: file or database")
	cmd.Flags().Bool("open", false, "открыть базу в браузере, когда сервер будет готов")
	cmd.Flags().Bool("reload-binary", false, "пересобирать платформу и перезапускать сервер при изменении Go-кода (для разработки самой платформы)")
	cmd.Flags().String("source", ".", "каталог дерева исходников платформы для --reload-binary (ищется go.mod вверх по дереву)")
}

func runDev(cmd *cobra.Command, _ []string) error {
	if cmd.Flags().Changed("db") && cmd.Flags().Changed("sqlite") {
		return errors.New("--db and --sqlite are mutually exclusive")
	}
	// --reload-binary уводит в супервизор: сервер поднимает не этот процесс, а
	// пересобранный им дочерний (см. dev_reload.go).
	if reloadBinary, _ := cmd.Flags().GetBool("reload-binary"); reloadBinary {
		return runDevSupervisor(cmd)
	}

	devLog := oblog.Component("cli.dev")
	dir, _ := cmd.Flags().GetString("project")
	dsn := dsnFromFlags(cmd)
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	port, _ := cmd.Flags().GetInt("port")
	configSource, _ := cmd.Flags().GetString("config-source")
	dbType := "postgres"
	if sqlitePath != "" {
		dbType = "sqlite"
	}

	ctx := context.Background()
	db, err := openCLIStorage(ctx, dbType, sqlitePath, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("auth schema: %w", err)
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

	reg := runtime.NewRegistry()
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc

	sched := scheduler.New(db, reg, interp)

	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		return fmt.Errorf("scheduled runs schema: %w", err)
	}
	if err := db.EnsureJobQueueSchema(ctx); err != nil {
		return fmt.Errorf("job queue schema: %w", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return fmt.Errorf("attachments table: %w", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return fmt.Errorf("blobs table: %w", err)
	}

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
		loadedConfigVersionID, err = latestConfigVersionID(ctx, cfgRepo)
		if err != nil {
			return fmt.Errorf("read current config version: %w", err)
		}
	}

	var appCfg *project.AppConfig
	var appBundle *i18n.Bundle
	var srv *api.Server
	load := func(loadCtx context.Context, initial bool) error {
		var proj *project.Project
		var lerr error

		if configSource == "database" {
			proj, lerr = project.LoadFromDB(loadCtx, cfgRepo)
		} else {
			proj, lerr = project.Load(dir)
		}
		if lerr != nil {
			devLog.Warn("project load failed", "err", lerr)
			return fmt.Errorf("load project: %w", lerr)
		}
		defer proj.Close()
		nextAppCfg, err := project.LoadConfig(proj.Dir)
		if err != nil {
			devLog.Warn("app config load failed", "err", err)
			return fmt.Errorf("load app config: %w", err)
		}
		if err := sched.ValidateProjectJobs(proj.ScheduledJobs); err != nil {
			devLog.Warn("scheduled jobs validation failed", "err", err)
			return fmt.Errorf("validate scheduled jobs: %w", err)
		}

		if err := db.Migrate(loadCtx, proj.Entities); err != nil {
			devLog.Warn("migrate failed", "err", err)
			return fmt.Errorf("migrate: %w", err)
		}
		if err := db.MigrateRegisters(loadCtx, proj.Registers); err != nil {
			devLog.Warn("migrate registers failed", "err", err)
			return fmt.Errorf("migrate registers: %w", err)
		}
		if err := db.MigrateInfoRegisters(loadCtx, proj.InfoRegisters); err != nil {
			devLog.Warn("migrate info registers failed", "err", err)
			return fmt.Errorf("migrate info registers: %w", err)
		}
		if err := db.MigrateConstants(loadCtx, proj.Constants); err != nil {
			devLog.Warn("migrate constants failed", "err", err)
			return fmt.Errorf("migrate constants: %w", err)
		}
		if err := db.EnsureAccountsTable(loadCtx); err != nil {
			devLog.Warn("accounts table ensure failed", "err", err)
			return fmt.Errorf("accounts table: %w", err)
		}
		if err := db.SyncAccounts(loadCtx, proj.ChartsOfAccounts); err != nil {
			devLog.Warn("sync accounts failed", "err", err)
			return fmt.Errorf("sync accounts: %w", err)
		}
		if err := db.MigrateAccountRegisters(loadCtx, proj.AccountRegisters); err != nil {
			devLog.Warn("migrate account registers failed", "err", err)
			return fmt.Errorf("migrate account registers: %w", err)
		}
		roles, err := auth.LoadRolesYAML(filepath.Join(proj.Dir, "roles"))
		if err != nil {
			devLog.Warn("roles load failed", "err", err)
			return fmt.Errorf("load roles: %w", err)
		}
		if len(roles) > 0 {
			if err := authRepo.SyncRoles(loadCtx, roles); err != nil {
				devLog.Warn("roles sync failed", "err", err)
				return fmt.Errorf("sync roles: %w", err)
			}
		}
		if err := reloadProjectRuntime(reg, sched, srv, proj); err != nil {
			devLog.Warn("runtime reload failed", "err", err)
			return err
		}

		// Внешний контур: печатные формы и отчёты из БД (вне конфигурации проекта).
		extRepo := extform.New(db)
		if err := extRepo.EnsureSchema(loadCtx); err != nil {
			devLog.Warn("extform schema failed", "err", err)
		} else if extForms, extLayouts, err := extRepo.LoadEnabledPrintForms(loadCtx); err != nil {
			devLog.Warn("external print forms load failed", "err", err)
		} else {
			reg.SetExternalPrintForms(extForms)
			reg.SetExternalLayoutForms(extLayouts)
		}
		extRepRepo := extform.NewReports(db)
		if err := extRepRepo.EnsureSchema(loadCtx); err != nil {
			devLog.Warn("extform reports schema failed", "err", err)
		} else if extReps, err := extRepRepo.LoadEnabledReports(loadCtx); err != nil {
			devLog.Warn("external reports load failed", "err", err)
		} else {
			reg.SetExternalReports(extReps)
		}
		extProcRepo := extform.NewProcessors(db)
		if err := extProcRepo.EnsureSchema(loadCtx); err != nil {
			devLog.Warn("extform processors schema failed", "err", err)
		} else if extProcs, extPrograms, err := extProcRepo.LoadEnabled(loadCtx); err != nil {
			devLog.Warn("external processors load failed", "err", err)
		} else {
			reg.SetExternalProcessors(extProcs, extPrograms)
		}
		if initial {
			appCfg = nextAppCfg
			bundle, err := i18n.Load(i18n.EmbeddedLocales, filepath.Join(proj.Dir, "locales"))
			if err != nil {
				devLog.Warn("i18n load failed", "err", err)
			}
			appBundle = bundle
		}
		if initial && nextAppCfg.Backup != nil {
			target, targetErr := devAutoBackupTarget(db, dbType, dsn, dir)
			if targetErr != nil {
				return targetErr
			}
			if err := backup.RegisterAutoBackup(nextAppCfg.Backup, target, sched); err != nil {
				devLog.Warn("auto backup job registration failed", "err", err)
			}
		}
		if initial {
			outln("[dev] loaded")
		} else {
			outln("[dev] metadata/DSL/scheduled reloaded; app.yaml runtime settings require restart")
			// Страница в браузере перечитывает себя сама: правка формы или
			// модуля видна без F5 (browser sync, только в dev-режиме).
			srv.PublishDevReload()
		}
		return nil
	}
	if err := load(ctx, true); err != nil {
		return err
	}
	if appCfg == nil {
		return errors.New("initial project load did not complete")
	}
	interp.StrictLexicalScope = appDSLStrictLexicalScope(appCfg)

	// file_storage.s3 (план 110, этап 2): S3-бэкенд image-блобов для dev-сервера.
	if err := applyFileStorageS3(db, appCfg); err != nil {
		return err
	}

	uiCfg := ui.Config{
		DSN:              dsn,
		DatabaseType:     dbType,
		DatabaseLocation: runtimeDatabaseLocation(dbType, dsn, sqlitePath),
		ConfigSource:     configSource,
		ConfigLocation:   runtimeConfigLocation(configSource, dir, dbType, dsn, sqlitePath),
		PlatVersion:      version.String(),
		PlatCommit:       version.Commit(),
		PlatDate:         version.CommitDate(),
		PlatAuthor:       version.Author,
		PlatLicense:      version.License,
		// Dev-режим: браузер получает метку запуска процесса и сам обновляет
		// страницу после перезагрузки конфигурации или перезапуска сервера.
		Dev: true,
	}
	if appCfg != nil {
		uiCfg.AppName = appCfg.Name
		uiCfg.AppVersion = appCfg.Version
		uiCfg.AppAuthor = appCfg.Author
		uiCfg.AppCopyright = appCfg.Copyright
		uiCfg.AppLicense = appCfg.License
		uiCfg.AppSupport = appCfg.Support
		uiCfg.Lang = appCfg.Lang
		if appCfg.Attachments != nil {
			if appCfg.Attachments.MaxFileSizeMB > 0 {
				uiCfg.MaxFileSizeMB = appCfg.Attachments.MaxFileSizeMB
			}
			uiCfg.AllowedTypes = appCfg.Attachments.AllowedTypes
		}
		uiCfg.Limits = runtimeLimitsFromApp(appCfg.Limits)
		if appCfg.Email != nil {
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
	}
	uiCfg.Bundle = appBundle
	// Очередь фоновых заданий (план 130) — и в dev-режиме тоже: прикладной код,
	// который ставит задачи, должен работать там же, где его пишут.
	queue := jobqueue.New(db, sched, runtimeQueueConfigFromApp(appCfg))
	uiCfg.JobQueue = queue
	// dev-сервер — всегда loopback (план 53: secure-by-default bind)
	srv = api.New(reg, db, interp, authRepo, "127.0.0.1", port, uiCfg, sched)

	var stopWatch func()
	switch configSource {
	case "file":
		watchCtx, watchCancel := context.WithCancel(ctx)
		watchDone, watchErr := devserver.WatchProjectContext(watchCtx, dir, func() {
			if err := load(watchCtx, false); err != nil {
				devLog.Warn("hot reload failed", "err", err)
			}
		})
		if watchErr != nil {
			watchCancel()
			return fmt.Errorf("watcher: %w", watchErr)
		}
		var stopOnce sync.Once
		stopWatch = func() {
			stopOnce.Do(func() {
				watchCancel()
				<-watchDone
			})
		}
	case "database":
		watchCtx, watchCancel := context.WithCancel(ctx)
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			watchConfigVersions(watchCtx, cfgRepo, loadedConfigVersionID, configReloadInterval, func() error {
				if err := load(watchCtx, false); err != nil {
					devLog.Warn("database hot reload failed", "err", err)
					return err
				}
				return nil
			})
		}()
		var stopOnce sync.Once
		stopWatch = func() {
			stopOnce.Do(func() {
				watchCancel()
				<-watchDone
			})
		}
	}
	defer func() {
		if stopWatch != nil {
			stopWatch()
		}
	}()

	listener, err := srv.Listen()
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:%d: %w", port, err)
	}
	defer func() { _ = listener.Close() }()

	schedCtx, schedCancel := context.WithCancel(ctx)
	defer schedCancel()
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		sched.Start(schedCtx)
	}()
	queueCtx, queueCancel := context.WithCancel(ctx)
	defer queueCancel()
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		if err := queue.Run(queueCtx); err != nil {
			devLog.Warn("job queue drain", "err", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		listenErr := srv.Serve(listener)
		if errors.Is(listenErr, http.ErrServerClosed) {
			listenErr = nil
		} else if listenErr != nil {
			devLog.Error("server failed", "err", listenErr)
		}
		serveErr <- listenErr
	}()

	// WS-шлюзы приёмки (план 120A): hot-reload делает resync через
	// reloadProjectRuntime, стартовый вызов — здесь.
	srv.ResyncWSIntakes()

	outf("onebase dev running on :%d\n", port)
	if openBrowser, _ := cmd.Flags().GetBool("open"); openBrowser {
		openInBrowser("127.0.0.1", port)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	serveResultReceived := false
	var listenErr error
	select {
	case <-quit:
	case <-srv.Done():
	case listenErr = <-serveErr:
		serveResultReceived = true
	}
	if stopWatch != nil {
		stopWatch()
		stopWatch = nil
	}
	schedCancel()
	queueCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if !serveResultReceived {
		listenErr = <-serveErr
	}
	<-schedDone
	<-queueDone
	return errors.Join(listenErr, shutdownErr)
}

func devAutoBackupTarget(db *storage.DB, dbType, dsn, projectDir string) (backup.AutoTarget, error) {
	target := backup.AutoTarget{DBType: dbType, DSN: dsn, ProjectDir: projectDir}
	if dbType != "sqlite" {
		return target, nil
	}
	if db == nil || !db.IsSQLite() {
		return backup.AutoTarget{}, errors.New("automatic backup: SQLite database is not open")
	}
	// Use the exact canonical file that the lifetime lock and open handle refer
	// to. Re-resolving the CLI spelling could follow a retargeted symlink.
	target.SQLitePath = db.SQLitePath()
	if target.SQLitePath == "" {
		return backup.AutoTarget{}, errors.New("automatic backup requires file-backed SQLite; in-memory databases cannot be backed up")
	}
	return target, nil
}
