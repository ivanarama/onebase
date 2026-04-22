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
	"github.com/ivantit66/onebase/internal/devserver"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Start the server in dev mode with hot reload",
	RunE:  runDev,
}

func init() {
	devCmd.Flags().String("project", ".", "path to project directory")
	devCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
}

func runDev(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	dsn := dsnFromFlags(cmd)

	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	reg := runtime.NewRegistry()
	interp := interpreter.New()

	load := func() {
		proj, err := project.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "[dev] project error:", err)
			return
		}
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			fmt.Fprintln(os.Stderr, "[dev] migrate error:", err)
			return
		}
		reg.Load(proj.Entities, proj.Programs)
		fmt.Fprintln(os.Stdout, "[dev] reloaded")
	}
	load()

	if err := devserver.Watch(dir, load); err != nil {
		return fmt.Errorf("watcher: %w", err)
	}

	srv := api.New(reg, db, interp)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "server error:", err)
		}
	}()

	fmt.Fprintln(os.Stdout, "onebase dev running on :8080")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	_ = srv.Shutdown(ctx)
	wg.Wait()
	return nil
}
