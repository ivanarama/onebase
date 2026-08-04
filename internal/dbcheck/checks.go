package dbcheck

// Остальные проверки: физическая целостность, соответствие схемы метаданным,
// осиротевшие движения, итоги регистров, блобы-сироты.
//
// Три из них — не новый код, а вывод наружу того, что уже было написано, но
// лежало по разным углам: удаление осиротевших движений жило только в
// веб-админке, пересчёт итогов и сборка блобов — в отдельных командах, каждая
// со своим форматом вывода.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// ── Физическая целостность ────────────────────────────────────────────────────

type integrityCheck struct{}

func (integrityCheck) Name() string  { return "integrity" }
func (integrityCheck) Title() string { return "Физическая целостность базы" }
func (integrityCheck) CanFix() bool  { return false }

// Run на SQLite выполняет PRAGMA integrity_check — прямой аналог chdbfl.exe: он
// читает страницы файла и находит повреждения, о которых обычные запросы могут
// молчать месяцами. На PostgreSQL целостность файлов — забота самой СУБД,
// поэтому проверяем только доступность базы.
func (c integrityCheck) Run(ctx context.Context, env *Env) Result {
	if !env.DB.IsSQLite() {
		var one int
		if err := env.DB.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
			return failed(c, err)
		}
		return ok(c, "PostgreSQL отвечает; целостность файлов данных — на стороне СУБД")
	}
	rows, err := env.DB.Query(ctx, "PRAGMA integrity_check")
	if err != nil {
		return failed(c, err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return failed(c, err)
		}
		if strings.EqualFold(strings.TrimSpace(v), "ok") {
			continue
		}
		problems = append(problems, v)
	}
	if err := rows.Err(); err != nil {
		return failed(c, err)
	}
	if len(problems) == 0 {
		return ok(c, "файл базы цел (PRAGMA integrity_check)")
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityError,
		Summary: fmt.Sprintf("SQLite сообщает о повреждениях: %d", len(problems)),
		FixHint: "восстановите базу из резервной копии — повреждённый файл чинится только так"}
	if len(problems) > maxExamples {
		problems = problems[:maxExamples]
	}
	res.Findings = append(res.Findings, Finding{Object: "файл базы", Detail: "повреждение структуры", Examples: problems})
	return res
}

func (integrityCheck) Fix(context.Context, *Env, Result) (int, error) { return 0, nil }

// ── Схема против метаданных ───────────────────────────────────────────────────

type schemaCheck struct{}

func (schemaCheck) Name() string  { return "schema" }
func (schemaCheck) Title() string { return "Соответствие схемы метаданным" }
func (schemaCheck) CanFix() bool  { return false } // чинит `onebase migrate`

func (c schemaCheck) Run(ctx context.Context, env *Env) Result {
	plan, err := env.DB.PlanMigration(ctx, env.Entities, env.Registers, env.InfoRegisters)
	if err != nil {
		return failed(c, err)
	}
	if len(plan) == 0 {
		return ok(c, "схема существующих таблиц соответствует метаданным")
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityWarn,
		Summary: fmt.Sprintf("схема расходится с метаданными: %d изменен.", len(plan)),
		FixHint: "onebase migrate --dry-run — посмотреть план, затем onebase migrate"}
	for _, ch := range plan {
		// В описании изменения таблица уже названа — иначе в отчёте выходило
		// «товары: товары: переименовать колонку …».
		res.Findings = append(res.Findings, Finding{
			Object: ch.Table,
			Detail: strings.TrimPrefix(ch.String(), ch.Table+": "),
		})
	}
	return res
}

func (schemaCheck) Fix(context.Context, *Env, Result) (int, error) { return 0, nil }

// ── Осиротевшие движения ──────────────────────────────────────────────────────

type orphanMovementsCheck struct{}

func (orphanMovementsCheck) Name() string { return "orphan-movements" }
func (orphanMovementsCheck) Title() string {
	return "Движения без документа-регистратора"
}
func (orphanMovementsCheck) CanFix() bool { return true }

// Движение, чей регистратор удалён, продолжает участвовать в остатках: отчёт
// показывает товар, которого нет ни в одном документе. Раньше это лечилось
// только через веб-админку, то есть на поднятой базе.
// Run различает два случая, которые до плана 114 считались одним и тем же:
//
//   - документ известен конфигурации, но записи с таким id нет — это сирота,
//     движение можно удалять;
//   - документа с таким именем в конфигурации НЕТ — движения целы, а не
//     осиротели. Тип регистратора хранится строкой, поэтому расходится он и
//     при обычном переименовании документа, и при запуске против другой
//     конфигурации. Удалять такие движения нельзя: данные не должны исчезать
//     оттого, что метаданные о них не упоминают.
func (c orphanMovementsCheck) Run(ctx context.Context, env *Env) Result {
	stats := env.DB.OrphanMovements(ctx, env.Registers, env.Entities)
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	orphans, unknown := 0, 0
	for _, s := range stats {
		if s.Count == 0 {
			continue
		}
		if s.UnknownType {
			unknown += s.Count
			res.Findings = append(res.Findings, Finding{
				Object: s.RegisterName,
				Detail: "документа «" + s.RecorderType + "» нет в конфигурации; движения целы и НЕ удаляются",
				Count:  s.Count,
			})
			continue
		}
		orphans += s.Count
		res.Findings = append(res.Findings, Finding{
			Object: s.RegisterName,
			Detail: "регистратор (" + s.RecorderType + ") не существует",
			Count:  s.Count,
		})
	}
	switch {
	case orphans == 0 && unknown == 0:
		return ok(c, "движений без регистратора нет")
	case orphans == 0:
		res.Severity = SeverityWarn
		res.Summary = fmt.Sprintf("движений документов вне конфигурации: %d", unknown)
		res.FixHint = "проверьте, не переименован ли документ и та ли это конфигурация; " +
			"удалять такие движения автоматически платформа не станет"
	default:
		res.Severity = SeverityError
		res.Summary = fmt.Sprintf("движений без регистратора: %d", orphans)
		if unknown > 0 {
			res.Summary += fmt.Sprintf("; движений документов вне конфигурации: %d", unknown)
		}
		res.FixHint = "onebase doctor --fix orphan-movements — удалить сирот и пересчитать итоги " +
			"(движения документов вне конфигурации не трогаются)"
	}
	return res
}

// Fix удаляет осиротевшие движения и пересчитывает итоги затронутых регистров:
// без пересчёта остатки остались бы прежними, и удаление ничего бы не изменило
// для пользователя (дефект Д1 из аудита — ровно про это).
func (c orphanMovementsCheck) Fix(ctx context.Context, env *Env, _ Result) (int, error) {
	deleted := env.DB.DeleteOrphanMovements(ctx, env.Registers, env.Entities)
	if deleted == 0 {
		return 0, nil
	}
	for _, reg := range env.Registers {
		if !reg.TotalsUsable() {
			continue
		}
		if err := env.DB.RecalcRegisterTotals(ctx, reg); err != nil {
			return int(deleted), fmt.Errorf("пересчёт итогов %s: %w", reg.Name, err)
		}
	}
	return int(deleted), nil
}

// ── Итоги регистров ───────────────────────────────────────────────────────────

type totalsCheck struct{}

func (totalsCheck) Name() string  { return "totals" }
func (totalsCheck) Title() string { return "Итоги регистров" }
func (totalsCheck) CanFix() bool  { return true }

// Таблица итогов — это те же движения, свёрнутые по месяцам, поэтому сумма
// ресурса по итогам обязана совпадать с суммой по движениям. Расхождение
// означает, что итоги отстали: движения меняли мимо пересчёта (свёрткой,
// прямым SQL, восстановлением части данных).
func (c totalsCheck) Run(ctx context.Context, env *Env) Result {
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	checked := 0
	for _, reg := range env.Registers {
		if !reg.TotalsUsable() {
			continue
		}
		src := metadata.RegisterTableName(reg.Name)
		totals := metadata.RegisterTotalsTableName(reg.Name)
		if !env.DB.HasTable(ctx, src) || !env.DB.HasTable(ctx, totals) {
			continue
		}
		for _, r := range reg.Resources {
			checked++
			// Числовое значение ресурса: на SQLite сырая колонка регистра —
			// TEXT, поэтому CAST обязателен по обе стороны сверки.
			num := "CAST(" + quoteIdent(metadata.ColumnName(r)) + " AS NUMERIC)"
			// Движения суммируются ЗНАКОВО — тем же выражением, которым итоги и
			// заполняются (storage.SignedResourceSum). Беззнаковый SUM давал бы
			// расхождение на каждом регистре, где есть расход: итоги 100−30=70
			// против «движений» 100+30=130 — то есть проверка объявляла бы
			// ошибкой исправную базу.
			fromMoves, err := sumExpr(ctx, env, src, storage.SignedResourceSum(num))
			if err != nil {
				return failed(c, err)
			}
			// В итогах знак уже учтён при записи — здесь просто сумма по месяцам.
			fromTotals, err := sumExpr(ctx, env, totals, "SUM("+num+")")
			if err != nil {
				return failed(c, err)
			}
			if fromMoves.Equal(fromTotals) {
				continue
			}
			res.Findings = append(res.Findings, Finding{
				Object: reg.Name + "." + r.Name,
				Detail: fmt.Sprintf("итоги %s, движения %s", fromTotals.String(), fromMoves.String()),
			})
		}
	}
	if len(res.Findings) == 0 {
		return ok(c, fmt.Sprintf("проверено ресурсов: %d, итоги сходятся с движениями", checked))
	}
	res.Severity = SeverityError
	res.Summary = fmt.Sprintf("итоги расходятся с движениями: %d ресурс(ов)", len(res.Findings))
	res.FixHint = "onebase doctor --fix totals (то же делает onebase recalc-totals)"
	return res
}

func (c totalsCheck) Fix(ctx context.Context, env *Env, res Result) (int, error) {
	// Пересчитываем только регистры с находками: полный пересчёт по всей базе
	// — операция на минуты, и просить её там, где разошёлся один регистр,
	// незачем.
	broken := map[string]bool{}
	for _, f := range res.Findings {
		broken[strings.ToLower(strings.SplitN(f.Object, ".", 2)[0])] = true
	}
	fixed := 0
	for _, reg := range env.Registers {
		if !reg.TotalsUsable() || !broken[strings.ToLower(reg.Name)] {
			continue
		}
		if err := env.DB.RecalcRegisterTotals(ctx, reg); err != nil {
			return fixed, fmt.Errorf("пересчёт итогов %s: %w", reg.Name, err)
		}
		fixed++
	}
	return fixed, nil
}

// sumExpr вычисляет агрегат agg по таблице и возвращает его как decimal.
// Значение читается строкой: на SQLite числа хранятся в TEXT, и float здесь дал
// бы копеечные расхождения на ровном месте (тот самый дефект Д2 из аудита).
//
// Выражение агрегата задаёт вызывающий: движения и итоги сверяются РАЗНЫМИ
// выражениями (знаковая сумма против обычной), и прятать эту разницу внутрь
// помощника значило бы снова сделать её незаметной.
func sumExpr(ctx context.Context, env *Env, table, agg string) (decimal.Decimal, error) {
	var raw *string
	q := "SELECT CAST(COALESCE(" + agg + ", 0) AS TEXT) FROM " + quoteIdent(table)
	if err := env.DB.QueryRow(ctx, q).Scan(&raw); err != nil {
		return decimal.Zero, fmt.Errorf("%s: сумма %s: %w", table, agg, err)
	}
	if raw == nil {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(strings.TrimSpace(*raw))
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s: сумма %q не разбирается как число: %w", table, *raw, err)
	}
	return d, nil
}

// ── Блобы-сироты ──────────────────────────────────────────────────────────────

type blobsCheck struct{}

func (blobsCheck) Name() string  { return "blobs" }
func (blobsCheck) Title() string { return "Бинарники без владельца" }
func (blobsCheck) CanFix() bool  { return true }

// blobGrace — окно, в котором свежий блоб не считается сиротой: он мог быть
// загружен формой, которую ещё не сохранили. То же значение, что по умолчанию
// у `onebase gc-blobs`.
const blobGrace = 24 * time.Hour

func (c blobsCheck) Run(ctx context.Context, env *Env) Result {
	// База, где не пользовались картинками, таблицы бинарников не заводила —
	// это не повод объявлять проверку сбойной.
	if !env.DB.HasTable(ctx, "_blobs") {
		return ok(c, "хранилище бинарников не создавалось — проверять нечего")
	}
	st, err := env.DB.SweepOrphanBlobs(ctx, env.Entities, blobGrace, true)
	if err != nil {
		return failed(c, err)
	}
	if len(st.Orphans) == 0 {
		return ok(c, fmt.Sprintf("бинарников: %d, все на месте (живых ссылок: %d)", st.TotalBlobs, st.LiveRefs))
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityWarn,
		Summary: fmt.Sprintf("бинарников без владельца: %d из %d", len(st.Orphans), st.TotalBlobs),
		FixHint: "onebase doctor --fix blobs (то же делает onebase gc-blobs --delete)"}
	f := Finding{Object: "_blobs", Detail: "на бинарник не ссылается ни одна запись", Count: len(st.Orphans)}
	for i, id := range st.Orphans {
		if i >= maxExamples {
			break
		}
		f.Examples = append(f.Examples, id.String())
	}
	res.Findings = append(res.Findings, f)
	return res
}

func (c blobsCheck) Fix(ctx context.Context, env *Env, _ Result) (int, error) {
	st, err := env.DB.SweepOrphanBlobs(ctx, env.Entities, blobGrace, false)
	if err != nil {
		return 0, err
	}
	return st.Deleted, nil
}
