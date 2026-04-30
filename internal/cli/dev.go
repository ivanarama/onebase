package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/version"
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

	authRepo := auth.NewRepo(db.Pool())
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("auth schema: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}

	reg := runtime.NewRegistry()
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc

	var watchDir string
	load := func() {
		var proj *project.Project
		var lerr error

		if configSource == "database" {
			cfgRepo := configdb.New(db.Pool())
			if err := cfgRepo.EnsureSchema(ctx); err != nil {
				fmt.Fprintln(os.Stderr, "[dev] configdb error:", err)
				return
			}
			proj, lerr = project.LoadFromDB(ctx, cfgRepo)
		} else {
			proj, lerr = project.Load(dir)
			watchDir = dir
		}
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "[dev] project error:", lerr)
			return
		}
		defer proj.Close()

		if err := db.Migrate(ctx, proj.Entities); err != nil {
			fmt.Fprintln(os.Stderr, "[dev] migrate error:", err)
			return
		}
		if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
			fmt.Fprintln(os.Stderr, "[dev] migrate registers error:", err)
			return
		}
		if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
			fmt.Fprintln(os.Stderr, "[dev] migrate info registers error:", err)
			return
		}
		if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
			fmt.Fprintln(os.Stderr, "[dev] migrate constants error:", err)
			return
		}
		if roles, err2 := auth.LoadRolesYAML(proj.Dir + "/roles"); err2 == nil && len(roles) > 0 {
			_ = authRepo.SyncRoles(ctx, roles)
		}
		reg.Load(proj.Entities, proj.Programs, proj.Registers, proj.InfoRegisters, proj.Enums, proj.Constants, proj.Reports, proj.PrintForms)
		reg.LoadModules(proj.Modules)
		reg.LoadProcessors(proj.Processors)
		fmt.Fprintln(os.Stdout, "[dev] reloaded")
	}
	load()

	if configSource == "file" && watchDir != "" {
		if err := devserver.Watch(watchDir, load); err != nil {
			return fmt.Errorf("watcher: %w", err)
		}
	}

	appCfg, _ := project.LoadConfig(dir)
	uiCfg := ui.Config{DSN: dsn, PlatVersion: version.String()}
	if appCfg != nil {
		uiCfg.AppName = appCfg.Name
		uiCfg.AppVersion = appCfg.Version
	}
	srv := api.New(reg, db, interp, authRepo, port, uiCfg)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "server error:", err)
		}
	}()

	fmt.Fprintf(os.Stdout, "onebase dev running on :%d\n", port)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	_ = srv.Shutdown(ctx)
	wg.Wait()
	return nil
}
