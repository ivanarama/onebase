package cli

import (
	"encoding/json"
	"io"

	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newMigrateMatrixCmd()) }

func newMigrateMatrixCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate-matrix [export-directory]",
		Short: "Матрица проверенных сигнатур BSL и ограничений переноса",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var scan *onec_forms.ExportScan
			if len(args) > 0 {
				encoding, err := cmd.Flags().GetString("encoding")
				if err != nil {
					return err
				}
				scan, err = onec_forms.ScanExport(args[0], encoding)
				if err != nil {
					return err
				}
			}
			matrix, err := onec_forms.BuildCompatibilityMatrix(scan)
			if err != nil {
				return err
			}
			asJSON, err := cmd.Flags().GetBool("json")
			if err != nil {
				return err
			}
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(matrix)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), onec_forms.CompatibilityMarkdown(matrix))
			return err
		},
	}
	cmd.Flags().Bool("json", false, "машиночитаемые контракты, примеры и частоты")
	cmd.Flags().String("encoding", "auto", "кодировка выгрузки: auto|utf-8|windows-1251|utf-16le|utf-16be")
	return cmd
}
