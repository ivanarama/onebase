package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/ivantit66/onebase/internal/converter"
)

var convertCmd = &cobra.Command{
	Use:   "convert",
	Short: "Convert a 1C configuration to onebase format",
	Long: `Reads a 1C configuration XML export and creates a onebase project.

How to export from 1C Configurator:
  Configuration → Save configuration to files... → choose a directory

Then run:
  onebase convert --from 1c-xml --dir ./1c-export --out ./my-project`,
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().String("from", "1c-xml", "source format: 1c-xml")
	convertCmd.Flags().String("dir", "", "path to the 1C XML export directory")
	convertCmd.Flags().String("out", "", "output directory for the onebase project")
	convertCmd.MarkFlagRequired("dir")
	convertCmd.MarkFlagRequired("out")
}

func runConvert(cmd *cobra.Command, _ []string) error {
	from, _ := cmd.Flags().GetString("from")
	if from != "1c-xml" {
		return fmt.Errorf("unsupported format: %q (only 1c-xml is supported)", from)
	}

	srcDir, _ := cmd.Flags().GetString("dir")
	outDir, _ := cmd.Flags().GetString("out")

	fmt.Fprintf(os.Stdout, "Конвертация: %s → %s\n", srcDir, outDir)

	report, err := converter.Convert(converter.Options{
		SourceDir: srcDir,
		OutDir:    outDir,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, report.String())
	fmt.Fprintf(os.Stdout, "Отчёт сохранён: %s/conversion_report.txt\n", outDir)
	fmt.Fprintf(os.Stdout, "\nЗапуск сервера:\n  onebase dev --project %s --db \"postgres://localhost/mydb?sslmode=disable\"\n", outDir)
	return nil
}
