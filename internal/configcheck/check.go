// Package configcheck validates a OneBase configuration: YAML metadata schema,
// DSL syntax, undefined function calls, and query compilation. It reports each
// problem with a file:line:column location.
//
// The logic was originally inside internal/launcher (configurator endpoints);
// it lives here so both the web configurator and the `onebase check` CLI gate
// share one implementation.
package configcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
)

// Issue is a single error/warning. Line and column are 1-based; zero means the
// parser could not pinpoint a position.
type Issue struct {
	File         string `json:"file,omitempty"`
	Object       string `json:"object,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message"`
	SuggestedFix string `json:"suggestedFix,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
}

// Result is the machine-readable check outcome: ok=true means clean.
// Warnings are informational notices that do NOT affect OK or exit code.
type Result struct {
	OK       bool    `json:"ok"`
	Total    int     `json:"total"`
	Issues   []Issue `json:"issues,omitempty"`
	Warnings []Issue `json:"warnings,omitempty"`
}

// NewResult wraps a slice of issues and warnings into a Result.
// OK is determined solely by len(issues) == 0; warnings never set OK=false.
func NewResult(issues []Issue, warnings ...[]Issue) Result {
	issues = AnnotateIssues(issues)
	var ws []Issue
	for _, w := range warnings {
		ws = append(ws, w...)
	}
	ws = AnnotateIssues(ws)
	return Result{OK: len(issues) == 0, Total: len(issues), Issues: issues, Warnings: ws}
}

// AnnotateIssues fills machine-readable Code and SuggestedFix for common issue
// classes. It is intentionally heuristic: loaders still own the exact messages,
// while AI tooling gets a stable enough contract for remediation loops.
func AnnotateIssues(in []Issue) []Issue {
	if len(in) == 0 {
		return in
	}
	out := make([]Issue, len(in))
	for i, is := range in {
		out[i] = annotateIssue(is)
	}
	return out
}

func annotateIssue(is Issue) Issue {
	if is.Code != "" && is.SuggestedFix != "" {
		return is
	}
	lowMsg := strings.ToLower(is.Message)
	lowKind := strings.ToLower(is.Kind)
	lowFile := strings.ToLower(is.File)
	set := func(code, fix string) {
		if is.Code == "" {
			is.Code = code
		}
		if is.SuggestedFix == "" {
			is.SuggestedFix = fix
		}
	}
	switch {
	case strings.Contains(lowMsg, "устаревший формат печатной формы"):
		set("printform.legacy", "Запустите `onebase printforms migrate` или перепишите печатную форму в layout v2.")
	case strings.Contains(lowMsg, "форма пустая"):
		set("printform.empty", "Опишите legacy-поля `title/header/table/footer` или переведите файл в layout v2.")
	case strings.Contains(lowMsg, "wizard") || strings.Contains(lowMsg, "steps"):
		set("processor.unsupported-key", "Удалите неподдерживаемые `wizard/steps`; сейчас обработка принимает плоский список `params`.")
	case strings.Contains(lowMsg, "выражение без эффекта"):
		set("dsl.expression-without-effect", "Если это вызов процедуры, добавьте скобки: `ИмяПроцедуры()`.")
	case strings.Contains(lowMsg, "неизвестная функция"):
		set("dsl.unknown-function", "Проверьте имя процедуры/функции, импортируемый модуль или используйте существенный builtin из `onebase ai-guide`.")
	case strings.HasSuffix(lowFile, ".os") || strings.Contains(lowKind, "dsl"):
		set("dsl.syntax", "Проверьте синтаксис модуля около указанной строки и колонки.")
	case strings.Contains(lowMsg, "yaml:") || strings.Contains(lowMsg, "cannot unmarshal") || strings.Contains(lowMsg, "unmarshal errors") || strings.Contains(lowMsg, "did not find expected"):
		set("yaml.invalid", "Исправьте YAML-синтаксис, отступы и тип значения в указанном файле.")
	case strings.Contains(lowKind, "запрос") || strings.Contains(lowMsg, "compile query") || strings.Contains(lowMsg, "no such table") || strings.Contains(lowMsg, "ambiguous column"):
		set("query.invalid", "Проверьте источник запроса, имена полей и параметры; для диагностики используйте `onebase query --sql`.")
	case strings.Contains(lowKind, "компоновка"):
		set("report.composition.invalid", "Сверьте поля группировок, показателей, сортировки и графика с колонками запроса отчёта.")
	case strings.Contains(lowKind, "имя таблицы") || strings.Contains(lowMsg, "коллизия"):
		set("metadata.name-collision", "Переименуйте один из объектов, чтобы их физические имена таблиц не совпадали.")
	case strings.Contains(lowMsg, "project.load"):
		set("project.load-failed", "Сначала исправьте ошибку загрузки проекта; затем повторите `onebase check --json`.")
	default:
		set("metadata.invalid", "Исправьте структуру объекта по `onebase schema` и повторите `onebase check`.")
	}
	return is
}

// CheckDir walks the configuration sources under dir so each broken file is
// reported individually instead of aborting on the first error (which is what
// project.Load would do). Covers YAML metadata + .os DSL files.
// Returns (issues, warnings): issues cause check to fail; warnings are
// informational and do not affect the exit code.
func CheckDir(dir string) (issues, warnings []Issue) {

	type yamlGroup struct {
		subdir string
		kind   string
		check  func(path string) error
	}
	groups := []yamlGroup{
		{"catalogs", "Справочник", func(p string) error { _, err := metadata.LoadFile(p, metadata.KindCatalog); return err }},
		{"documents", "Документ", func(p string) error { _, err := metadata.LoadFile(p, metadata.KindDocument); return err }},
		{"registers", "Регистр", func(p string) error { _, err := metadata.LoadRegisterFile(p); return err }},
		{"inforegs", "Регистр сведений", func(p string) error { _, err := metadata.LoadInfoRegisterFile(p); return err }},
		{"enums", "Перечисление", func(p string) error { _, err := metadata.LoadEnumFile(p); return err }},
		{"constants", "Константы", func(p string) error { _, err := metadata.LoadConstantsFile(p); return err }},
		{"widgets", "Виджет", func(p string) error { _, err := metadata.LoadWidgetFile(p); return err }},
		{"accounts", "План счетов", func(p string) error { _, err := metadata.LoadChartOfAccountsFile(p); return err }},
		{"accountregs", "Регистр бухгалтерии", func(p string) error { _, err := metadata.LoadAccountRegisterFile(p); return err }},
		{"journals", "Журнал", func(p string) error { _, err := metadata.LoadJournalFile(p); return err }},
		{"subsystems", "Подсистема", func(p string) error { _, err := metadata.LoadSubsystemFile(p); return err }},
		{"scheduled", "Регламентное задание", func(p string) error { _, err := metadata.LoadScheduledFile(p); return err }},
		{"reports", "Отчёт", func(p string) error { _, err := report.LoadFile(p); return err }},
		{"roles", "Роль", func(p string) error { _, err := auth.LoadRoleFile(p); return err }},
	}
	for _, g := range groups {
		gdir := filepath.Join(dir, g.subdir)
		entries, _ := os.ReadDir(gdir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
				continue
			}
			path := filepath.Join(gdir, e.Name())
			if err := g.check(path); err != nil {
				issues = append(issues, Issue{
					File:    g.subdir + "/" + e.Name(),
					Object:  strings.TrimSuffix(e.Name(), ".yaml"),
					Kind:    g.kind,
					Message: err.Error(),
				})
			}
		}
	}

	// processors/*.yaml — валидность + предупреждение о неподдерживаемых
	// конструкциях мастера (wizard/steps). Платформа рендерит только плоский
	// список params; wizard:true / steps: молча игнорируются yaml.v3, и форма
	// вырождается в одну кнопку «Выполнить» (issue #14). Сообщаем об этом явно.
	procDir := filepath.Join(dir, "processors")
	procEntries, _ := os.ReadDir(procDir)
	for _, e := range procEntries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		path := filepath.Join(procDir, e.Name())
		label := "processors/" + e.Name()
		object := strings.TrimSuffix(e.Name(), ".yaml")
		if _, err := processor.LoadFile(path); err != nil {
			issues = append(issues, Issue{File: label, Object: object, Kind: "Обработка", Message: err.Error()})
			continue
		}
		for _, key := range unsupportedProcessorKeys(path) {
			issues = append(issues, Issue{
				File:    label,
				Object:  object,
				Kind:    "Обработка",
				Message: fmt.Sprintf("ключ %q не поддерживается: многошаговые мастера не реализованы, опишите параметры плоским списком «params» (иначе форма покажет только кнопку «Выполнить»)", key),
			})
		}
	}

	// printforms/*.yaml — устаревший плоский формат печатных форм. Валидность +
	// предупреждение о пустой форме + предупреждение о необходимости миграции в
	// макет v2. Файлы *.layout.yaml (макет v2) сюда НЕ попадают — они проверяются
	// валидатором binding в cross_refs (CheckCrossRefs). yaml.v3 молча игнорирует
	// неизвестные ключи, поэтому пустую legacy-форму отлавливаем явно.
	pfDir := filepath.Join(dir, "printforms")
	pfEntries, _ := os.ReadDir(pfDir)
	for _, e := range pfEntries {
		lower := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".layout.yaml") {
			continue
		}
		path := filepath.Join(pfDir, e.Name())
		label := "printforms/" + e.Name()
		object := strings.TrimSuffix(e.Name(), ".yaml")
		pf, err := printform.LoadFile(path)
		if err != nil {
			issues = append(issues, Issue{File: label, Object: object, Kind: "Печатная форма", Message: err.Error()})
			continue
		}
		if pf.Title == "" && pf.Header == "" && pf.Footer == "" && pf.Table == nil {
			issues = append(issues, Issue{
				File:    label,
				Object:  object,
				Kind:    "Печатная форма",
				Message: "форма пустая: не заданы ни title, ни header, ни table, ни footer (проверьте формат — поддерживаются эти ключи, а не «layout»)",
			})
			continue
		}
		// Непустая legacy-форма валидна, но устарела — предупреждение о миграции в v2.
		// Предупреждение не влияет на OK / exit code (план 64, этап 4).
		warnings = append(warnings, Issue{
			File:    label,
			Object:  object,
			Kind:    "Печатная форма",
			Message: "устаревший формат печатной формы, выполните onebase printforms migrate",
		})
	}

	// home_page.yaml — single file
	hpPath := filepath.Join(dir, "config", "home_page.yaml")
	if _, err := metadata.LoadHomePage(hpPath); err != nil {
		issues = append(issues, Issue{
			File:    "config/home_page.yaml",
			Object:  "home_page",
			Kind:    "Главная страница",
			Message: err.Error(),
		})
	}

	// .os DSL files — два прохода, чтобы вызовы процедур из ДРУГИХ модулей
	// (общие модули, утилиты) не считались «неизвестными функциями».
	srcDir := filepath.Join(dir, "src")
	entries, _ := os.ReadDir(srcDir)
	type parsedModule struct {
		label string
		prog  *ast.Program
	}
	var modules []parsedModule
	projProcs := map[string]struct{}{} // имена всех процедур по всей конфигурации
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".os") {
			continue
		}
		label := "src/" + e.Name()
		raw, rerr := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if rerr != nil {
			issues = append(issues, Issue{File: label, Message: rerr.Error()})
			continue
		}
		l := lexer.New(string(raw), label)
		prog, perr := parser.New(l).ParseProgram()
		if perr != nil {
			issues = append(issues, Issue{File: label, Message: perr.Error()})
			continue
		}
		// Тело модуля обработки (#171) сворачиваем в Выполнить, чтобы вызовы в нём
		// проверялись на неизвестные функции наравне с процедурами. У прочих
		// модулей тело недопустимо, но эту ошибку даёт project.Load (cli/check.go) —
		// здесь не дублируем.
		if len(prog.Body) > 0 && strings.HasSuffix(strings.ToLower(e.Name()), ".proc.os") {
			prog.Procedures = append(prog.Procedures, ast.NewProcedureFromBody("Выполнить", label, prog.ModuleVars, prog.Body))
			prog.Body = nil
		}
		for _, pr := range prog.Procedures {
			projProcs[strings.ToLower(pr.Name.Literal)] = struct{}{}
		}
		modules = append(modules, parsedModule{label, prog})
	}
	for _, m := range modules {
		issues = append(issues, CheckDSLCallsKnown(m.prog, m.label, projProcs)...)
	}

	return issues, warnings
}

// CheckQueries compiles every query in widgets and reports. Compilation needs
// metadata about registers, so this runs after the project has loaded.
func CheckQueries(proj *project.Project) []Issue {
	var issues []Issue
	opts := query.CompileOpts{
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Entities:    proj.Entities,
	}
	for _, w := range proj.Widgets {
		if w.Query == "" {
			continue
		}
		params := map[string]any{}
		for k := range w.Params {
			params[k] = nil // placeholder so &Param doesn't fail name resolution
		}
		o := opts
		o.Params = params
		if _, err := query.Compile(w.Query, o); err != nil {
			issues = append(issues, Issue{
				File:    "widgets/" + w.Name + ".yaml",
				Object:  w.Name,
				Kind:    "Виджет (запрос)",
				Message: err.Error(),
			})
		}
	}
	for _, rep := range proj.Reports {
		if rep.Query == "" {
			continue
		}
		params := map[string]any{}
		for _, p := range rep.Params {
			params[p.Name] = nil
		}
		o := opts
		o.Params = params
		if _, err := query.Compile(rep.Query, o); err != nil {
			issues = append(issues, Issue{
				File:    "reports/" + rep.Name + ".yaml",
				Object:  rep.Name,
				Kind:    "Отчёт (запрос)",
				Message: err.Error(),
			})
		}
	}
	return issues
}

// CheckQueriesExecutable компилирует каждый запрос виджета/отчёта под SQLite и
// валидирует его через validate (PREPARE против in-memory схемы из метаданных).
// Это ловит класс «компилируется, но не исполняется» — no such table (VT без
// скобок), ambiguous column (авто-JOIN), неизвестные ключевые слова — которые
// CheckQueries (только компиляция) пропускает. validate(sql) возвращает ошибку
// prepare или nil. Компиляционные ошибки здесь не дублируются (их репортит
// CheckQueries) — запрос с ошибкой компиляции просто пропускается.
func CheckQueriesExecutable(proj *project.Project, validate func(sqlText string) error) []Issue {
	var issues []Issue
	opts := query.CompileOpts{
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Entities:    proj.Entities,
		Dialect:     storage.SQLiteDialect{},
	}
	check := func(qtext, file, name, kind string, params map[string]any) {
		if qtext == "" {
			return
		}
		o := opts
		o.Params = params
		r, err := query.Compile(qtext, o)
		if err != nil {
			return // ошибки компиляции уже репортит CheckQueries
		}
		if verr := validate(r.SQL); verr != nil {
			issues = append(issues, Issue{File: file, Object: name, Kind: kind, Message: verr.Error()})
		}
	}
	for _, w := range proj.Widgets {
		params := map[string]any{}
		for k := range w.Params {
			params[k] = nil
		}
		check(w.Query, "widgets/"+w.Name+".yaml", w.Name, "Виджет (исполнение запроса)", params)
	}
	for _, rep := range proj.Reports {
		params := map[string]any{}
		for _, p := range rep.Params {
			params[p.Name] = nil
		}
		check(rep.Query, "reports/"+rep.Name+".yaml", rep.Name, "Отчёт (исполнение запроса)", params)
	}
	return issues
}

// ParseDSL runs the lexer and parser on a snippet, then validates function
// calls against known builtins + procedures defined in the same module.
// parseErrLocRe вытаскивает "<…>:<line>:<col>: <сообщение>" из ошибки
// лексера/парсера. Координаты кладём в поля Issue (а не только в текст), чтобы
// в конфигураторе по ошибке можно было кликнуть и перейти к месту (issue #103).
var parseErrLocRe = regexp.MustCompile(`:(\d+):(\d+): (.*)$`)

func parseErrIssue(label, msg string) Issue {
	if m := parseErrLocRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return Issue{File: label, Line: line, Column: col, Message: m[3]}
	}
	return Issue{File: label, Message: msg}
}

func ParseDSL(source, label string) []Issue {
	l := lexer.New(source, label)
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		return []Issue{parseErrIssue(label, err.Error())}
	}
	return CheckDSLCalls(prog, label)
}

// CheckDSLCalls walks the AST and reports calls to undefined functions.
// Known = builtins + procedures declared in the same module.
func CheckDSLCalls(prog *ast.Program, label string) []Issue {
	return CheckDSLCallsKnown(prog, label, nil)
}

// CheckDSLCallsKnown is like CheckDSLCalls but also treats the names in extra
// as known — used by CheckDir to recognise procedures declared in OTHER modules
// (common modules, utilities), avoiding false "unknown function" reports.
func CheckDSLCallsKnown(prog *ast.Program, label string, extra map[string]struct{}) []Issue {
	known := interpreter.KnownBuiltinNames()
	for _, pr := range prog.Procedures {
		known[strings.ToLower(pr.Name.Literal)] = struct{}{}
	}
	for k := range extra {
		known[k] = struct{}{}
	}
	var issues []Issue
	for _, pr := range prog.Procedures {
		walkStmts(pr.Body, known, label, &issues)
	}
	return issues
}

// CheckDSLSource validates ad-hoc module text (configurator editor).
func CheckDSLSource(source, name string) []Issue {
	issues := ParseDSL(source, name)
	for i := range issues {
		issues[i].Kind = "DSL модуль"
		issues[i].Object = name
	}
	return issues
}

// CheckWidgetYAML validates ad-hoc widget YAML text.
func CheckWidgetYAML(source, name string) []Issue {
	tmp, err := os.CreateTemp("", "widget-check-*.yaml")
	if err != nil {
		return []Issue{{Message: err.Error()}}
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(source)
	tmp.Close()
	if _, err := metadata.LoadWidgetFile(tmp.Name()); err != nil {
		return []Issue{{Kind: "Виджет", Object: name, Message: err.Error()}}
	}
	return nil
}

// CheckHomePageYAML validates ad-hoc home-page YAML text.
func CheckHomePageYAML(source string) []Issue {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	tmp, err := os.CreateTemp("", "homepage-check-*.yaml")
	if err != nil {
		return []Issue{{Message: err.Error()}}
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(source)
	tmp.Close()
	if _, err := metadata.LoadHomePage(tmp.Name()); err != nil {
		return []Issue{{Kind: "Главная страница", Message: err.Error()}}
	}
	return nil
}

// CheckEntityYAML validates a free-form catalog/document YAML. The kind is
// unknown, so both are tried — passing if either parses cleanly.
func CheckEntityYAML(source, name string) []Issue {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	tmp, err := os.CreateTemp("", "entity-check-*.yaml")
	if err != nil {
		return []Issue{{Message: err.Error()}}
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(source)
	tmp.Close()
	if _, err1 := metadata.LoadFile(tmp.Name(), metadata.KindCatalog); err1 == nil {
		return nil
	}
	if _, err2 := metadata.LoadFile(tmp.Name(), metadata.KindDocument); err2 == nil {
		return nil
	}
	_, e := metadata.LoadFile(tmp.Name(), metadata.KindCatalog)
	return []Issue{{Kind: "Объект метаданных", Object: name, Message: e.Error()}}
}

// unsupportedProcessorKeys возвращает имена ключей верхнего уровня YAML
// обработки, которые платформа не поддерживает (wizard, steps). Их наличие —
// признак того, что разработчик ожидает многошаговый мастер, которого в OneBase
// нет; ключи нужно ловить отдельно, т.к. yaml.v3 молча отбрасывает неизвестные
// поля структуры Processor.
func unsupportedProcessorKeys(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil
	}
	var found []string
	for _, key := range []string{"wizard", "steps"} {
		if _, ok := top[key]; ok {
			found = append(found, key)
		}
	}
	return found
}

// CheckReportComposition валидирует структуру блока composition отчётов
// (без выполнения запроса). Проверка полей против колонок запроса — Stage 3.
func CheckReportComposition(proj *project.Project) []Issue {
	var issues []Issue
	aggs := map[string]bool{"": true, "sum": true, "count": true, "avg": true, "min": true, "max": true}
	dirs := map[string]bool{"": true, "asc": true, "desc": true}
	ctypes := map[string]bool{"bar": true, "line": true, "pie": true}
	aligns := map[string]bool{"": true, "left": true, "right": true, "center": true}
	add := func(name, msg string) {
		issues = append(issues, Issue{File: "reports/" + name + ".yaml", Object: name, Kind: "Отчёт (компоновка)", Message: msg})
	}
	// checkComp валидирует одну компоновку. prefix непустой для вариантов
	// («вариант "Имя": ») — чтобы в сообщении было видно, где именно ошибка.
	checkComp := func(repName, prefix string, c *report.Composition) {
		if c == nil {
			return
		}
		groups := map[string]bool{}
		for _, g := range c.Groupings {
			groups[g] = true
		}
		measures := map[string]bool{}
		for _, m := range c.Measures {
			measures[m.Field] = true
			if m.Expr != "" {
				// Вычисляемый показатель: агрегат необязателен, но выражение валидируем.
				if err := parseReturnExpr(m.Expr); err != nil {
					add(repName, prefix+"ошибка выражения показателя \""+m.Field+"\": "+err.Error())
				}
			} else if !aggs[m.Agg] {
				add(repName, prefix+"неизвестный агрегат: "+m.Agg)
			}
			if !aligns[m.Align] {
				add(repName, prefix+"неизвестное выравнивание: "+m.Align)
			}
		}
		for _, s := range c.Sort {
			if !dirs[s.Dir] {
				add(repName, prefix+"неизвестное направление сортировки: "+s.Dir)
			}
			if !groups[s.Field] && !measures[s.Field] {
				add(repName, prefix+"поле сортировки не группировка и не показатель: "+s.Field)
			}
		}
		if c.Chart != nil {
			if !ctypes[c.Chart.Type] {
				add(repName, prefix+"неизвестный тип графика: "+c.Chart.Type)
			}
			if !groups[c.Chart.Category] {
				add(repName, prefix+"категория графика не входит в группировки: "+c.Chart.Category)
			}
			for _, sname := range c.Chart.Series {
				if !measures[sname] {
					add(repName, prefix+"ряд графика не входит в показатели: "+sname)
				}
			}
		}
		for _, cr := range c.Conditional {
			if err := parseReturnExpr(cr.When); err != nil {
				add(repName, prefix+"ошибка выражения условия \""+cr.When+"\": "+err.Error())
			}
		}
		// Кросс-таблица (columns): измерения уходят в колонки. Детализация в этом
		// режиме не выводится; поле-колонка не должно дублировать строковое
		// измерение или показатель.
		if len(c.Columns) > 0 {
			if c.Detail {
				add(repName, prefix+"детализация (detail) несовместима с кросс-таблицей (columns) и будет проигнорирована")
			}
			for _, col := range c.Columns {
				if groups[col] {
					add(repName, prefix+"поле \""+col+"\" указано и в группировках, и в колонках кросс-таблицы")
				}
				if measures[col] {
					add(repName, prefix+"поле \""+col+"\" указано и в показателях, и в колонках кросс-таблицы")
				}
			}
		}
	}
	for _, rep := range proj.Reports {
		checkComp(rep.Name, "", rep.Composition)
		for _, v := range rep.Variants {
			checkComp(rep.Name, "вариант \""+v.Name+"\": ", v.Composition)
		}
	}
	return issues
}

// CheckJournalConditional валидирует DSL-выражения условного оформления журналов.
func CheckJournalConditional(proj *project.Project) []Issue {
	var issues []Issue
	add := func(name, msg string) {
		issues = append(issues, Issue{File: "journals/" + name + ".yaml", Object: name, Kind: "Журнал", Message: msg})
	}
	for _, j := range proj.Journals {
		for _, cr := range j.Conditional {
			if err := parseReturnExpr(cr.When); err != nil {
				add(j.Name, "ошибка выражения условия \""+cr.When+"\": "+err.Error())
			}
		}
	}
	return issues
}

// CheckFormConditional валидирует DSL-выражения условного оформления форм.
func CheckFormConditional(proj *project.Project) []Issue {
	var issues []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			if form == nil || !form.IsManaged() {
				continue
			}
			for _, cr := range form.Conditional {
				if err := parseReturnExpr(cr.When); err != nil {
					issues = append(issues, Issue{
						File:    formFileLabel(ent, form),
						Object:  form.Name,
						Kind:    "Управляемая форма",
						Message: "ошибка выражения условия \"" + cr.When + "\": " + err.Error(),
					})
				}
			}
		}
	}
	return issues
}

func parseReturnExpr(expr string) error {
	src := "Функция __cond()\nВозврат (" + expr + ");\nКонецФункции\n"
	_, err := parser.New(lexer.New(src, "cond.os")).ParseProgram()
	return err
}

// AlreadyReported reports whether msg overlaps an existing issue message.
func AlreadyReported(issues []Issue, msg string) bool {
	for _, i := range issues {
		if strings.Contains(msg, i.Message) || strings.Contains(i.Message, msg) {
			return true
		}
	}
	return false
}

func walkStmts(stmts []ast.Stmt, known map[string]struct{}, label string, issues *[]Issue) {
	for _, s := range stmts {
		walkStmt(s, known, label, issues)
	}
}

func walkStmt(s ast.Stmt, known map[string]struct{}, label string, issues *[]Issue) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		if ident, ok := v.X.(*ast.Ident); ok {
			*issues = append(*issues, Issue{
				File:    label,
				Line:    ident.Tok.Line,
				Column:  ident.Tok.Col,
				Message: fmt.Sprintf("выражение без эффекта: «%s» (возможно, пропущены скобки вызова?)", ident.Tok.Literal),
			})
		}
		walkExpr(v.X, known, label, issues)
	case *ast.AssignStmt:
		walkExpr(v.Value, known, label, issues)
	case *ast.ReturnStmt:
		if v.Value != nil {
			walkExpr(v.Value, known, label, issues)
		}
	case *ast.IfStmt:
		walkExpr(v.Cond, known, label, issues)
		walkStmts(v.Then, known, label, issues)
		for _, ei := range v.ElseIfs {
			walkExpr(ei.Cond, known, label, issues)
			walkStmts(ei.Body, known, label, issues)
		}
		walkStmts(v.Else, known, label, issues)
	case *ast.ForEachStmt:
		walkExpr(v.Collection, known, label, issues)
		walkStmts(v.Body, known, label, issues)
	case *ast.NumericForStmt:
		walkExpr(v.Start, known, label, issues)
		walkExpr(v.End, known, label, issues)
		walkStmts(v.Body, known, label, issues)
	case *ast.TryStmt:
		walkStmts(v.Try, known, label, issues)
		walkStmts(v.Except, known, label, issues)
	}
}

func walkExpr(e ast.Expr, known map[string]struct{}, label string, issues *[]Issue) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.CallExpr:
		if ident, ok := v.Callee.(*ast.Ident); ok {
			name := strings.ToLower(ident.Tok.Literal)
			if _, found := known[name]; !found {
				*issues = append(*issues, Issue{
					File:    label,
					Line:    ident.Tok.Line,
					Column:  ident.Tok.Col,
					Message: fmt.Sprintf("неизвестная функция %q", ident.Tok.Literal),
				})
			}
		}
		walkExpr(v.Callee, known, label, issues)
		for _, arg := range v.Args {
			walkExpr(arg, known, label, issues)
		}
	case *ast.BinaryExpr:
		walkExpr(v.Left, known, label, issues)
		walkExpr(v.Right, known, label, issues)
	case *ast.UnaryExpr:
		walkExpr(v.Operand, known, label, issues)
	case *ast.MemberExpr:
		walkExpr(v.Object, known, label, issues)
	case *ast.IndexExpr:
		walkExpr(v.Object, known, label, issues)
		walkExpr(v.Index, known, label, issues)
	case *ast.TernaryExpr:
		walkExpr(v.Cond, known, label, issues)
		walkExpr(v.True, known, label, issues)
		walkExpr(v.False, known, label, issues)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			walkExpr(arg, known, label, issues)
		}
	}
}
