// Package dbcheck — проверки состояния информационной базы (план 114), аналог
// «Тестирования и исправления» 1С.
//
// Проверки собраны в одном месте по простой причине: половина из них уже была
// написана, но жила порознь — пересчёт итогов отдельной командой, сироты-блобы
// другой, удаление осиротевших движений вообще только в веб-админке. Базу,
// которую не удаётся поднять, чинить было нечем.
//
// Два правила, общих для всех проверок:
//
//  1. Проверка читает. Ничего не меняет, пока администратор не назвал её в
//     --fix явно. Диагностика, которая может что-то испортить, не запускается
//     «на всякий случай».
//  2. Проверка объясняет. Находка — это объект, количество и примеры, а не
//     «обнаружены ошибки»: по отчёту должно быть понятно, что именно чинить,
//     даже если чинить решено руками.
package dbcheck

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Severity — насколько всё плохо.
type Severity string

// Уровни находок.
const (
	// SeverityOK — проверка прошла, делать нечего.
	SeverityOK Severity = "ok"
	// SeverityWarn — есть расхождение, но данные целы и база работает.
	SeverityWarn Severity = "warn"
	// SeverityError — испорчены данные или недоступна сама база.
	SeverityError Severity = "error"
)

// Finding — одна находка проверки.
type Finding struct {
	Object   string   `json:"object"`             // где: «Реализация.Контрагент», «рег_остатки»
	Detail   string   `json:"detail"`             // что именно не так
	Count    int      `json:"count,omitempty"`    // сколько строк/значений затронуто
	Examples []string `json:"examples,omitempty"` // до maxExamples примеров
}

// Result — итог одной проверки.
type Result struct {
	Check    string    `json:"check"`
	Title    string    `json:"title"`
	Severity Severity  `json:"severity"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
	// Fixed — сколько исправлено (заполняется, когда проверка названа в --fix).
	Fixed int `json:"fixed,omitempty"`
	// FixHint — как починить, если проверка сама этого не умеет.
	FixHint string `json:"fixHint,omitempty"`
	// Error — проверку не удалось выполнить (это не то же самое, что находки).
	Error string `json:"error,omitempty"`
}

// maxExamples ограничивает число примеров в находке: список нужен, чтобы понять
// характер проблемы, а не чтобы выгрузить данные в отчёт.
const maxExamples = 5

// Env — то, над чем работают проверки.
type Env struct {
	DB   *storage.DB
	Proj *project.Project
}

// Check — одна проверка. Fix вызывается только по явной просьбе и только после
// успешного Run: он получает результат проверки, чтобы не искать всё заново.
type Check interface {
	Name() string  // ключ для --check/--fix
	Title() string // название для человека
	Run(ctx context.Context, env *Env) Result
	// CanFix сообщает, умеет ли проверка чинить найденное сама.
	CanFix() bool
	// Fix исправляет найденное и возвращает число исправленных объектов.
	Fix(ctx context.Context, env *Env, res Result) (int, error)
}

// All возвращает все проверки в порядке, в котором их разумно читать: от
// структуры базы к её содержимому.
func All() []Check {
	return []Check{
		integrityCheck{},
		schemaCheck{},
		refsCheck{},
		orphanMovementsCheck{},
		totalsCheck{},
		blobsCheck{},
	}
}

// Select возвращает проверки с указанными именами. Пустой список — все.
// Неизвестное имя возвращается вторым значением, чтобы CLI мог сказать об
// опечатке, а не молча проверить не то.
func Select(names []string) ([]Check, []string) {
	if len(names) == 0 {
		return All(), nil
	}
	want := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			want[n] = true
		}
	}
	var out []Check
	for _, c := range All() {
		if want[c.Name()] {
			out = append(out, c)
			delete(want, c.Name())
		}
	}
	var unknown []string
	for n := range want {
		unknown = append(unknown, n)
	}
	sort.Strings(unknown)
	return out, unknown
}

// Report — итог всего прогона.
type Report struct {
	Results []Result `json:"results"`
}

// Worst возвращает наибольший уровень среди результатов — по нему CLI выбирает
// код возврата.
func (r Report) Worst() Severity {
	worst := SeverityOK
	for _, res := range r.Results {
		switch res.Severity {
		case SeverityError:
			return SeverityError
		case SeverityWarn:
			worst = SeverityWarn
		}
	}
	return worst
}

// Run выполняет проверки и, для названных в fix, исправляет найденное.
func Run(ctx context.Context, env *Env, checks []Check, fix map[string]bool) Report {
	var rep Report
	for _, c := range checks {
		res := c.Run(ctx, env)
		if fix[c.Name()] && res.Error == "" && len(res.Findings) > 0 {
			if !c.CanFix() {
				// Сохраняем подсказку: проверка нашла проблему, но чинится она
				// не здесь (например, реструктуризацией схемы).
				rep.Results = append(rep.Results, res)
				continue
			}
			n, err := c.Fix(ctx, env, res)
			if err != nil {
				res.Fixed = n
				res.Error = err.Error()
				res.Severity = SeverityError
			} else if n > 0 {
				// Состояние после починки берём перезапуском проверки, а не
				// объявляем «теперь всё хорошо»: починка бывает частичной —
				// часть находок чинится, часть требует решения человека, — и
				// отчёт обязан показывать то, что осталось, а не то, что мы
				// намеревались сделать.
				res = c.Run(ctx, env)
				res.Fixed = n
				res.Summary = res.Summary + "; исправлено: " + strconv.Itoa(n)
			}
		}
		rep.Results = append(rep.Results, res)
	}
	return rep
}

// ok — короткий конструктор результата «всё в порядке».
func ok(c Check, summary string) Result {
	return Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK, Summary: summary}
}

// failed — проверку не удалось выполнить.
func failed(c Check, err error) Result {
	return Result{Check: c.Name(), Title: c.Title(), Severity: SeverityError,
		Summary: "проверку выполнить не удалось", Error: err.Error()}
}

// quoteIdent экранирует идентификатор для обоих диалектов.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
