package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/spf13/cobra"
)

var ibasesCmd = &cobra.Command{
	Use:   "ibases",
	Short: "Manage registered information bases",
}

var ibasesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered information bases",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := launcher.NewStore()
		if err != nil {
			return err
		}
		bases, err := store.List()
		if err != nil {
			return err
		}
		if len(bases) == 0 {
			outln("No information bases registered.")
			outln("Use 'onebase start' or 'onebase ibases add' to add one.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		// tabwriter копит строки в буфере и отдаёт ошибку записи из Flush,
		// который проверяется ниже, — поэтому здесь ошибки нет по построению.
		tabln(tw, "ID\tNAME\tSOURCE\tPORT\tDB")
		for _, b := range bases {
			dbLabel := b.DB
			if b.DBType == "sqlite" {
				dbLabel = b.DBPath
			}
			tabf(tw, "%s\t%s\t%s\t%d\t%s\n", b.ID[:8]+"…", b.Name, b.ConfigSource, b.Port, dbLabel)
		}
		return tw.Flush()
	},
}

var ibasesAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Register an information base",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		db, _ := cmd.Flags().GetString("db")
		sqlitePath, _ := cmd.Flags().GetString("sqlite")
		path, _ := cmd.Flags().GetString("path")
		port, _ := cmd.Flags().GetInt("port")
		src, _ := cmd.Flags().GetString("source")

		if name == "" {
			return fmt.Errorf("--name is required")
		}
		switch {
		case db == "" && sqlitePath == "":
			return fmt.Errorf("укажите --db (PostgreSQL) или --sqlite (файл SQLite)")
		case db != "" && sqlitePath != "":
			return fmt.Errorf("--db и --sqlite взаимоисключающи; укажите только один")
		}
		store, err := launcher.NewStore()
		if err != nil {
			return err
		}
		b := &launcher.Base{
			Name:         name,
			DB:           db,
			Path:         path,
			Port:         port,
			ConfigSource: src,
		}
		if sqlitePath != "" {
			b.DBType = "sqlite"
			b.DBPath = sqlitePath
			b.DB = ""
		}
		warnMappedNetworkPath("файл SQLite", b.DBPath, detectMappedNetworkDrive)
		warnMappedNetworkPath("каталог проекта", b.Path, detectMappedNetworkDrive)
		if err := store.Add(b); err != nil {
			return err
		}
		outf("added: %s (%s)\n", b.Name, b.ID)
		return nil
	},
}

var ibasesRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a registered information base",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetString("id")
		if id == "" {
			return fmt.Errorf("--id is required")
		}
		store, err := launcher.NewStore()
		if err != nil {
			return err
		}
		if err := store.Remove(id); err != nil {
			return err
		}
		outln("removed:", id)
		return nil
	},
}

func init() {
	ibasesAddCmd.Flags().String("name", "", "display name")
	ibasesAddCmd.Flags().String("db", "", "PostgreSQL connection string")
	ibasesAddCmd.Flags().String("sqlite", "", "SQLite database file path")
	ibasesAddCmd.Flags().String("path", "", "project directory (for file source)")
	ibasesAddCmd.Flags().Int("port", 8080, "server port")
	ibasesAddCmd.Flags().String("source", "database", "config source: file or database")

	ibasesRemoveCmd.Flags().String("id", "", "base ID (from ibases list)")

	ibasesCmd.AddCommand(ibasesListCmd, ibasesAddCmd, ibasesRemoveCmd)
}
