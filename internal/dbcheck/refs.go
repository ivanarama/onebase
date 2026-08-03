package dbcheck

// Ссылочная целостность: ссылки, указывающие на несуществующий объект.
//
// В 1С это «объект не найден» — самая заметная для пользователя поломка данных:
// в документе вместо контрагента показывается битая ссылка, отчёты по нему
// молча теряют строки. Появляется она обычным путём: объект удалили прямым SQL
// или восстановлением частичной копии, при обмене приехала ссылка на узел,
// которого здесь нет, конфигурацию переименовали мимо миграции.
//
// Проверка идёт по колонке за запрос, а не по строке: у базы на сотню тысяч
// документов построчный обход занял бы часы, а один LEFT JOIN на колонку —
// секунды.

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type refsCheck struct{}

func (refsCheck) Name() string  { return "refs" }
func (refsCheck) Title() string { return "Ссылочная целостность" }
func (refsCheck) CanFix() bool  { return true }

// refColumn — одна ссылочная колонка и объект, на который она обязана ссылаться.
type refColumn struct {
	Object string // как показать человеку: «Реализация.Контрагент»
	Table  string
	Column string
	Target string // таблица объекта-цели
}

func (c refsCheck) Run(ctx context.Context, env *Env) Result {
	cols, err := refColumns(ctx, env)
	if err != nil {
		return failed(c, err)
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	total := 0
	for _, rc := range cols {
		count, examples, err := brokenRefs(ctx, env.DB, rc)
		if err != nil {
			return failed(c, err)
		}
		if count == 0 {
			continue
		}
		total += count
		res.Findings = append(res.Findings, Finding{
			Object:   rc.Object,
			Detail:   "ссылка на несуществующий объект",
			Count:    count,
			Examples: examples,
		})
	}
	if total == 0 {
		return ok(c, fmt.Sprintf("проверено ссылочных полей: %d, битых ссылок нет", len(cols)))
	}
	res.Severity = SeverityError
	res.Summary = fmt.Sprintf("битых ссылок: %d в %d поле(ях)", total, len(res.Findings))
	res.FixHint = "onebase doctor --fix refs — очистить битые ссылки (значение станет пустым)"
	return res
}

// Fix очищает битые ссылки. Условие то же, что у проверки, и вычисляется
// заново: между отчётом и починкой данные могли измениться, а очищать нужно
// ровно то, что битое прямо сейчас — ни строкой больше.
func (c refsCheck) Fix(ctx context.Context, env *Env, _ Result) (int, error) {
	cols, err := refColumns(ctx, env)
	if err != nil {
		return 0, err
	}
	fixed := 0
	for _, rc := range cols {
		n, err := clearBrokenRefs(ctx, env.DB, rc)
		if err != nil {
			return fixed, err
		}
		fixed += n
	}
	return fixed, nil
}

// refColumns собирает все ссылочные колонки конфигурации: шапки, табличные
// части, измерения и реквизиты регистров накопления, измерения и ресурсы
// регистров сведений.
func refColumns(ctx context.Context, env *Env) ([]refColumn, error) {
	known := map[string]string{} // имя объекта в нижнем регистре → таблица
	for _, e := range env.Entities {
		known[strings.ToLower(e.Name)] = metadata.TableName(e.Name)
	}

	var out []refColumn
	add := func(object, table string, fields []metadata.Field) {
		for _, f := range fields {
			if f.RefEntity == "" {
				continue
			}
			target, ok := known[strings.ToLower(f.RefEntity)]
			if !ok {
				// Ссылка на несуществующий объект конфигурации — это забота
				// `onebase check`, а не наша: данных для проверки просто нет.
				continue
			}
			out = append(out, refColumn{
				Object: object + "." + f.Name,
				Table:  table,
				Column: metadata.ColumnName(f),
				Target: target,
			})
		}
	}

	for _, e := range env.Entities {
		add(e.Name, metadata.TableName(e.Name), e.Fields)
		for _, tp := range e.TableParts {
			add(e.Name+"."+tp.Name, metadata.TablePartTableName(e.Name, tp.Name), tp.Fields)
		}
	}
	for _, reg := range env.Registers {
		table := metadata.RegisterTableName(reg.Name)
		add(reg.Name, table, reg.Dimensions)
		add(reg.Name, table, reg.Attributes)
	}
	for _, ir := range env.InfoRegisters {
		table := metadata.InfoRegTableName(ir.Name)
		add(ir.Name, table, ir.Dimensions)
		add(ir.Name, table, ir.Resources)
	}

	// Таблицы может не быть — например, объект только что добавили в
	// конфигурацию, а миграцию ещё не выполнили.
	var existing []refColumn
	for _, rc := range out {
		if env.DB.HasTable(ctx, rc.Table) && env.DB.HasTable(ctx, rc.Target) {
			existing = append(existing, rc)
		}
	}
	return existing, nil
}

// brokenWhere — условие «ссылка задана, а объекта нет».
//
// Внешняя колонка квалифицирована именем таблицы, а таблица-цель получает
// псевдоним: иначе в подзапросе неквалифицированное имя связалось бы с целевой
// таблицей, если в ней есть колонка с тем же именем, — и проверка тихо давала
// бы неверный ответ.
func brokenWhere(rc refColumn) string {
	col := quoteIdent(rc.Table) + "." + quoteIdent(rc.Column)
	return col + " IS NOT NULL AND NOT EXISTS (SELECT 1 FROM " + quoteIdent(rc.Target) +
		" AS __ref WHERE __ref." + quoteIdent("id") + " = " + col + ")"
}

func brokenRefs(ctx context.Context, db *storage.DB, rc refColumn) (int, []string, error) {
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM "+quoteIdent(rc.Table)+" WHERE "+brokenWhere(rc)).Scan(&count); err != nil {
		return 0, nil, fmt.Errorf("%s: подсчёт битых ссылок: %w", rc.Object, err)
	}
	if count == 0 {
		return 0, nil, nil
	}
	rows, err := db.Query(ctx,
		"SELECT DISTINCT CAST("+quoteIdent(rc.Column)+" AS TEXT) FROM "+quoteIdent(rc.Table)+
			" WHERE "+brokenWhere(rc)+" LIMIT "+fmt.Sprint(maxExamples))
	if err != nil {
		return 0, nil, fmt.Errorf("%s: примеры битых ссылок: %w", rc.Object, err)
	}
	defer rows.Close()
	var examples []string
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return 0, nil, fmt.Errorf("%s: примеры битых ссылок: %w", rc.Object, err)
		}
		if v != nil {
			examples = append(examples, *v)
		}
	}
	return count, examples, rows.Err()
}

func clearBrokenRefs(ctx context.Context, db *storage.DB, rc refColumn) (int, error) {
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM "+quoteIdent(rc.Table)+" WHERE "+brokenWhere(rc)).Scan(&count); err != nil {
		return 0, fmt.Errorf("%s: подсчёт битых ссылок: %w", rc.Object, err)
	}
	if count == 0 {
		return 0, nil
	}
	if _, err := db.Exec(ctx,
		"UPDATE "+quoteIdent(rc.Table)+" SET "+quoteIdent(rc.Column)+" = NULL WHERE "+brokenWhere(rc)); err != nil {
		return 0, fmt.Errorf("%s: очистка битых ссылок: %w", rc.Object, err)
	}
	return count, nil
}
