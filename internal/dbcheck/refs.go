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
	// Register != nil — колонка принадлежит регистру накопления: очистка меняет
	// то, во что движение агрегируется, поэтому после неё нужно пересчитать итоги
	// (план 80), иначе Остатки()/Обороты() врут молча (#619).
	Register *metadata.Register
	// Manual — причина, по которой автоочистка колонку НЕ трогает. Пусто —
	// зануляется. Причин две, и они разные: измерение регистра сведений
	// объявлено NOT NULL и входит в PRIMARY KEY (#619), а субконто и ресурсы
	// бухрегистра зануляемы технически, но очистка меняет аналитику проводки —
	// такое решение принимает бухгалтер, а не doctor (#910).
	Manual string
}

func (c refsCheck) Run(ctx context.Context, env *Env) Result {
	cols, err := refColumns(ctx, env)
	if err != nil {
		return failed(c, err)
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	total := 0
	autoFixable := 0
	for _, rc := range cols {
		count, examples, err := brokenRefs(ctx, env.DB, rc)
		if err != nil {
			return failed(c, err)
		}
		if count == 0 {
			continue
		}
		total += count
		detail := "ссылка на несуществующий объект"
		if rc.Manual != "" {
			// Причина ручного разбора видна прямо в находке: иначе оператор
			// прочитал бы подсказку «--fix refs очистит» и не понял, почему
			// после починки строки на месте.
			detail += "; " + rc.Manual
		} else {
			autoFixable++
		}
		res.Findings = append(res.Findings, Finding{
			Object:   rc.Object,
			Detail:   detail,
			Count:    count,
			Examples: examples,
		})
	}
	if total == 0 {
		return ok(c, fmt.Sprintf("проверено ссылочных полей: %d, битых ссылок нет", len(cols)))
	}
	res.Severity = SeverityError
	res.Summary = fmt.Sprintf("битых ссылок: %d в %d поле(ях)", total, len(res.Findings))
	if autoFixable > 0 {
		res.FixHint = "onebase doctor --fix refs — очистить битые ссылки (значение станет пустым)"
	} else {
		// Обещать починку, которой не будет, нельзя: все находки требуют
		// ручного разбора, и --fix refs не изменил бы ни строки.
		res.FixHint = "все находки требуют ручного разбора — см. пояснение у каждой"
	}
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
	affected := map[string]*metadata.Register{} // регистры накопления к пересчёту
	var problems []string                       // копим, а не прерываемся на первой
	for _, rc := range cols {
		if rc.Manual != "" {
			// Колонка не зануляется автоматически — показываем оператору, если по
			// ней есть битые ссылки: строки надо разбирать вручную.
			n, _, err := brokenRefs(ctx, env.DB, rc)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", rc.Object, err))
			} else if n > 0 {
				problems = append(problems, fmt.Sprintf("%s: %d битых ссылок, %s", rc.Object, n, rc.Manual))
			}
			continue
		}
		n, err := clearBrokenRefs(ctx, env.DB, rc)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rc.Object, err))
			continue
		}
		fixed += n
		if n > 0 && rc.Register != nil {
			affected[strings.ToLower(rc.Register.Name)] = rc.Register
		}
	}
	// Пересчёт итогов затронутых регистров накопления: очистка измерения изменила
	// то, во что движение агрегируется, а Остатки()/Обороты() читают итоги.
	for _, reg := range affected {
		if !reg.TotalsUsable() {
			continue
		}
		if err := env.DB.RecalcRegisterTotals(ctx, reg); err != nil {
			problems = append(problems, fmt.Sprintf("пересчёт итогов %s: %v", reg.Name, err))
		}
	}
	if len(problems) > 0 {
		return fixed, fmt.Errorf("%s", strings.Join(problems, "; "))
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
	// column — как называется колонка в таблице. Обычно это имя поля, но у
	// субконто бухрегистра имя аналитики и имя колонки не совпадают: колонки
	// нумерованные (субконто1, субконто2…).
	add := func(object, table string, fields []metadata.Field, reg *metadata.Register, manual string,
		protectRequired bool,
		column func(i int, f metadata.Field) string) {
		for i, f := range fields {
			if f.RefEntity == "" {
				continue
			}
			target, ok := known[strings.ToLower(f.RefEntity)]
			if !ok {
				// Ссылка на несуществующий объект конфигурации — это забота
				// `onebase check`, а не наша: данных для проверки просто нет.
				continue
			}
			fieldManual := manual
			if protectRequired && f.Required {
				fieldManual = "обязательный реквизит — занулить нельзя, восстановите ссылку вручную"
			}
			out = append(out, refColumn{
				Object:   object + "." + f.Name,
				Table:    table,
				Column:   column(i, f),
				Target:   target,
				Register: reg,
				Manual:   fieldManual,
			})
		}
	}
	byName := func(_ int, f metadata.Field) string { return metadata.ColumnName(f) }

	for _, e := range env.Entities {
		add(e.Name, metadata.TableName(e.Name), e.Fields, nil, "", true, byName)
		for _, tp := range e.TableParts {
			add(e.Name+"."+tp.Name, metadata.TablePartTableName(e.Name, tp.Name), tp.Fields, nil, "", true, byName)
		}
	}
	for _, reg := range env.Registers {
		table := metadata.RegisterTableName(reg.Name)
		// Измерения и реквизиты регистра накопления зануляемы, но очистка меняет
		// агрегацию — после неё пересчитываем итоги (reg передаём в refColumn).
		add(reg.Name, table, reg.Dimensions, reg, "", false, byName)
		// Ресурс регистра накопления обычно число, но объявить его ссылкой никто
		// не мешает — и CheckRefs на удалении такую ссылку уже считает. Пропуск
		// здесь означал бы, что удалить объект нельзя, а найти почему — нечем.
		add(reg.Name, table, reg.Resources, reg, "", false, byName)
		add(reg.Name, table, reg.Attributes, reg, "", false, byName)
	}
	for _, ir := range env.InfoRegisters {
		table := metadata.InfoRegTableName(ir.Name)
		// Измерения регистра сведений — NOT NULL и в PRIMARY KEY: занулить нельзя.
		add(ir.Name, table, ir.Dimensions, nil,
			"измерение регистра сведений (NOT NULL) — занулить нельзя, разберите строки вручную", false, byName)
		add(ir.Name, table, ir.Resources, nil, "", false, byName)
	}
	// Регистр бухгалтерии — тот самый остаток, замеченный в #881: внешних ключей
	// у его колонок нет, а проверка обходила его стороной, хотя субконто хранит
	// ровно такие же ссылки.
	//
	// Автоочистка сюда не идёт: занулить субконто технически можно, но это
	// меняет аналитику уже проведённой операции и итоги по ней. Такое решение
	// принимает бухгалтер, а не doctor.
	for _, ar := range env.AccountRegisters {
		table := metadata.AccountRegTableName(ar.Name)
		add(ar.Name, table, ar.Subconto, nil,
			"субконто бухрегистра — очистка меняет аналитику проводки, разберите вручную",
			false,
			func(i int, _ metadata.Field) string { return metadata.SubcontoColumn(i + 1) })
		add(ar.Name, table, ar.Resources, nil,
			"ресурс бухрегистра — очистка меняет проводку, разберите вручную", false, byName)
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
