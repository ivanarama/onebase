package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/project"
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Scaffold a new onebase project",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	name := "myapp"
	if dir != "." {
		name = dir
	}
	if err := project.Scaffold(dir, name); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "project initialized in %s\n", dir)
	return nil
}
