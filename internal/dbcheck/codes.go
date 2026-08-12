package dbcheck

// Коды и номера (план 117E). Проверка отвечает на два вопроса, которые до неё
// администратор мог задать базе только руками:
//
//   - у скольких записей код пуст — это те, что появились до включения
//     нумератора, и пока они пусты, `unique: true` включить нельзя;
//   - какие значения повторяются — до включения уникальности повтор ничем не
//     мешал записи и всплывал уже в отчётах и обмене.
//
// Проверка ничего не чинит сама: дозаполнение — отдельная команда `renumber`, а
// разбор дублей вообще решение человека, машине неоткуда знать, какой из двух
// «К-000042» настоящий.

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
)

type codesCheck struct{}

func (codesCheck) Name() string { return "codes" }
func (codesCheck) Title() string {
	return "Коды справочников и номера документов"
}
func (codesCheck) CanFix() bool { return false } // дозаполняет `onebase renumber`

func (c codesCheck) Run(ctx context.Context, env *Env) Result {
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	checked := 0
	empty, dups := 0, 0

	for _, e := range env.Entities {
		table := metadata.TableName(e.Name)
		if !env.DB.HasTable(ctx, table) {
			continue // таблицы нет — это забота проверки схемы, не этой
		}
		st, ok, err := env.DB.CodeStats(ctx, e)
		if err != nil {
			return failed(c, err)
		}
		if !ok {
			continue // автонумеруемого поля у объекта нет
		}
		checked++
		if st.Empty > 0 {
			empty += st.Empty
			res.Findings = append(res.Findings, Finding{
				Object: e.Name + "." + st.Field,
				Detail: "записей без значения",
				Count:  st.Empty,
			})
		}
		if st.Duplicates > 0 {
			dups += st.Duplicates
			res.Findings = append(res.Findings, Finding{
				Object:   e.Name + "." + st.Field,
				Detail:   "повторяющихся значений",
				Count:    st.Duplicates,
				Examples: st.Examples,
			})
		}
	}

	if checked == 0 {
		return ok(c, "объектов с автонумерацией нет")
	}
	if len(res.Findings) == 0 {
		return ok(c, fmt.Sprintf("коды и номера заполнены и не повторяются (объектов: %d)", checked))
	}
	res.Severity = SeverityWarn
	res.Summary = fmt.Sprintf("без значения: %d, повторяющихся значений: %d", empty, dups)
	switch {
	case empty > 0 && dups > 0:
		res.FixHint = "onebase renumber --project . --write — дозаполнить пустые; дубли разобрать вручную"
	case empty > 0:
		res.FixHint = "onebase renumber --project . --write"
	default:
		res.FixHint = "дубли разобрать вручную: какой из одинаковых кодов верный, знает только человек"
	}
	return res
}

func (codesCheck) Fix(context.Context, *Env, Result) (int, error) { return 0, nil }
