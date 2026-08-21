package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/api"
	runtimeapp "github.com/ivantit66/onebase/internal/app"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/jobqueue"
	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/version"
	"github.com/ivantit66/onebase/internal/webhook"
	"github.com/spf13/cobra"
)

type preparedServerRuntime struct {
	application       *runtimeapp.Application
	scheduler         *scheduler.Scheduler
	demoResetRequests <-chan *scheduledDemoResetRequest
}

func prepareServerRuntime(ctx context.Context, cmd *cobra.Command, db *storage.DB, launch serverLaunchConfig, prepared *preparedServerProject, log *slog.Logger) (*preparedServerRuntime, error) {
	appCfg, proj, reg := prepared.appConfig, prepared.project, prepared.registry
	for _, err := range applyAppAISettings(ctx, db, appCfg) {
		log.Warn("apply app ai setting failed", "err", err)
	}
	if err := applyFileStorageS3(db, appCfg); err != nil {
		return nil, err
	}
	uiCfg := ui.Config{
		DSN: launch.dsn, DatabaseType: runtimeDatabaseType(launch.dbType),
		DatabaseLocation: runtimeDatabaseLocation(launch.dbType, launch.dsn, launch.sqlitePath),
		ConfigSource:     launch.configSource,
		ConfigLocation:   runtimeConfigLocation(launch.configSource, launch.dir, launch.dbType, launch.dsn, launch.sqlitePath),
		PlatVersion:      version.String(), PlatCommit: version.Commit(), PlatDate: version.CommitDate(),
		PlatAuthor: version.Author, PlatLicense: version.License,
	}
	if appCfg != nil {
		uiCfg.AppName, uiCfg.AppVersion = appCfg.Name, appCfg.Version
		uiCfg.AppAuthor, uiCfg.AppCopyright = appCfg.Author, appCfg.Copyright
		uiCfg.AppLicense, uiCfg.AppSupport, uiCfg.Lang = appCfg.License, appCfg.Support, appCfg.Lang
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
		log.Warn("i18n load failed", "err", err)
	}
	uiCfg.Bundle = bundle

	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupSiblingProc = reg.GetSiblingProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	interp.StrictLexicalScope = appDSLStrictLexicalScope(appCfg)
	if err := ensureRuntimeSchemas(ctx, db); err != nil {
		return nil, err
	}
	sched := scheduler.New(db, reg, interp)
	if err := sched.LoadJobs(proj.ScheduledJobs); err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	uiCfg.JobQueue = jobqueue.New(db, sched, runtimeQueueConfigFromApp(appCfg))
	demoResetRequests := make(chan *scheduledDemoResetRequest, 1)
	applyDemoFlags(cmd, &appCfg)
	if err := configureDemoRuntime(ctx, db, launch, proj.Dir, appCfg, &uiCfg, sched, demoResetRequests, log); err != nil {
		return nil, err
	}
	if appCfg != nil && appCfg.Backup != nil {
		target := backup.AutoTarget{DBType: launch.dbType, DSN: launch.dsn, SQLitePath: launch.sqlitePath, ProjectDir: launch.dir}
		if err := backup.RegisterAutoBackup(appCfg.Backup, target, sched); err != nil {
			log.Warn("auto backup job registration failed", "err", err)
		}
	}
	if appCfg != nil && appCfg.Email != nil {
		m := mailer.New(mailer.Config{
			SMTPHost: appCfg.Email.SMTPHost, SMTPPort: appCfg.Email.SMTPPort,
			SMTPUser: appCfg.Email.SMTPUser, SMTPPass: appCfg.Email.SMTPPass,
			FromName: appCfg.Email.FromName, FromAddress: appCfg.Email.FromAddress,
		})
		uiCfg.Mailer = m
		sched.SetMailer(m)
	}
	configureWebhooks(ctx, db, appCfg, &uiCfg)

	host, _ := cmd.Flags().GetString("host")
	allowInsecure, _ := cmd.Flags().GetBool("allow-insecure-bootstrap")
	if !api.IsLoopbackHost(host) {
		hasUsers, _ := prepared.authRepo.HasUsers(ctx)
		if refusal := bootstrapRefusal(host, hasUsers, allowInsecure); refusal != "" {
			return nil, errors.New(refusal)
		}
		if !hasUsers {
			fmt.Fprintf(os.Stderr, "ПРЕДУПРЕЖДЕНИЕ (--allow-insecure-bootstrap): сервер слушает %s\n"+
				"без настроенных пользователей — база и консоль кода доступны без\n"+
				"аутентификации. Создайте администратора немедленно.\n", host)
		}
	}
	application, err := runtimeapp.Build(ctx, runtimeapp.Config{
		Registry: reg, Store: db, Interpreter: interp, AuthRepo: prepared.authRepo,
		Host: host, Port: launch.port, UI: uiCfg, Scheduler: sched,
	})
	if err != nil {
		return nil, err
	}
	return &preparedServerRuntime{application: application, scheduler: sched, demoResetRequests: demoResetRequests}, nil
}

func ensureRuntimeSchemas(ctx context.Context, db *storage.DB) error {
	checks := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"scheduled runs schema", db.EnsureScheduledRunsTable},
		{"job queue schema", db.EnsureJobQueueSchema},
		{"attachments table", db.EnsureAttachmentTable},
		{"public files table", db.EnsurePublicFilesSchema},
		{"blobs table", db.EnsureBlobTable},
	}
	for _, check := range checks {
		if err := check.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", check.name, err)
		}
	}
	return nil
}

func applyDemoFlags(cmd *cobra.Command, appCfg **project.AppConfig) {
	backupFlag, _ := cmd.Flags().GetString("demo-backup")
	scheduleFlag, _ := cmd.Flags().GetString("demo-schedule")
	messageFlag, _ := cmd.Flags().GetString("demo-message")
	if backupFlag == "" && scheduleFlag == "" && messageFlag == "" {
		return
	}
	if *appCfg == nil {
		*appCfg = &project.AppConfig{}
	}
	if (*appCfg).Demo == nil {
		(*appCfg).Demo = &project.DemoConfig{}
	}
	(*appCfg).Demo.Enabled = true
	if backupFlag != "" {
		(*appCfg).Demo.ResetBackup = backupFlag
	}
	if scheduleFlag != "" {
		(*appCfg).Demo.ResetSchedule = scheduleFlag
	}
	if messageFlag != "" {
		(*appCfg).Demo.Message = messageFlag
	}
}

func configureDemoRuntime(ctx context.Context, db *storage.DB, launch serverLaunchConfig, projectDir string, appCfg *project.AppConfig, uiCfg *ui.Config, sched *scheduler.Scheduler, requests chan<- *scheduledDemoResetRequest, log *slog.Logger) error {
	if appCfg == nil || appCfg.Demo == nil || !appCfg.Demo.Enabled {
		return nil
	}
	if err := checkDemoEnv(os.Getenv("ONEBASE_ENV")); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "⚠️  ONEBASE: ДЕМО-РЕЖИМ. Данные сбрасываются по расписанию.")
	uiCfg.DemoMode = true
	interpreter.SetFileSandbox(projectDir)
	uiCfg.DemoMessage = appCfg.Demo.Message
	if uiCfg.DemoMessage == "" {
		uiCfg.DemoMessage = "Данные сбрасываются каждую ночь в 02:00"
	}
	schedule := appCfg.Demo.ResetSchedule
	if schedule == "" {
		schedule = "0 2 * * *"
	}
	backupPath := ""
	var err error
	if appCfg.Demo.ResetBackup != "" {
		if filepath.IsAbs(appCfg.Demo.ResetBackup) {
			backupPath = filepath.Clean(appCfg.Demo.ResetBackup)
		} else {
			backupPath, err = filepath.Abs(filepath.Join(launch.dir, appCfg.Demo.ResetBackup))
			if err != nil {
				return fmt.Errorf("resolve demo reset backup path: %w", err)
			}
		}
	}
	var mu sync.Mutex
	requested := false
	if err := sched.RegisterGoJob("DemoReset", "Сброс демо-данных", schedule, func(ctx context.Context) error {
		if backupPath == "" {
			return nil
		}
		runInfo, ok := scheduler.CurrentRun(ctx)
		if !ok {
			return errors.New("scheduled demo reset run identity is unavailable")
		}
		request := &scheduledDemoResetRequest{
			dbType: launch.dbType, dsn: launch.dsn, sqlitePath: launch.sqlitePath,
			filesDir: db.FilesDir(), backupPath: backupPath, run: runInfo,
		}
		mu.Lock()
		defer mu.Unlock()
		if requested {
			return errors.New("scheduled demo reset is already pending")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case requests <- request:
			requested = true
			return scheduler.Accepted("offline demo reset request accepted")
		}
	}); err != nil {
		log.Warn("demo reset job registration failed", "err", err)
	}
	return nil
}

func configureWebhooks(ctx context.Context, db *storage.DB, appCfg *project.AppConfig, uiCfg *ui.Config) {
	if appCfg == nil || len(appCfg.Webhooks) == 0 {
		return
	}
	d := webhook.New(appCfg.Webhooks, func(e webhook.LogEntry) {
		logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		db.LogWebhook(logCtx, storage.WebhookLogEntry{
			Webhook: e.Webhook, Event: e.Event, Entity: e.Entity, RecordID: e.RecordID,
			URL: e.URL, StatusCode: e.StatusCode, Error: e.Error,
			Duration: e.Duration, Attempts: e.Attempts,
		})
	})
	d.SetGuard(func() bool { return db.GetNetworkEnabled(context.Background()) })
	uiCfg.Webhooks = d
	outf("веб-хуки: настроено %d\n", len(appCfg.Webhooks))
	if !db.GetNetworkEnabled(ctx) {
		outln("  ⚠ сеть заблокирована предохранителем — хуки не будут отправляться.\n" + "    " + storage.NetworkEnabledHint)
	}
}
