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
// из .os-модулей (обработка проведения, заполнения и т.п.). CheckQueries
// покрывает только виджеты/отчёты — запросы внутри модулей раньше не
// проверялись вовсе (так в примере «закрытие месяца» прошёл незамеченным
// неподдерживаемый ПОДОБНО). Если validate != nil — дополнительно PREPARE
// против in-memory схемы (как CheckQueriesExecutable). Динамически собранные
// тексты (конкатенация с переменными) пропускаются — их статически не извлечь.
func CheckModuleQueries(proj *project.Project, validate func(string) error) (issues, warnings []Issue) {
	srcDir := filepath.Join(proj.Dir, "src")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, nil
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
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".os") {
			continue
		}
		label := "src/" + e.Name()
		raw, rerr := os.ReadFile(filepath.Join(srcDir, e.Name()))
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
			warnings = append(warnings, queryTypeWarnings(r, label, "", "Запрос модуля", q.line, q.col)...)
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
	return issues, warnings
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
