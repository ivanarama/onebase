package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/launcher"
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
			fmt.Fprintln(os.Stdout, "No information bases registered.")
			fmt.Fprintln(os.Stdout, "Use 'onebase start' or 'onebase ibases add' to add one.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tSOURCE\tPORT\tDB")
		for _, b := range bases {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", b.ID[:8]+"…", b.Name, b.ConfigSource, b.Port, b.DB)
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
		path, _ := cmd.Flags().GetString("path")
		port, _ := cmd.Flags().GetInt("port")
		src, _ := cmd.Flags().GetString("source")

		if name == "" || db == "" {
			return fmt.Errorf("--name and --db are required")
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
		if err := store.Add(b); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "added: %s (%s)\n", b.Name, b.ID)
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
		fmt.Fprintln(os.Stdout, "removed:", id)
		return nil
	},
}

func init() {
	ibasesAddCmd.Flags().String("name", "", "display name")
	ibasesAddCmd.Flags().String("db", "", "PostgreSQL connection string")
	ibasesAddCmd.Flags().String("path", "", "project directory (for file source)")
	ibasesAddCmd.Flags().Int("port", 8080, "server port")
	ibasesAddCmd.Flags().String("source", "database", "config source: file or database")

	ibasesRemoveCmd.Flags().String("id", "", "base ID (from ibases list)")

	ibasesCmd.AddCommand(ibasesListCmd, ibasesAddCmd, ibasesRemoveCmd)
}
