package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/configdb"
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
	migrateCmd.Flags().String("config-source", "file", "configuration source: file or database")
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	dsn := dsnFromFlags(cmd)
	configSource, _ := cmd.Flags().GetString("config-source")

	ctx := context.Background()
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	var proj *project.Project
	if configSource == "database" {
		cfgRepo := configdb.New(db.Pool())
		if err := cfgRepo.EnsureSchema(ctx); err != nil {
			return fmt.Errorf("configdb schema: %w", err)
		}
		proj, err = project.LoadFromDB(ctx, cfgRepo)
	} else {
		proj, err = project.Load(dir)
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return err
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
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
