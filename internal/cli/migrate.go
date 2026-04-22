package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database schema from project metadata",
	RunE:  runMigrate,
}

func init() {
	migrateCmd.Flags().String("project", ".", "path to project directory")
	migrateCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
}

func runMigrate(cmd *cobra.Command, _ []string) error {
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
		return err
	}
	fmt.Fprintln(os.Stdout, "migration complete")
	return nil
}

func dsnFromFlags(cmd *cobra.Command) string {
	if dsn, _ := cmd.Flags().GetString("db"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://localhost/onebase"
}
