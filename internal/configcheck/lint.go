package configcheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"gopkg.in/yaml.v3"
)

// CheckLintYAML reports YAML keys that are accepted by yaml.v3 but ignored by
// the metadata loaders. These are warnings: they do not make a configuration
// invalid, but they almost always mean a typo or an expectation the platform
// currently does not implement.
func CheckLintYAML(dir string) []Issue {
	var issues []Issue
	for _, spec := range yamlLintSpecs() {
		root := filepath.Join(dir, spec.dir)
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
				continue
			}
			label := filepath.ToSlash(filepath.Join(spec.dir, e.Name()))
			issues = append(issues, lintYAMLFile(filepath.Join(root, e.Name()), label, spec.kind, spec.schema)...)
		}
	}

	issues = append(issues,
		lintYAMLFile(filepath.Join(dir, "config", "home_page.yaml"), "config/home_page.yaml", "Главная страница", homePageYAMLSchema())...)

	formsRoot := filepath.Join(dir, "forms")
	filepath.WalkDir(formsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".form.yaml") {
			return nil
		}
		label := relLabel(dir, path)
		issues = append(issues, lintYAMLFile(path, label, "Управляемая форма", formModuleYAMLSchema())...)
		return nil
	})

	return issues
}

// CheckLintProject reports advisory checks that require a successfully loaded
// project: DSL usage/reachability and role coverage.
func CheckLintProject(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	var issues []Issue
	issues = append(issues, CheckLintDSL(dir, proj)...)
	issues = append(issues, CheckLintRoles(proj, roles)...)
	return issues
}

type yamlLintSpec struct {
	dir    string
	kind   string
	schema *yamlLintSchema
}

type yamlLintSchema struct {
	keys map[string]*yamlLintSchema
	elem *yamlLintSchema
	free bool
}

func yamlLintSpecs() []yamlLintSpec {
	return []yamlLintSpec{
		{"catalogs", "Справочник", entityYAMLSchema()},
		{"documents", "Документ", entityYAMLSchema()},
		{"registers", "Регистр", registerYAMLSchema()},
		{"inforegs", "Регистр сведений", infoRegisterYAMLSchema()},
		{"enums", "Перечисление", enumYAMLSchema()},
		{"constants", "Константы", constantsYAMLSchema()},
		{"widgets", "Виджет", widgetYAMLSchema()},
		{"reports", "Отчёт", reportYAMLSchema()},
		{"roles", "Роль", roleYAMLSchema()},
		{"processors", "Обработка", processorYAMLSchema()},
		{"services", "HTTP-сервис", serviceYAMLSchema()},
		{"pages", "Страница", pageYAMLSchema()},
		{"journals", "Журнал", journalYAMLSchema()},
		{"subsystems", "Подсистема", subsystemYAMLSchema()},
		{"scheduled", "Регламентное задание", scheduledYAMLSchema()},
		{"accounts", "План счетов", accountsYAMLSchema()},
		{"accountregs", "Регистр бухгалтерии", accountRegisterYAMLSchema()},
	}
}

func lintYAMLFile(path, label, kind string, schema *yamlLintSchema) []Issue {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // YAML syntax errors are already reported by normal check.
	}
	if len(doc.Content) == 0 {
		return nil
	}
	var issues []Issue
	lintYAMLNode(label, kind, "", doc.Content[0], schema, &issues)
	return issues
}

func lintYAMLNode(label, kind, path string, node *yaml.Node, schema *yamlLintSchema, issues *[]Issue) {
	if node == nil || schema == nil || schema.free {
		return
	}
	if schema.elem != nil {
		if node.Kind != yaml.SequenceNode {
			return
		}
		nextPath := path + "[]"
		for _, item := range node.Content {
			lintYAMLNode(label, kind, nextPath, item, schema.elem, issues)
		}
		return
	}
	if node.Kind != yaml.MappingNode || len(schema.keys) == 0 {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		key := keyNode.Value
		child, ok := schema.keys[key]
		nextPath := key
		if path != "" {
			nextPath = path + "." + key
		}
		if !ok {
			*issues = append(*issues, Issue{
				File:         label,
				Kind:         kind,
				Code:         "metadata.unvalidated-key",
				Line:         keyNode.Line,
				Column:       keyNode.Column,
				Message:      fmt.Sprintf("неизвестный YAML-ключ %q: загрузчик его игнорирует", nextPath),
				SuggestedFix: "Удалите ключ, исправьте опечатку или добавьте поддержку этого поля в загрузчик метаданных.",
			})
			continue
		}
		lintYAMLNode(label, kind, nextPath, valueNode, child, issues)
	}
}

func obj(keys ...string) *yamlLintSchema {
	m := make(map[string]*yamlLintSchema, len(keys))
	for _, k := range keys {
		m[k] = nil
	}
	return &yamlLintSchema{keys: m}
}

func seq(elem *yamlLintSchema) *yamlLintSchema {
	return &yamlLintSchema{elem: elem}
}

func freeMap() *yamlLintSchema {
	return &yamlLintSchema{free: true}
}

func with(base *yamlLintSchema, nested map[string]*yamlLintSchema) *yamlLintSchema {
	for k, v := range nested {
		base.keys[k] = v
	}
	return base
}

func fieldYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "type", "allow_inline_create"), map[string]*yamlLintSchema{
		"titles": freeMap(),
	})
}

func tablePartYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"fields": seq(fieldYAMLSchema()),
	})
}

func entityYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "posting", "hierarchical", "hierarchy_kind", "list_form", "item_form", "based_on", "list_mode"), map[string]*yamlLintSchema{
		"titles":     freeMap(),
		"fields":     seq(fieldYAMLSchema()),
		"tableparts": seq(tablePartYAMLSchema()),
		"numerator":  obj("prefix", "length", "period", "scope"),
		"predefined": seq(with(obj("name"), map[string]*yamlLintSchema{"fields": freeMap()})),
		"tile_view":  obj("image", "title", "subtitle", "fields"),
		"activity":   obj("field", "default_scope", "hide_from_choice"),
	})
}

func registerYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "kind"), map[string]*yamlLintSchema{
		"titles":     freeMap(),
		"dimensions": seq(fieldYAMLSchema()),
		"resources":  seq(fieldYAMLSchema()),
		"attributes": seq(fieldYAMLSchema()),
	})
}

func infoRegisterYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "periodic"), map[string]*yamlLintSchema{
		"titles":     freeMap(),
		"dimensions": seq(fieldYAMLSchema()),
		"resources":  seq(fieldYAMLSchema()),
	})
}

func enumYAMLSchema() *yamlLintSchema {
	return with(obj("name"), map[string]*yamlLintSchema{
		"values": seq(with(obj("name"), map[string]*yamlLintSchema{"titles": freeMap()})),
	})
}

func constantsYAMLSchema() *yamlLintSchema {
	return with(obj(), map[string]*yamlLintSchema{
		"constants": seq(with(obj("name", "type", "default", "label"), map[string]*yamlLintSchema{"labels": freeMap()})),
	})
}

func widgetYAMLSchema() *yamlLintSchema {
	return with(obj("name", "type", "title", "query", "format", "compare_to", "limit", "chart_kind", "x_field", "y_fields", "entities", "scope"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"params": freeMap(),
		"columns": seq(with(obj("field", "label", "format", "align"), map[string]*yamlLintSchema{
			"labels": freeMap(),
		})),
		"items": seq(with(obj("label", "entity", "url"), map[string]*yamlLintSchema{
			"labels": freeMap(),
		})),
	})
}

func reportYAMLSchema() *yamlLintSchema {
	measure := obj("field", "agg", "title", "align", "format", "expr")
	sortKey := obj("field", "dir")
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "field"), map[string]*yamlLintSchema{"style": style})
	composition := with(obj("groupings", "columns", "detail", "detail_link", "detail_entity"), map[string]*yamlLintSchema{
		"measures":    seq(measure),
		"totals":      obj("grand", "subtotals"),
		"sort":        seq(sortKey),
		"conditional": seq(conditional),
		"appearance":  obj("lines", "zebra"),
		"chart":       obj("type", "category", "series"),
	})
	return with(obj("name", "title", "query", "chart_proc", "output_format"), map[string]*yamlLintSchema{
		"titles":      freeMap(),
		"params":      seq(with(obj("name", "type", "label", "options"), map[string]*yamlLintSchema{"labels": freeMap()})),
		"composition": composition,
		"variants":    seq(with(obj("name"), map[string]*yamlLintSchema{"composition": composition})),
	})
}

func roleYAMLSchema() *yamlLintSchema {
	perm := with(obj(), map[string]*yamlLintSchema{
		"catalogs":   freeMap(),
		"documents":  freeMap(),
		"registers":  freeMap(),
		"inforegs":   freeMap(),
		"reports":    freeMap(),
		"processors": freeMap(),
	})
	return with(obj("name", "description"), map[string]*yamlLintSchema{"permissions": perm})
}

func processorYAMLSchema() *yamlLintSchema {
	param := with(obj("name", "type", "label", "default", "options"), map[string]*yamlLintSchema{"labels": freeMap()})
	return with(obj("name", "title"), map[string]*yamlLintSchema{
		"titles":      freeMap(),
		"params":      seq(param),
		"table_parts": seq(tablePartYAMLSchema()),
	})
}

func serviceYAMLSchema() *yamlLintSchema {
	cors := obj("origins", "headers", "credentials", "max_age")
	template := with(obj("template"), map[string]*yamlLintSchema{"methods": freeMap()})
	return with(obj("name", "title", "root_url", "auth", "secret", "rate_limit", "roles"), map[string]*yamlLintSchema{
		"titles":    freeMap(),
		"cors":      cors,
		"templates": seq(template),
	})
}

func pageYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "icon", "roles", "params"), map[string]*yamlLintSchema{"titles": freeMap()})
}

func journalYAMLSchema() *yamlLintSchema {
	column := with(obj("field", "label", "fallback", "format"), map[string]*yamlLintSchema{
		"labels": freeMap(),
		"map":    freeMap(),
	})
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "field"), map[string]*yamlLintSchema{
		"style": style,
		"then":  style,
	})
	return with(obj("name", "title", "documents"), map[string]*yamlLintSchema{
		"titles":                 freeMap(),
		"columns":                seq(column),
		"filters":                seq(obj("field", "type")),
		"conditional":            seq(conditional),
		"conditional_formatting": seq(conditional),
	})
}

func subsystemYAMLSchema() *yamlLintSchema {
	contents := obj("documents", "catalogs", "reports", "inforegs", "registers", "processors", "journals", "pages")
	return with(obj("name", "title", "icon", "order"), map[string]*yamlLintSchema{
		"titles":    freeMap(),
		"contents":  contents,
		"home_page": homePageYAMLSchema(),
	})
}

func scheduledYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "schedule", "processor", "enabled", "on_error", "timeout"), map[string]*yamlLintSchema{
		"titles": freeMap(),
		"params": freeMap(),
	})
}

func accountsYAMLSchema() *yamlLintSchema {
	account := with(obj("code", "name", "kind", "parent"), map[string]*yamlLintSchema{"names": freeMap()})
	return with(obj("name", "title"), map[string]*yamlLintSchema{
		"titles":   freeMap(),
		"accounts": seq(account),
	})
}

func accountRegisterYAMLSchema() *yamlLintSchema {
	return with(obj("name", "title", "accounts"), map[string]*yamlLintSchema{
		"titles":    freeMap(),
		"resources": seq(fieldYAMLSchema()),
		"subconto":  seq(fieldYAMLSchema()),
	})
}

func homePageYAMLSchema() *yamlLintSchema {
	nav := obj("documents", "catalogs", "reports", "inforegs", "registers", "processors", "journals", "pages")
	return with(obj("title", "layout"), map[string]*yamlLintSchema{
		"titles":  freeMap(),
		"rows":    seq(obj("widgets")),
		"widgets": seq(obj("name", "span")),
		"nav":     nav,
	})
}

func formModuleYAMLSchema() *yamlLintSchema {
	element := &yamlLintSchema{}
	element.keys = map[string]*yamlLintSchema{}
	for _, k := range []string{
		"id", "name", "kind", "field", "table_part", "visible", "enabled", "required",
		"original_id", "data_path", "picture", "values_picture", "width", "height",
		"halign", "valign", "readonly", "use_grid", "no_grid", "auto_sum", "hint", "mask",
		"format", "display_format", "type", "choice", "unknown_xml", "view",
	} {
		element.keys[k] = nil
	}
	element.keys["title"] = freeMap()
	element.keys["events"] = freeMap()
	element.keys["props"] = freeMap()
	element.keys["children"] = seq(element)
	element.keys["choices"] = seq(with(obj("value"), map[string]*yamlLintSchema{"title": freeMap()}))
	element.keys["options"] = seq(with(obj("value"), map[string]*yamlLintSchema{"label": freeMap()}))

	attrColumn := with(obj("id", "original_id", "name", "type", "length", "precision"), map[string]*yamlLintSchema{
		"title": freeMap(),
		"props": freeMap(),
	})
	attr := with(obj("id", "original_id", "name", "type", "length", "precision", "allowed_length", "save", "filling_value", "main"), map[string]*yamlLintSchema{
		"title":   freeMap(),
		"columns": seq(attrColumn),
		"props":   freeMap(),
	})
	command := with(obj("id", "original_id", "name", "group", "picture", "action"), map[string]*yamlLintSchema{
		"title": freeMap(),
		"props": freeMap(),
	})
	button := with(obj("id", "original_id", "name", "command", "representation", "picture"), map[string]*yamlLintSchema{
		"title": freeMap(),
	})
	commandBar := obj("id", "original_id", "name", "visible")
	commandBar.keys["buttons"] = seq(button)

	formHeader := with(obj("entity", "name", "kind", "original_id", "auto_save_settings", "auto_save_data_in_settings", "vertical_scroll"), map[string]*yamlLintSchema{
		"title": freeMap(),
	})
	style := obj("color", "background", "bold", "italic")
	conditional := with(obj("when", "target", "element", "table_part", "field"), map[string]*yamlLintSchema{
		"style": style,
		"then":  style,
	})

	return with(obj("schema", "entity", "name", "kind", "layout_kind", "original_id", "auto_save_settings", "auto_save_data_in_settings", "vertical_scroll"), map[string]*yamlLintSchema{
		"form":                   formHeader,
		"title":                  freeMap(),
		"events":                 freeMap(),
		"elements":               seq(element),
		"actions":                freeMap(),
		"attributes":             seq(attr),
		"commands":               seq(command),
		"command_bar":            commandBar,
		"conditional":            seq(conditional),
		"conditional_formatting": seq(conditional),
		"oneC_meta":              freeMap(),
	})
}

type lintProgram struct {
	label   string
	object  string
	kind    string
	prog    *ast.Program
	roots   map[string]bool
	rootAll bool
}

// CheckLintDSL reports declared but unread DSL variables and procedures that
// are unreachable from known runtime entry points.
func CheckLintDSL(dir string, proj *project.Project) []Issue {
	programs := collectLintPrograms(dir, proj)
	var issues []Issue
	for _, lp := range programs {
		issues = append(issues, lintUnusedVars(lp)...)
		issues = append(issues, lintCrossScopeReads(lp)...)
	}
	issues = append(issues, lintDeadProcedures(programs)...)
	return issues
}

// commonDSLGlobals — инжектируемые объекты-значения, доступные в любом модуле без
// объявления (dslvars.Common.Build + контекстные переменные форм/заданий). Они
// читаются как значения, поэтому попадают в reads; исключаем, чтобы не спутать с
// переменными. Builtins-функции в список не нужны: вызов callee не считается
// чтением (см. collectReadIdentTokensExpr).
var commonDSLGlobals = map[string]bool{
	"документы": true, "documents": true,
	"справочники": true, "catalogs": true,
	"перечисления": true, "enums": true,
	"константы": true, "constants": true,
	"движения": true, "movements": true,
	"запрос": true, "query": true,
	"предопределённыезначения": true, "предопределенныезначения": true, "predefinedvalues": true,
	"регистрынакопления": true, "регистрысведений": true, "регистрыбухгалтерии": true,
	"планысчетов": true, "планывидовхарактеристик": true,
	"ссылканаобъект": true, "objectref": true,
	"этотобъект": true, "this": true,
	// Контекст форм/страниц/заданий/сервисов.
	"объект": true, "форма": true, "элементы": true, "элементыформы": true,
	"отказ": true, "параметры": true, "параметрысеанса": true, "запрос_": true,
}

// lintCrossScopeReads помечает чтение идентификатора, который процедура не
// объявляла локально, но который является локальной переменной (параметром,
// Перем, переменной цикла или целью присваивания) ДРУГОЙ процедуры того же
// модуля. Такое чтение сегодня резолвится только потому, что окружение вызванной
// процедуры сцеплено с окружением вызывающей (динамическая видимость чтения):
// код хрупкий и сломается при корректной лексической изоляции (см. план изоляции
// scope). Это предупреждение — рантайм не меняется.
func lintCrossScopeReads(lp lintProgram) []Issue {
	if lp.prog == nil || len(lp.prog.Procedures) < 2 {
		return nil
	}
	moduleVars := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			moduleVars[strings.ToLower(tok.Literal)] = true
		}
	}
	procNames := map[string]bool{}
	for _, pr := range lp.prog.Procedures {
		procNames[strings.ToLower(pr.Name.Literal)] = true
	}
	// procLocals[i] — имена, «принадлежащие» i-й процедуре; ownerCount — в
	// скольких процедурах имя объявлено/присвоено.
	procLocals := make([]map[string]bool, len(lp.prog.Procedures))
	ownerCount := map[string]int{}
	for i, pr := range lp.prog.Procedures {
		ls := map[string]bool{}
		for _, p := range pr.Params {
			ls[strings.ToLower(p.Literal)] = true
		}
		collectDeclaredAndAssigned(pr.Body, ls)
		procLocals[i] = ls
		for name := range ls {
			ownerCount[name]++
		}
	}
	var issues []Issue
	for i, pr := range lp.prog.Procedures {
		reads := map[string]token.Token{}
		for _, def := range pr.Defaults {
			collectReadIdentTokensExpr(def, reads)
		}
		collectReadIdentTokensStmts(pr.Body, reads)
		names := make([]string, 0, len(reads))
		for name := range reads {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if procLocals[i][name] || moduleVars[name] || procNames[name] || commonDSLGlobals[name] {
				continue
			}
			// Имя — локальная другой процедуры (текущая исключена проверкой выше).
			if ownerCount[name] > 0 {
				issues = append(issues, crossScopeReadIssue(lp, reads[name]))
			}
		}
	}
	return issues
}

func crossScopeReadIssue(lp lintProgram, tok token.Token) Issue {
	return Issue{
		File:         sourceLabelForToken(lp.label, tok),
		Object:       lp.object,
		Kind:         lp.kind,
		Code:         "dsl.cross-scope-read",
		Line:         tok.Line,
		Column:       tok.Col,
		Message:      fmt.Sprintf("переменная %q не объявлена в этой процедуре и является локальной другой процедуры модуля — чтение работает лишь из-за утечки области видимости вызова", tok.Literal),
		SuggestedFix: "Передайте значение параметром/результатом функции или объявите переменную локально; не полагайтесь на видимость переменных вызывающей процедуры.",
	}
}

// collectDeclaredAndAssigned собирает имена, «принадлежащие» процедуре: Перем,
// переменные циклов и цели простых присваиваний (Ident = ...).
func collectDeclaredAndAssigned(stmts []ast.Stmt, out map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.VarDecl:
			for _, tok := range v.Names {
				out[strings.ToLower(tok.Literal)] = true
			}
		case *ast.AssignStmt:
			if id, ok := v.Target.(*ast.Ident); ok {
				out[strings.ToLower(id.Tok.Literal)] = true
			}
		case *ast.IfStmt:
			collectDeclaredAndAssigned(v.Then, out)
			for _, ei := range v.ElseIfs {
				collectDeclaredAndAssigned(ei.Body, out)
			}
			collectDeclaredAndAssigned(v.Else, out)
		case *ast.ForEachStmt:
			out[strings.ToLower(v.Var.Literal)] = true
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.NumericForStmt:
			out[strings.ToLower(v.Var.Literal)] = true
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.WhileStmt:
			collectDeclaredAndAssigned(v.Body, out)
		case *ast.TryStmt:
			collectDeclaredAndAssigned(v.Try, out)
			collectDeclaredAndAssigned(v.Except, out)
		}
	}
}

// collectReadIdentTokensStmts/Expr — как collectDSLReadsStmts, но сохраняет токен
// первого чтения каждого имени (для точной локации предупреждения). Цель
// присваивания Ident не считается чтением; callee прямого вызова — тоже.
func collectReadIdentTokensStmts(stmts []ast.Stmt, out map[string]token.Token) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectReadIdentTokensExpr(v.X, out)
		case *ast.AssignStmt:
			if v.Op == token.ASSIGN {
				collectReadIdentTokensTarget(v.Target, out)
			} else {
				collectReadIdentTokensExpr(v.Target, out)
			}
			collectReadIdentTokensExpr(v.Value, out)
		case *ast.ReturnStmt:
			collectReadIdentTokensExpr(v.Value, out)
		case *ast.IfStmt:
			collectReadIdentTokensExpr(v.Cond, out)
			collectReadIdentTokensStmts(v.Then, out)
			for _, ei := range v.ElseIfs {
				collectReadIdentTokensExpr(ei.Cond, out)
				collectReadIdentTokensStmts(ei.Body, out)
			}
			collectReadIdentTokensStmts(v.Else, out)
		case *ast.ForEachStmt:
			collectReadIdentTokensExpr(v.Collection, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.NumericForStmt:
			collectReadIdentTokensExpr(v.Start, out)
			collectReadIdentTokensExpr(v.End, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.WhileStmt:
			collectReadIdentTokensExpr(v.Cond, out)
			collectReadIdentTokensStmts(v.Body, out)
		case *ast.TryStmt:
			collectReadIdentTokensStmts(v.Try, out)
			collectReadIdentTokensStmts(v.Except, out)
		}
	}
}

func collectReadIdentTokensTarget(expr ast.Expr, out map[string]token.Token) {
	switch v := expr.(type) {
	case *ast.Ident:
		return
	case *ast.MemberExpr:
		collectReadIdentTokensExpr(v.Object, out)
	case *ast.IndexExpr:
		collectReadIdentTokensExpr(v.Object, out)
		collectReadIdentTokensExpr(v.Index, out)
	default:
		collectReadIdentTokensExpr(expr, out)
	}
}

func collectReadIdentTokensExpr(expr ast.Expr, out map[string]token.Token) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.Ident:
		if k := strings.ToLower(v.Tok.Literal); k != "" {
			if _, ok := out[k]; !ok {
				out[k] = v.Tok
			}
		}
	case *ast.CallExpr:
		if _, ok := v.Callee.(*ast.Ident); !ok {
			collectReadIdentTokensExpr(v.Callee, out)
		}
		for _, arg := range v.Args {
			collectReadIdentTokensExpr(arg, out)
		}
	case *ast.MemberExpr:
		collectReadIdentTokensExpr(v.Object, out)
	case *ast.BinaryExpr:
		collectReadIdentTokensExpr(v.Left, out)
		collectReadIdentTokensExpr(v.Right, out)
	case *ast.UnaryExpr:
		collectReadIdentTokensExpr(v.Operand, out)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectReadIdentTokensExpr(arg, out)
		}
	case *ast.IndexExpr:
		collectReadIdentTokensExpr(v.Object, out)
		collectReadIdentTokensExpr(v.Index, out)
	case *ast.TernaryExpr:
		collectReadIdentTokensExpr(v.Cond, out)
		collectReadIdentTokensExpr(v.True, out)
		collectReadIdentTokensExpr(v.False, out)
	}
}

func collectLintPrograms(dir string, proj *project.Project) []lintProgram {
	var out []lintProgram
	add := func(object, kind string, prog *ast.Program, roots map[string]bool, rootAll bool) {
		if prog == nil {
			return
		}
		out = append(out, lintProgram{
			label:   programLabel(dir, prog),
			object:  object,
			kind:    kind,
			prog:    prog,
			roots:   roots,
			rootAll: rootAll,
		})
	}

	entities := map[string]*metadata.Entity{}
	for _, e := range proj.Entities {
		entities[strings.ToLower(e.Name)] = e
	}
	processors := map[string]bool{}
	for _, p := range proj.Processors {
		processors[strings.ToLower(p.Name)] = true
	}
	reportChartProcs := map[string]string{}
	for _, r := range proj.Reports {
		reportChartProcs[strings.ToLower(r.Name)] = r.ChartProc
	}

	for name, prog := range proj.Programs {
		low := strings.ToLower(name)
		switch {
		case processors[low]:
			add(name, "DSL обработка", prog, rootNames("Выполнить"), false)
		case reportChartProcs[low] != "":
			add(name, "DSL отчёт", prog, rootNames(reportChartProcs[low]), false)
		case entities[low] != nil:
			add(name, "DSL объект", prog, rootNames(
				"OnWrite", "ПриЗаписи",
				"OnPost", "ОбработкаПроведения",
				"OnFill", "ОбработкаЗаполнения",
				"Печать", "Print",
			), false)
		default:
			add(name, "DSL модуль", prog, nil, false)
		}
	}
	for name, prog := range proj.ManagerPrograms {
		add(name, "DSL менеджер", prog, nil, true)
	}

	serviceRoots := map[string]map[string]bool{}
	for _, svc := range proj.HTTPServices {
		roots := serviceRoots[strings.ToLower(svc.Name)]
		if roots == nil {
			roots = map[string]bool{}
			serviceRoots[strings.ToLower(svc.Name)] = roots
		}
		for _, tmpl := range svc.Templates {
			for _, handler := range tmpl.Methods {
				if strings.TrimSpace(handler) != "" {
					roots[strings.ToLower(handler)] = true
				}
			}
		}
	}
	for name, prog := range proj.ServicePrograms {
		add(name, "DSL HTTP-сервис", prog, serviceRoots[strings.ToLower(name)], false)
	}

	for name, prog := range proj.PagePrograms {
		add(name, "DSL страница", prog, rootNames("ПриФормировании"), false)
	}
	for name, prog := range proj.Modules {
		add(name, "DSL общий модуль", prog, nil, false)
	}

	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			prog, _ := form.ProgramAST.(*ast.Program)
			if prog == nil {
				continue
			}
			roots := map[string]bool{}
			collectFormHandlerRoots(form, roots)
			formName := form.Name
			if formName == "" {
				formName = ent.Name
			}
			add(ent.Name+"/"+formName, "DSL форма", prog, roots, false)
		}
	}
	return out
}

func collectFormHandlerRoots(form *metadata.FormModule, roots map[string]bool) {
	for _, handler := range form.Handlers {
		addRoot(roots, handler)
	}
	var walkElements func([]*metadata.FormElement)
	walkElements = func(elements []*metadata.FormElement) {
		for _, el := range elements {
			for _, handler := range el.Handlers {
				addRoot(roots, handler)
			}
			walkElements(el.Children)
		}
	}
	walkElements(form.Elements)
	for _, cmd := range form.Commands {
		addRoot(roots, cmd.Action)
	}
}

func addRoot(roots map[string]bool, name string) {
	if strings.TrimSpace(name) != "" {
		roots[strings.ToLower(name)] = true
	}
}

func rootNames(names ...string) map[string]bool {
	roots := make(map[string]bool, len(names))
	for _, name := range names {
		addRoot(roots, name)
	}
	return roots
}

func lintUnusedVars(lp lintProgram) []Issue {
	if lp.prog == nil {
		return nil
	}
	var issues []Issue
	moduleVars := collectModuleVars(lp.prog)
	if len(moduleVars) > 0 {
		reads := map[string]int{}
		for _, pr := range lp.prog.Procedures {
			for _, def := range pr.Defaults {
				collectDSLReadsExpr(def, reads)
			}
			collectDSLReadsStmts(pr.Body, reads)
		}
		collectDSLReadsStmts(lp.prog.Body, reads)
		for _, decl := range lp.prog.ModuleVars {
			if decl.Exported {
				continue
			}
			for _, tok := range decl.Names {
				if reads[strings.ToLower(tok.Literal)] == 0 {
					issues = append(issues, unusedVarIssue(lp, tok, "переменная модуля"))
				}
			}
		}
	}

	moduleVarTokens := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			moduleVarTokens[tokenKey(tok)] = true
		}
	}
	for _, pr := range lp.prog.Procedures {
		decls := map[string][]token.Token{}
		collectLocalVarDecls(pr.Body, decls, moduleVarTokens)
		if len(decls) == 0 {
			continue
		}
		reads := map[string]int{}
		for _, def := range pr.Defaults {
			collectDSLReadsExpr(def, reads)
		}
		collectDSLReadsStmts(pr.Body, reads)
		names := make([]string, 0, len(decls))
		for name := range decls {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if reads[name] > 0 {
				continue
			}
			for _, tok := range decls[name] {
				issues = append(issues, unusedVarIssue(lp, tok, "локальная переменная"))
			}
		}
	}
	return issues
}

func collectModuleVars(prog *ast.Program) map[string]token.Token {
	vars := map[string]token.Token{}
	for _, decl := range prog.ModuleVars {
		for _, tok := range decl.Names {
			vars[strings.ToLower(tok.Literal)] = tok
		}
	}
	return vars
}

func collectLocalVarDecls(stmts []ast.Stmt, out map[string][]token.Token, skip map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.VarDecl:
			for _, tok := range v.Names {
				if skip[tokenKey(tok)] {
					continue
				}
				out[strings.ToLower(tok.Literal)] = append(out[strings.ToLower(tok.Literal)], tok)
			}
		case *ast.IfStmt:
			collectLocalVarDecls(v.Then, out, skip)
			for _, ei := range v.ElseIfs {
				collectLocalVarDecls(ei.Body, out, skip)
			}
			collectLocalVarDecls(v.Else, out, skip)
		case *ast.ForEachStmt:
			collectLocalVarDecls(v.Body, out, skip)
		case *ast.NumericForStmt:
			collectLocalVarDecls(v.Body, out, skip)
		case *ast.TryStmt:
			collectLocalVarDecls(v.Try, out, skip)
			collectLocalVarDecls(v.Except, out, skip)
		}
	}
}

func unusedVarIssue(lp lintProgram, tok token.Token, role string) Issue {
	return Issue{
		File:         sourceLabelForToken(lp.label, tok),
		Object:       lp.object,
		Kind:         lp.kind,
		Code:         "dsl.unused-var",
		Line:         tok.Line,
		Column:       tok.Col,
		Message:      fmt.Sprintf("%s %q объявлена, но не читается", role, tok.Literal),
		SuggestedFix: "Удалите объявление или используйте переменную в коде.",
	}
}

func lintDeadProcedures(programs []lintProgram) []Issue {
	type procNode struct {
		lp    lintProgram
		proc  *ast.ProcedureDecl
		root  bool
		edges []int
	}
	var nodes []procNode
	byName := map[string][]int{}
	for _, lp := range programs {
		for _, pr := range lp.prog.Procedures {
			root := lp.rootAll || pr.Export || lp.roots[strings.ToLower(pr.Name.Literal)]
			nodes = append(nodes, procNode{lp: lp, proc: pr, root: root})
			idx := len(nodes) - 1
			byName[strings.ToLower(pr.Name.Literal)] = append(byName[strings.ToLower(pr.Name.Literal)], idx)
		}
	}
	for i := range nodes {
		calls := map[string]bool{}
		for _, def := range nodes[i].proc.Defaults {
			collectDSLCallNamesExpr(def, calls)
		}
		collectDSLCallNamesStmts(nodes[i].proc.Body, calls)
		for name := range calls {
			nodes[i].edges = append(nodes[i].edges, byName[name]...)
		}
	}
	reachable := make([]bool, len(nodes))
	var visit func(int)
	visit = func(i int) {
		if i < 0 || i >= len(nodes) || reachable[i] {
			return
		}
		reachable[i] = true
		for _, j := range nodes[i].edges {
			visit(j)
		}
	}
	for i := range nodes {
		if nodes[i].root {
			visit(i)
		}
	}
	var issues []Issue
	for i, n := range nodes {
		if reachable[i] {
			continue
		}
		tok := n.proc.Name
		issues = append(issues, Issue{
			File:         sourceLabelForToken(n.lp.label, tok),
			Object:       n.lp.object,
			Kind:         n.lp.kind,
			Code:         "dsl.dead-procedure",
			Line:         tok.Line,
			Column:       tok.Col,
			Message:      fmt.Sprintf("процедура %q не достижима из известных точек входа", tok.Literal),
			SuggestedFix: "Удалите процедуру, вызовите её из рабочей точки входа или пометьте Экспорт, если она вызывается извне.",
		})
	}
	return issues
}

func collectDSLReadsStmts(stmts []ast.Stmt, reads map[string]int) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectDSLReadsExpr(v.X, reads)
		case *ast.AssignStmt:
			if v.Op == token.ASSIGN {
				collectDSLTargetReads(v.Target, reads)
			} else {
				collectDSLReadsExpr(v.Target, reads)
			}
			collectDSLReadsExpr(v.Value, reads)
		case *ast.ReturnStmt:
			collectDSLReadsExpr(v.Value, reads)
		case *ast.IfStmt:
			collectDSLReadsExpr(v.Cond, reads)
			collectDSLReadsStmts(v.Then, reads)
			for _, ei := range v.ElseIfs {
				collectDSLReadsExpr(ei.Cond, reads)
				collectDSLReadsStmts(ei.Body, reads)
			}
			collectDSLReadsStmts(v.Else, reads)
		case *ast.ForEachStmt:
			collectDSLReadsExpr(v.Collection, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.NumericForStmt:
			collectDSLReadsExpr(v.Start, reads)
			collectDSLReadsExpr(v.End, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.WhileStmt:
			collectDSLReadsExpr(v.Cond, reads)
			collectDSLReadsStmts(v.Body, reads)
		case *ast.TryStmt:
			collectDSLReadsStmts(v.Try, reads)
			collectDSLReadsStmts(v.Except, reads)
		}
	}
}

func collectDSLTargetReads(expr ast.Expr, reads map[string]int) {
	switch v := expr.(type) {
	case *ast.Ident:
		return
	case *ast.MemberExpr:
		collectDSLReadsExpr(v.Object, reads)
	case *ast.IndexExpr:
		collectDSLReadsExpr(v.Object, reads)
		collectDSLReadsExpr(v.Index, reads)
	default:
		collectDSLReadsExpr(expr, reads)
	}
}

func collectDSLReadsExpr(expr ast.Expr, reads map[string]int) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.Ident:
		reads[strings.ToLower(v.Tok.Literal)]++
	case *ast.CallExpr:
		if _, ok := v.Callee.(*ast.Ident); !ok {
			collectDSLReadsExpr(v.Callee, reads)
		}
		for _, arg := range v.Args {
			collectDSLReadsExpr(arg, reads)
		}
	case *ast.MemberExpr:
		collectDSLReadsExpr(v.Object, reads)
	case *ast.BinaryExpr:
		collectDSLReadsExpr(v.Left, reads)
		collectDSLReadsExpr(v.Right, reads)
	case *ast.UnaryExpr:
		collectDSLReadsExpr(v.Operand, reads)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectDSLReadsExpr(arg, reads)
		}
	case *ast.IndexExpr:
		collectDSLReadsExpr(v.Object, reads)
		collectDSLReadsExpr(v.Index, reads)
	case *ast.TernaryExpr:
		collectDSLReadsExpr(v.Cond, reads)
		collectDSLReadsExpr(v.True, reads)
		collectDSLReadsExpr(v.False, reads)
	}
}

func collectDSLCallNamesStmts(stmts []ast.Stmt, calls map[string]bool) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			collectDSLCallNamesExpr(v.X, calls)
		case *ast.AssignStmt:
			collectDSLCallNamesExpr(v.Target, calls)
			collectDSLCallNamesExpr(v.Value, calls)
		case *ast.ReturnStmt:
			collectDSLCallNamesExpr(v.Value, calls)
		case *ast.IfStmt:
			collectDSLCallNamesExpr(v.Cond, calls)
			collectDSLCallNamesStmts(v.Then, calls)
			for _, ei := range v.ElseIfs {
				collectDSLCallNamesExpr(ei.Cond, calls)
				collectDSLCallNamesStmts(ei.Body, calls)
			}
			collectDSLCallNamesStmts(v.Else, calls)
		case *ast.ForEachStmt:
			collectDSLCallNamesExpr(v.Collection, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.NumericForStmt:
			collectDSLCallNamesExpr(v.Start, calls)
			collectDSLCallNamesExpr(v.End, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.WhileStmt:
			collectDSLCallNamesExpr(v.Cond, calls)
			collectDSLCallNamesStmts(v.Body, calls)
		case *ast.TryStmt:
			collectDSLCallNamesStmts(v.Try, calls)
			collectDSLCallNamesStmts(v.Except, calls)
		}
	}
}

func collectDSLCallNamesExpr(expr ast.Expr, calls map[string]bool) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.CallExpr:
		if ident, ok := v.Callee.(*ast.Ident); ok {
			calls[strings.ToLower(ident.Tok.Literal)] = true
		} else {
			collectDSLCallNamesExpr(v.Callee, calls)
		}
		for _, arg := range v.Args {
			collectDSLCallNamesExpr(arg, calls)
		}
	case *ast.MemberExpr:
		collectDSLCallNamesExpr(v.Object, calls)
	case *ast.BinaryExpr:
		collectDSLCallNamesExpr(v.Left, calls)
		collectDSLCallNamesExpr(v.Right, calls)
	case *ast.UnaryExpr:
		collectDSLCallNamesExpr(v.Operand, calls)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			collectDSLCallNamesExpr(arg, calls)
		}
	case *ast.IndexExpr:
		collectDSLCallNamesExpr(v.Object, calls)
		collectDSLCallNamesExpr(v.Index, calls)
	case *ast.TernaryExpr:
		collectDSLCallNamesExpr(v.Cond, calls)
		collectDSLCallNamesExpr(v.True, calls)
		collectDSLCallNamesExpr(v.False, calls)
	}
}

func CheckLintRoles(proj *project.Project, roles []*auth.Role) []Issue {
	if len(roles) == 0 {
		return nil
	}
	coveredCatalogs := map[string]bool{}
	coveredDocuments := map[string]bool{}
	coveredRegisters := map[string]bool{}
	coveredInfoRegs := map[string]bool{}
	coveredReports := map[string]bool{}
	coveredProcessors := map[string]bool{}
	processorsOpen := false

	mark := func(dst map[string]bool, src map[string][]string) {
		for name, ops := range src {
			if len(ops) > 0 {
				dst[strings.ToLower(name)] = true
			}
		}
	}
	for _, role := range roles {
		mark(coveredCatalogs, role.Permissions.Catalogs)
		mark(coveredDocuments, role.Permissions.Documents)
		mark(coveredRegisters, role.Permissions.Registers)
		mark(coveredInfoRegs, role.Permissions.InfoRegs)
		mark(coveredReports, role.Permissions.Reports)
		if role.Permissions.Processors == nil {
			processorsOpen = true
		} else {
			mark(coveredProcessors, role.Permissions.Processors)
		}
	}

	var issues []Issue
	add := func(file, object, kind string) {
		issues = append(issues, Issue{
			File:         file,
			Object:       object,
			Kind:         kind,
			Code:         "rbac.object-without-role",
			Message:      fmt.Sprintf("%s %q не получает прав ни в одной роли", kind, object),
			SuggestedFix: "Добавьте объект в roles/*.yaml или удалите его, если он больше не используется.",
		})
	}
	for _, ent := range proj.Entities {
		if ent.Kind == metadata.KindCatalog {
			if !coveredCatalogs[strings.ToLower(ent.Name)] {
				add("catalogs/"+ent.Name+".yaml", ent.Name, "Справочник")
			}
			continue
		}
		if !coveredDocuments[strings.ToLower(ent.Name)] {
			add("documents/"+ent.Name+".yaml", ent.Name, "Документ")
		}
	}
	for _, reg := range proj.Registers {
		if !coveredRegisters[strings.ToLower(reg.Name)] {
			add("registers/"+reg.Name+".yaml", reg.Name, "Регистр")
		}
	}
	for _, ir := range proj.InfoRegisters {
		if !coveredInfoRegs[strings.ToLower(ir.Name)] {
			add("inforegs/"+ir.Name+".yaml", ir.Name, "Регистр сведений")
		}
	}
	for _, rep := range proj.Reports {
		if !coveredReports[strings.ToLower(rep.Name)] {
			add("reports/"+rep.Name+".yaml", rep.Name, "Отчёт")
		}
	}
	if !processorsOpen {
		for _, proc := range proj.Processors {
			if !coveredProcessors[strings.ToLower(proc.Name)] {
				add("processors/"+proc.Name+".yaml", proc.Name, "Обработка")
			}
		}
	}
	return issues
}

func programLabel(dir string, prog *ast.Program) string {
	for _, pr := range prog.Procedures {
		if pr.Name.File != "" {
			return relLabel(dir, pr.Name.File)
		}
	}
	for _, decl := range prog.ModuleVars {
		for _, tok := range decl.Names {
			if tok.File != "" {
				return relLabel(dir, tok.File)
			}
		}
	}
	return ""
}

func relLabel(root, path string) string {
	if path == "" {
		return ""
	}
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func sourceLabelForToken(fallback string, tok token.Token) string {
	if fallback != "" {
		return fallback
	}
	return filepath.ToSlash(tok.File)
}

func tokenKey(tok token.Token) string {
	return fmt.Sprintf("%s:%d:%d:%s", tok.File, tok.Line, tok.Col, strings.ToLower(tok.Literal))
}
