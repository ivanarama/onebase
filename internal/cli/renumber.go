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
	// Error — почему объект пропущен. Пустая строка = объект обработан.
	// Причина едет в отчёте, а не в коде возврата: непрочитанный объект не
	// отменяет работу по остальным (см. runRenumber).
	Error string `json:"error,omitempty"`
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

	// Сбой на одном объекте не отменяет остальные. Команду зовут ровно тогда,
	// когда схема базы РАСХОДИТСЯ с конфигурацией: гейт уникальности остановил
	// миграцию на первом же объекте, и всё, что шло после него, осталось без
	// новых колонок. Чтение такого объекта падает («no such column»), и раньше
	// это падение уносило весь отчёт — вместе с тем объектом, из-за которого
	// база и не стартовала. Пользователь лаунчера при этом терял кнопку
	// «Дозаполнить коды»: она рисуется по отчёту, которого не было (#1067).
	reports := make([]renumberEntityReport, 0, len(targets))
	failed := 0
	var firstErr error
	for _, ent := range targets {
		rep, err := renumberEntity(ctx, db, ent, write)
		if err != nil {
			rep.Error = err.Error()
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", ent.Name, err)
			}
		}
		reports = append(reports, rep)
	}
	// Ни одного прочитанного объекта — это уже не «часть схемы отстала», а
	// нерабочая база: отчёт всё равно пуст, и молчать о причине нельзя.
	if failed > 0 && failed == len(targets) {
		return firstErr
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

// renumberEntity дозаполняет пустые значения одного объекта. База читается
// страницами до исчерпания: один вызов List с Limit=MaxListPageSize видел
// только первую тысячу записей, и на большом справочнике команда молча
// «дозаполняла» первую страницу, рапортуя успех (issue #867).
func renumberEntity(ctx context.Context, db *storage.DB, ent *metadata.Entity, write bool) (renumberEntityReport, error) {
	field := storage.AutoNumberField(ent)
	rep := renumberEntityReport{Object: ent.Name, Field: field}
	// Частично выполненная миграция — основной сценарий этой команды: гейт
	// уникальности мог остановить схему на одном объекте, а следующие таблицы
	// ещё не созданы. Для такого объекта дозаполнять нечего. Проверяем наличие
	// явно, не распознаём текст ошибки List: TableExists отделяет «нет таблицы»
	// от настоящего сбоя соединения на обоих диалектах (#1080).
	exists, err := db.TableExists(ctx, metadata.TableName(ent.Name))
	if err != nil {
		return rep, err
	}
	if !exists {
		return rep, nil
	}

	lastRows, err := db.List(ctx, ent.Name, ent, storage.ListParams{
		Sort: "id", Dir: "desc", Limit: 1,
	})
	if err != nil {
		return rep, err
	}
	if len(lastRows) == 0 {
		return rep, nil
	}
	through, err := renumberRowID(lastRows[0])
	if err != nil {
		return rep, err
	}

	var after *uuid.UUID
	for {
		// Курсор по неизменяемому PK не сдвигается при удалении уже пройденных
		// строк. ThroughID фиксирует верхнюю границу ключей на начало команды:
		// новые UUID выше неё в текущий прогон не попадают.
		rows, err := db.List(ctx, ent.Name, ent, storage.ListParams{
			Sort: "id", Limit: storage.MaxListPageSize,
			AfterID: after, ThroughID: &through,
		})
		if err != nil {
			return rep, err
		}
		if len(rows) == 0 {
			return rep, nil
		}
		ids := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			ids[i], err = renumberRowID(row)
			if err != nil {
				return rep, err
			}
		}
		for i, row := range rows {
			raw := rowFieldValue(row, field)
			if !isBlankValue(raw) {
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
			var expected *string
			if raw != nil {
				observed := fmt.Sprintf("%v", raw)
				expected = &observed
			}
			updated, err := db.SetAutoNumberValue(ctx, ent, ids[i], field, expected, value)
			if err != nil {
				return rep, err
			}
			if !updated {
				continue
			}
			rep.Filled++
			if len(rep.Samples) < 3 {
				rep.Samples = append(rep.Samples, value)
			}
		}
		lastID := ids[len(ids)-1]
		if len(rows) < storage.MaxListPageSize || lastID == through {
			return rep, nil
		}
		cursor := lastID
		after = &cursor
	}
}

func renumberRowID(row map[string]any) (uuid.UUID, error) {
	raw := fmt.Sprintf("%v", row["id"])
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("неразбираемый идентификатор %v: %w", row["id"], err)
	}
	if raw != id.String() {
		return uuid.Nil, fmt.Errorf("идентификатор %q не в каноническом формате UUID", raw)
	}
	return id, nil
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
	total, skipped := 0, 0
	for _, r := range reports {
		if r.Error != "" {
			skipped++
			outf("%s.%s: пропущен — %s\n", r.Object, r.Field, r.Error)
			continue
		}
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
		printRenumberSkipped(skipped)
		return
	}
	outf("\nИтого без значения: %d. Ничего не изменено — повторите с --write.\n", total)
	printRenumberSkipped(skipped)
}

// printRenumberSkipped объясняет пропуск: сам по себе он выглядит как отказ, а
// означает «схема этих объектов ещё не догнала конфигурацию». Догоняет её
// миграция при следующем запуске базы — после того, как дозаполнение снимет
// то, обо что она споткнулась.
func printRenumberSkipped(skipped int) {
	if skipped == 0 {
		return
	}
	outf("Пропущено объектов: %d — их таблицы ещё не приведены к конфигурации; повторите после запуска базы.\n", skipped)
}
