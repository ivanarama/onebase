package configcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// paramRefRe выбирает имена параметров запроса (&Имя), чтобы подставить их как
// плейсхолдеры в CompileOpts.Params — иначе компилятор спотыкается на &Param.
var paramRefRe = regexp.MustCompile(`&([\p{L}_][\p{L}\p{Nd}_]*)`)

// moduleQuery — статический текст запроса, извлечённый из .os-модуля, с локацией
// строкового литерала для точного сообщения об ошибке.
type moduleQuery struct {
	text string
	line int
	col  int
}

// CheckModuleQueries компилирует статические запросы вида `Запрос.Текст = "..."`
// из .os-модулей: src/*.os и модули управляемых форм forms/<сущность>/*.form.os.
// CheckQueries покрывает только виджеты/отчёты — запросы внутри модулей раньше
// не проверялись вовсе (так в примере «закрытие месяца» прошёл незамеченным
// неподдерживаемый ПОДОБНО). Если validate != nil — дополнительно PREPARE
// против in-memory схемы (как CheckQueriesExecutable). Динамически собранные
// тексты (конкатенация с переменными) пропускаются — их статически не извлечь.
func CheckModuleQueries(proj *project.Project, validate func(string) error) []Issue {
	var issues []Issue
	files := moduleSourceFiles(proj.Dir)
	if len(files) == 0 {
		return nil
	}
	opts := query.CompileOpts{
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Entities:    proj.Entities,
	}
	if validate != nil {
		opts.Dialect = storage.SQLiteDialect{}
	}
	for _, f := range files {
		label := f.label
		raw, rerr := os.ReadFile(f.path)
		if rerr != nil {
			continue
		}
		prog, perr := parser.New(lexer.New(string(raw), label)).ParseProgram()
		if perr != nil {
			continue // синтаксис уже репортит CheckDir
		}
		var found []moduleQuery
		for _, pr := range prog.Procedures {
			// Поле «Текст» есть не только у объекта Запрос (документы, табличные
			// документы и пр.), поэтому ловим присваивания .Текст только для
			// переменных, инициализированных «Новый Запрос» — иначе строки-данные
			// принимаются за запросы (ложные срабатывания).
			queryVars := map[string]bool{}
			collectQueryVars(pr.Body, queryVars)
			collectModuleQueries(pr.Body, queryVars, &found)
		}
		for _, q := range found {
			params := map[string]any{}
			for _, m := range paramRefRe.FindAllStringSubmatch(q.text, -1) {
				params[m[1]] = nil
			}
			o := opts
			o.Params = params
			r, cerr := query.Compile(q.text, o)
			if cerr != nil {
				issues = append(issues, Issue{
					File: label, Kind: "Запрос модуля",
					Message: cerr.Error(), Line: q.line, Column: q.col,
				})
				continue
			}
			if validate != nil {
				if verr := validate(r.SQL); verr != nil {
					issues = append(issues, Issue{
						File: label, Kind: "Запрос модуля (исполнение)",
						Message: verr.Error(), Line: q.line, Column: q.col,
					})
				}
			}
		}
	}
	return issues
}

// moduleSource — файл модуля для проверки: путь на диске и метка для сообщения.
type moduleSource struct{ label, path string }

// moduleSourceFiles перечисляет .os-модули конфигурации: src/*.os и модули
// управляемых форм forms/<сущность>/*.form.os.
//
// ФОРМЫ ДОБАВЛЕНЫ ОТДЕЛЬНО, потому что живут не в src/ и раньше не проверялись
// вовсе: запрос в обработчике кнопки компилировался впервые уже при нажатии, у
// пользователя. Это ровно тот класс, ради которого проверка и написана —
// «no such column» на форме ничем не отличается от такой же ошибки в проведении,
// кроме момента, когда её увидят.
func moduleSourceFiles(dir string) []moduleSource {
	var out []moduleSource
	isModule := func(name string) bool {
		return strings.HasSuffix(strings.ToLower(name), ".os")
	}
	srcDir := filepath.Join(dir, "src")
	if entries, err := os.ReadDir(srcDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !isModule(e.Name()) {
				continue
			}
			out = append(out, moduleSource{"src/" + e.Name(), filepath.Join(srcDir, e.Name())})
		}
	}
	// forms/<сущность>/<форма>.form.os — на один уровень глубже; имена каталогов
	// и файлов в нижнем регистре, но полагаться на это не нужно.
	formsDir := filepath.Join(dir, "forms")
	entityDirs, err := os.ReadDir(formsDir)
	if err != nil {
		return out
	}
	for _, d := range entityDirs {
		if !d.IsDir() {
			continue
		}
		sub := filepath.Join(formsDir, d.Name())
		files, ferr := os.ReadDir(sub)
		if ferr != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !isModule(f.Name()) {
				continue
			}
			out = append(out, moduleSource{
				label: "forms/" + d.Name() + "/" + f.Name(),
				path:  filepath.Join(sub, f.Name()),
			})
		}
	}
	return out
}

// collectQueryVars собирает имена переменных, которым где-либо в теле присвоен
// «Новый Запрос» (рекурсивно по вложенным блокам).
func collectQueryVars(stmts []ast.Stmt, vars map[string]bool) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.AssignStmt:
			if id, ok := v.Target.(*ast.Ident); ok && isNewQuery(v.Value) {
				vars[strings.ToLower(id.Tok.Literal)] = true
			}
		case *ast.IfStmt:
			collectQueryVars(v.Then, vars)
			for _, b := range v.ElseIfs {
				collectQueryVars(b.Body, vars)
			}
			collectQueryVars(v.Else, vars)
		case *ast.ForEachStmt:
			collectQueryVars(v.Body, vars)
		case *ast.NumericForStmt:
			collectQueryVars(v.Body, vars)
		case *ast.WhileStmt:
			collectQueryVars(v.Body, vars)
		case *ast.TryStmt:
			collectQueryVars(v.Try, vars)
			collectQueryVars(v.Except, vars)
		}
	}
}

// isNewQuery сообщает, что выражение — «Новый Запрос» (New Query).
func isNewQuery(e ast.Expr) bool {
	n, ok := e.(*ast.NewExpr)
	if !ok {
		return false
	}
	switch strings.ToLower(n.TypeName.Literal) {
	case "запрос", "query":
		return true
	}
	return false
}

// collectModuleQueries рекурсивно обходит операторы и собирает присваивания
// `<x>.Текст = "<статическая строка>"`, где <x> — переменная из queryVars.
func collectModuleQueries(stmts []ast.Stmt, queryVars map[string]bool, out *[]moduleQuery) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.AssignStmt:
			if isQueryTextTarget(v.Target, queryVars) {
				if text, line, col, ok := staticStringExpr(v.Value); ok {
					*out = append(*out, moduleQuery{text: text, line: line, col: col})
				}
			}
		case *ast.IfStmt:
			collectModuleQueries(v.Then, queryVars, out)
			for _, b := range v.ElseIfs {
				collectModuleQueries(b.Body, queryVars, out)
			}
			collectModuleQueries(v.Else, queryVars, out)
		case *ast.ForEachStmt:
			collectModuleQueries(v.Body, queryVars, out)
		case *ast.NumericForStmt:
			collectModuleQueries(v.Body, queryVars, out)
		case *ast.WhileStmt:
			collectModuleQueries(v.Body, queryVars, out)
		case *ast.TryStmt:
			collectModuleQueries(v.Try, queryVars, out)
			collectModuleQueries(v.Except, queryVars, out)
		}
	}
}

// isQueryTextTarget сообщает, что цель присваивания — свойство .Текст (.text)
// у переменной, объявленной как «Новый Запрос».
func isQueryTextTarget(e ast.Expr, queryVars map[string]bool) bool {
	m, ok := e.(*ast.MemberExpr)
	if !ok {
		return false
	}
	switch strings.ToLower(m.Field.Literal) {
	case "текст", "text":
	default:
		return false
	}
	id, ok := m.Object.(*ast.Ident)
	if !ok {
		return false
	}
	return queryVars[strings.ToLower(id.Tok.Literal)]
}

// staticStringExpr вычисляет строковый литерал или конкатенацию литералов через
// «+». Возвращает текст и позицию первого литерала. Если в выражении есть хоть
// одна переменная — текст не статический, ok=false (такой запрос пропускаем).
func staticStringExpr(e ast.Expr) (text string, line, col int, ok bool) {
	switch v := e.(type) {
	case *ast.StringLit:
		return v.Value, v.Tok.Line, v.Tok.Col, true
	case *ast.BinaryExpr:
		if v.Op.Type != token.PLUS {
			return "", 0, 0, false
		}
		l, ll, lc, lok := staticStringExpr(v.Left)
		r, _, _, rok := staticStringExpr(v.Right)
		if lok && rok {
			return l + r, ll, lc, true
		}
	}
	return "", 0, 0, false
}
