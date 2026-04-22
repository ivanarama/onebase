package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build project binary (placeholder for MVP)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("build: use 'go build ./cmd/onebase' to compile the platform binary")
		fmt.Println("       embedded project bundles are planned for a future release")
		return nil
	},
}
