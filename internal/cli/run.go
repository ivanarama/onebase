package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the server in production mode",
	RunE:  runServer,
}

func init() {
	runCmd.Flags().String("project", ".", "path to project directory")
	runCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
}

func runServer(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	dsn := dsnFromFlags(cmd)

	proj, err := project.Load(dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}

	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return fmt.Errorf("migrate registers: %w", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(proj.Entities, proj.Programs, proj.Registers, proj.Reports)

	interp := interpreter.New()
	srv := api.New(reg, db, interp)

	fmt.Fprintln(os.Stdout, "onebase running on :8080")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "server error:", err)
		}
	}()
	<-quit
	return srv.Shutdown(ctx)
}
