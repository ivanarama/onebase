package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// onebase renumber — дозаполнение пустых кодов и номеров (план 117C).
//
// Когда нумератор включают на живой базе, старые элементы остаются без кода:
// автоматически проставлять его при правке YAML нельзя — правка конфигурации не
// должна незаметно переписывать данные, а на большом справочнике это ещё и
// долгая операция. Отсюда отдельная команда: администратор решает сам и видит
// объём заранее.
//
// По умолчанию команда НИЧЕГО не пишет — печатает, что сделала бы. Запись
// включается флагом --write, как у refactor.

var renumberCmd = &cobra.Command{
	Use:   "renumber",
	Short: "Дозаполнить пустые коды справочников и номера документов",
	Long: `Проставляет код (справочник) или номер (документ) тем записям, у которых он пуст.

Заполненные значения не трогаются никогда: команда дозаполняет, а не
перенумеровывает. Порядок выдачи детерминирован (по идентификатору записи),
чтобы повторный запуск дал тот же результат.

По умолчанию ничего не пишет — показывает объём и примеры. Запись включается
флагом --write.

Примеры:
  onebase renumber --project . --sqlite base.db
  onebase renumber --project . --sqlite base.db --object Контрагенты --write`,
	RunE:          runRenumber,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// addBaseFlags уже даёт --project/--sqlite/--db/--id.
	addBaseFlags(renumberCmd)
	renumberCmd.Flags().String("object", "", "имя объекта; без флага — все с объявленным нумератором")
	renumberCmd.Flags().Bool("write", false, "выполнить запись (без флага только показ)")
	renumberCmd.Flags().Bool("json", false, "машинный отчёт")
	rootCmd.AddCommand(renumberCmd)
}

// renumberEntityReport — итог по одному объекту.
type renumberEntityReport struct {
	Object  string   `json:"object"`
	Field   string   `json:"field"`
	Empty   int      `json:"empty"`
	Filled  int      `json:"filled"`
	Samples []string `json:"samples,omitempty"`
}

func runRenumber(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	only, _ := cmd.Flags().GetString("object")
	write, _ := cmd.Flags().GetBool("write")
	asJSON, _ := cmd.Flags().GetBool("json")

	proj, err := project.Load(dir)
	if err != nil {
		return err
	}
	defer proj.Close()

	var db *storage.DB
	if sqlitePath != "" {
		db, err = openCLIStorage(ctx, "sqlite", sqlitePath, "")
	} else {
		db, err = openCLIStorage(ctx, "postgres", "", dsnFromFlags(cmd))
	}
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		return err
	}

	targets, err := renumberTargets(proj, only)
	if err != nil {
		return err
	}

	reports := make([]renumberEntityReport, 0, len(targets))
	for _, ent := range targets {
		rep, err := renumberEntity(ctx, db, ent, write)
		if err != nil {
			return fmt.Errorf("%s: %w", ent.Name, err)
		}
		reports = append(reports, rep)
	}

	if asJSON {
		out, err := json.MarshalIndent(map[string]any{"write": write, "objects": reports}, "", "  ")
		if err != nil {
			return err
		}
		outf("%s\n", out)
		return nil
	}
	printRenumberReport(reports, write)
	return nil
}

// renumberTargets отбирает объекты с объявленным нумератором.
func renumberTargets(proj *project.Project, only string) ([]*metadata.Entity, error) {
	var out []*metadata.Entity
	for _, ent := range proj.Entities {
		if storage.AutoNumberField(ent) == "" || ent.Numerator == nil {
			continue
		}
		if only != "" && !strings.EqualFold(ent.Name, only) {
			continue
		}
		out = append(out, ent)
	}
	if only != "" && len(out) == 0 {
		return nil, fmt.Errorf("объект %q не найден или у него не объявлен нумератор", only)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// renumberEntity дозаполняет пустые значения одного объекта.
func renumberEntity(ctx context.Context, db *storage.DB, ent *metadata.Entity, write bool) (renumberEntityReport, error) {
	field := storage.AutoNumberField(ent)
	rep := renumberEntityReport{Object: ent.Name, Field: field}

	rows, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: storage.MaxListPageSize})
	if err != nil {
		return rep, err
	}
	// Порядок выдачи детерминирован: сортируем по идентификатору записи, а не
	// полагаемся на порядок выборки. Иначе повторный запуск на другой машине
	// раздал бы те же коды другим элементам.
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprintf("%v", rows[i]["id"]) < fmt.Sprintf("%v", rows[j]["id"])
	})

	for _, row := range rows {
		if !isBlankValue(rowFieldValue(row, field)) {
			continue // заполненное не трогаем: дозаполнение, а не перенумерация
		}
		rep.Empty++
		if !write {
			if len(rep.Samples) < 3 {
				rep.Samples = append(rep.Samples, fmt.Sprintf("%v", row["id"]))
			}
			continue
		}
		value, err := db.GenerateNumber(ctx, ent, row)
		if err != nil {
			return rep, err
		}
		if value == "" {
			continue
		}
		id, err := uuid.Parse(fmt.Sprintf("%v", row["id"]))
		if err != nil {
			return rep, fmt.Errorf("неразбираемый идентификатор %v: %w", row["id"], err)
		}
		if err := db.SetAutoNumberValue(ctx, ent, id, field, value); err != nil {
			return rep, err
		}
		rep.Filled++
		if len(rep.Samples) < 3 {
			rep.Samples = append(rep.Samples, value)
		}
	}
	return rep, nil
}

// isBlankValue — пусто ли значение колонки. Отдельная функция потому, что
// fmt.Sprintf("%v", nil) даёт "<nil>", и наивная проверка на пустую строку
// считала незаполненный код заполненным: команда молча ничего не делала.
func isBlankValue(v any) bool {
	if v == nil {
		return true
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v)) == ""
}

func rowFieldValue(row map[string]any, field string) any {
	if v, ok := row[field]; ok {
		return v
	}
	low := strings.ToLower(field)
	for k, v := range row {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

func printRenumberReport(reports []renumberEntityReport, write bool) {
	if len(reports) == 0 {
		outf("Объектов с объявленным numerator: не найдено.\n")
		return
	}
	total := 0
	for _, r := range reports {
		if write {
			outf("%s.%s: заполнено %d\n", r.Object, r.Field, r.Filled)
			total += r.Filled
		} else {
			outf("%s.%s: без значения %d\n", r.Object, r.Field, r.Empty)
			total += r.Empty
		}
		if len(r.Samples) > 0 {
			outf("  примеры: %s\n", strings.Join(r.Samples, ", "))
		}
	}
	if write {
		outf("\nИтого заполнено: %d\n", total)
		return
	}
	outf("\nИтого без значения: %d. Ничего не изменено — повторите с --write.\n", total)
}
