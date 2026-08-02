package cli

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/converter"
	"github.com/spf13/cobra"
)

var convertCmd = &cobra.Command{
	Use:   "convert",
	Short: "Import a 1C:Enterprise configuration into onebase format",
	Long: `Reads a 1C:Enterprise configuration XML export and creates a onebase project.

To produce the export, use the 1C:Enterprise configurator:
  Configuration → Save configuration to files... → choose a directory

Then run:
  onebase convert --from 1c-xml --dir ./export --out ./my-project`,
	RunE: runConvert,
}

func init() {
	convertCmd.Flags().String("from", "1c-xml", "source format: 1c-xml")
	convertCmd.Flags().String("dir", "", "path to the 1C:Enterprise XML export directory")
	convertCmd.Flags().String("out", "", "output directory for the onebase project")
	mustMarkRequired(convertCmd, "dir")
	mustMarkRequired(convertCmd, "out")
}

func runConvert(cmd *cobra.Command, _ []string) error {
	from, _ := cmd.Flags().GetString("from")
	if from != "1c-xml" {
		return fmt.Errorf("unsupported format: %q (only 1c-xml is supported)", from)
	}

	srcDir, _ := cmd.Flags().GetString("dir")
	outDir, _ := cmd.Flags().GetString("out")

	outf("Конвертация: %s → %s\n", srcDir, outDir)

	report, err := converter.Convert(converter.Options{
		SourceDir: srcDir,
		OutDir:    outDir,
	})
	if err != nil {
		return err
	}

	outln(report.String())
	outf("Отчёт сохранён: %s/conversion_report.txt\n", outDir)
	outf("\nЗапуск сервера:\n  onebase dev --project %s --db \"postgres://localhost/mydb?sslmode=disable\"\n", outDir)
	return nil
}
