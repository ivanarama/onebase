package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newMigrateScanCmd()) }

func newMigrateScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate-scan <export-directory>",
		Short: "Измерить BSL и обращения к БСП в XML-выгрузке 1С",
		Long: `Читает каталог XML-выгрузки конфигурации из Конфигуратора 1С.
Состав БСП определяется подсистемой СтандартныеПодсистемы и её дочерними
подсистемами. Код не исполняется и не публикуется. Частоты вызовов — лексические
кандидаты, W040 — сырые совпадения, а не доказательство несовместимости.
UTF-8 и UTF-16 определяются по BOM; без BOM auto выбирает валидный UTF-8,
иначе Windows-1251. Для неоднозначного ввода задайте --encoding явно.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			encoding, err := cmd.Flags().GetString("encoding")
			if err != nil {
				return err
			}
			report, err := onec_forms.ScanExport(args[0], encoding)
			if err != nil {
				return err
			}
			asJSON, err := cmd.Flags().GetBool("json")
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			return writeMigrateScanText(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().Bool("json", false, "детерминированный JSON без исходного кода")
	cmd.Flags().String("encoding", "auto", "auto|utf-8|windows-1251|utf-16le|utf-16be (для BSL и XML)")
	return cmd
}

func writeMigrateScanText(out io.Writer, r *onec_forms.ExportScan) error {
	var text strings.Builder
	w := tabwriter.NewWriter(&text, 0, 4, 2, ' ', 0)
	// Build a complete report before writing, so both formats fail without a
	// partial report if an input file cannot be read or decoded.
	_, _ = fmt.Fprintln(w, "Объём BSL\nКод\tМодулей\tСтрок")
	_, _ = fmt.Fprintf(w, "Всего\t%d\t%d\nБСП\t%d\t%d\nПрикладной\t%d\t%d\n", r.Total.Modules, r.Total.Lines, r.BSP.Modules, r.BSP.Lines, r.Application.Modules, r.Application.Lines)
	_, _ = fmt.Fprintf(w, "Доля строк БСП: %.4f%%\nПодсистем БСП (включая корень): %d\nОбщих модулей БСП: %d; вызываются: %d; не вызываются: %d\n", r.BSPLinePercent, r.BSPSubsystems, r.BSPCommonModules, r.UsedBSPCommonModules, r.UnusedBSPCommonModules)
	_, _ = fmt.Fprintf(w, "Лексических вызовов из прикладного кода: %d; уникальных методов: %d; методов с 1–2 вызовами: %d\n", r.BSPCalls, len(r.Methods), r.RareMethods)
	rows := func(title string, rows []onec_forms.ScanFrequency) {
		_, _ = fmt.Fprintf(w, "\n%s\nИмя\tВхождений\tФайлов\n", title)
		for _, row := range rows {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%d\n", row.Name, row.Count, row.Files)
		}
	}
	rows("Вызовы по модулям", r.Modules)
	rows("Вызовы по методам", r.Methods)
	rows("Имена методов суммарно по модулям БСП", r.MethodNames)
	_, _ = fmt.Fprintln(w, "\nКонцентрация методов\nТоп\tВызовов\tДоля, %")
	for _, row := range r.Concentration {
		_, _ = fmt.Fprintf(w, "%d\t%d\t%.4f\n", row.Top, row.Calls, row.Percent)
	}
	for _, subsystem := range r.Subsystems {
		_, _ = fmt.Fprintf(w, "\n%s: %d общих модулей; %d вызовов; %d методов\n", subsystem.Name, subsystem.Modules, subsystem.Calls, len(subsystem.Methods))
		rows("Точки входа (подсистемы могут пересекаться)", subsystem.Methods)
	}
	_, _ = fmt.Fprintf(w, "\nПрофиль сырых W040: %s; всего вхождений: %d\n", r.RawW040Profile, r.RawW040Occurrences)
	rows("Сырые W040 прикладного кода (все точные вхождения на незакомментированных строках, включая строки и хвостовые комментарии)", r.RawW040)
	_, _ = fmt.Fprintln(w, "\nПодтверждённые несовместимости: не оценивались (реестр проверенных контрактов — отдельный срез плана 179).\nИсторические числа плана автоматически не подтверждаются этим отчётом.")
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(out, text.String())
	return err
}
