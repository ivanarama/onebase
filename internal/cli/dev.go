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
	devCmd.Flags().String("project", ".", "path to project directory")
	devCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	devCmd.Flags().Int("port", 8080, "HTTP server port")
	devCmd.Flags().String("config-source", "file", "configuration source: file or database")
}

func runDev(cmd *cobra.Command, _ []string) error {
	devLog := oblog.Component("cli.dev")
	dir, _ := cmd.Flags().GetString("project")
	dsn := dsnFromFlags(cmd)
	port, _ := cmd.Flags().GetInt("port")
	configSource, _ := cmd.Flags().GetString("config-source")

	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
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
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		return fmt.Errorf("exchange schema: %w", err)
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
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return fmt.Errorf("attachments table: %w", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return fmt.Errorf("blobs table: %w", err)
	}

	var watchDir string
	var appCfg *project.AppConfig
	var srv *api.Server
	var loadErr error
	load := func() {
		loadErr = nil
		var proj *project.Project
		var lerr error

		if configSource == "database" {
			cfgRepo := configdb.New(db)
			if err := cfgRepo.EnsureSchema(ctx); err != nil {
				loadErr = fmt.Errorf("configdb schema: %w", err)
				devLog.Warn("configdb schema failed", "err", err)
				return
			}
			proj, lerr = project.LoadFromDB(ctx, cfgRepo)
		} else {
			proj, lerr = project.Load(dir)
			watchDir = dir
		}
		if lerr != nil {
			loadErr = fmt.Errorf("load project: %w", lerr)
			devLog.Warn("project load failed", "err", lerr)
			return
		}
		defer proj.Close()
		nextAppCfg, err := project.LoadConfig(proj.Dir)
		if err != nil {
			loadErr = fmt.Errorf("load app config: %w", err)
			devLog.Warn("app config load failed", "err", err)
			return
		}
		appCfg = nextAppCfg

		if err := db.Migrate(ctx, proj.Entities); err != nil {
			loadErr = fmt.Errorf("migrate: %w", err)
			devLog.Warn("migrate failed", "err", err)
			return
		}
		if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
			loadErr = fmt.Errorf("migrate registers: %w", err)
			devLog.Warn("migrate registers failed", "err", err)
			return
		}
		if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
			loadErr = fmt.Errorf("migrate info registers: %w", err)
			devLog.Warn("migrate info registers failed", "err", err)
			return
		}
		if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
			loadErr = fmt.Errorf("migrate constants: %w", err)
			devLog.Warn("migrate constants failed", "err", err)
			return
		}
		if err := db.EnsureAccountsTable(ctx); err != nil {
			loadErr = fmt.Errorf("accounts table: %w", err)
			devLog.Warn("accounts table ensure failed", "err", err)
			return
		}
		if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
			loadErr = fmt.Errorf("sync accounts: %w", err)
			devLog.Warn("sync accounts failed", "err", err)
			return
		}
		if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
			loadErr = fmt.Errorf("migrate account registers: %w", err)
			devLog.Warn("migrate account registers failed", "err", err)
			return
		}
		roles, err := auth.LoadRolesYAML(filepath.Join(proj.Dir, "roles"))
		if err != nil {
			loadErr = fmt.Errorf("load roles: %w", err)
			devLog.Warn("roles load failed", "err", err)
			return
		}
		if len(roles) > 0 {
			if err := authRepo.SyncRoles(ctx, roles); err != nil {
				loadErr = fmt.Errorf("sync roles: %w", err)
				devLog.Warn("roles sync failed", "err", err)
				return
			}
		}
		reg.Load(runtime.LoadOptions{
			Entities:        proj.Entities,
			Programs:        proj.Programs,
			ManagerPrograms: proj.ManagerPrograms,
			ServicePrograms: proj.ServicePrograms,
			PagePrograms:    proj.PagePrograms,
			Registers:       proj.Registers,
			InfoRegs:        proj.InfoRegisters,
			Enums:           proj.Enums,
			Constants:       proj.Constants,
			Reports:         proj.Reports,
			PrintForms:      proj.PrintForms,
		})
		reg.LoadDSLPrintForms(proj.DSLPrintForms)
		reg.LoadLayoutForms(proj.LayoutForms)
		reg.LoadModules(proj.Modules)
		reg.LoadProcessors(proj.Processors)
		reg.LoadHTTPServices(proj.HTTPServices)
		reg.LoadPages(proj.Pages)
		reg.LoadExchangePlans(proj.ExchangePlans)
		reg.LoadSubsystems(proj.Subsystems)
		reg.LoadJournals(proj.Journals)
		reg.LoadAccountRegisters(proj.AccountRegisters, proj.ChartsOfAccounts)
		reg.LoadWidgets(proj.Widgets)
		reg.LoadHomePage(proj.HomePage)

		// Внешний контур: печатные формы и отчёты из БД (вне конфигурации проекта).
		extRepo := extform.New(db)
		if err := extRepo.EnsureSchema(ctx); err != nil {
			devLog.Warn("extform schema failed", "err", err)
		} else if extForms, extLayouts, err := extRepo.LoadEnabledPrintForms(ctx); err != nil {
			devLog.Warn("external print forms load failed", "err", err)
		} else {
			reg.SetExternalPrintForms(extForms)
			reg.SetExternalLayoutForms(extLayouts)
		}
		extRepRepo := extform.NewReports(db)
		if err := extRepRepo.EnsureSchema(ctx); err != nil {
			devLog.Warn("extform reports schema failed", "err", err)
		} else if extReps, err := extRepRepo.LoadEnabledReports(ctx); err != nil {
			devLog.Warn("external reports load failed", "err", err)
		} else {
			reg.SetExternalReports(extReps)
		}
		extProcRepo := extform.NewProcessors(db)
		if err := extProcRepo.EnsureSchema(ctx); err != nil {
			devLog.Warn("extform processors schema failed", "err", err)
		} else if extProcs, extPrograms, err := extProcRepo.LoadEnabled(ctx); err != nil {
			devLog.Warn("external processors load failed", "err", err)
		} else {
			reg.SetExternalProcessors(extProcs, extPrograms)
		}
		if schedErr := sched.Reload(proj.ScheduledJobs); schedErr != nil {
			loadErr = fmt.Errorf("scheduler reload: %w", schedErr)
			devLog.Warn("scheduler reload failed", "err", schedErr)
			return
		}
		if appCfg != nil && appCfg.Backup != nil {
			if err := backup.RegisterAutoBackup(appCfg.Backup, backup.AutoTarget{
				DSN:        dsn,
				ProjectDir: dir,
			}, sched); err != nil {
				devLog.Warn("auto backup job registration failed", "err", err)
			}
		}
		if srv != nil {
			srv.InvalidateWidgetCache()
		}
		fmt.Fprintln(os.Stdout, "[dev] reloaded")
	}
	load()
	if loadErr != nil {
		return loadErr
	}
	if appCfg == nil {
		return errors.New("initial project load did not complete")
	}
	interp.StrictLexicalScope = appDSLStrictLexicalScope(appCfg)

	if configSource == "file" && watchDir != "" {
		if err := devserver.Watch(watchDir, load); err != nil {
			return fmt.Errorf("watcher: %w", err)
		}
	}

	uiCfg := ui.Config{
		DSN:              dsn,
		DatabaseType:     "postgres",
		DatabaseLocation: runtimeDatabaseLocation("postgres", dsn, ""),
		ConfigSource:     configSource,
		ConfigLocation:   runtimeConfigLocation(configSource, dir, "postgres", dsn, ""),
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
	bundle, err2 := i18n.Load(i18n.EmbeddedLocales, filepath.Join(dir, "locales"))
	if err2 != nil {
		devLog.Warn("i18n load failed", "err", err2)
	}
	uiCfg.Bundle = bundle
	// dev-сервер — всегда loopback (план 53: secure-by-default bind)
	srv = api.New(reg, db, interp, authRepo, "127.0.0.1", port, uiCfg, sched)

	schedCtx, schedCancel := context.WithCancel(ctx)
	defer schedCancel()
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		sched.Start(schedCtx)
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			devLog.Error("server failed", "err", err)
		}
	}()

	fmt.Fprintf(os.Stdout, "onebase dev running on :%d\n", port)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	schedCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
	<-schedDone
	return nil
}
