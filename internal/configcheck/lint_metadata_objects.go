package configcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/token"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// metadataManager — глобальный менеджер, после точки у которого стоит имя
// объекта конфигурации: Документы.<документ>, Движения.<регистр> и т.п.
type metadataManager struct {
	// what — вид объекта для сообщения: «документ», «регистр» …
	what string
	// names — имя объекта в нижнем регистре → имя, как оно объявлено.
	names map[string]string
}

// metadataManagers строит словарь менеджеров по загруженной конфигурации.
// Ключ — имя менеджера в нижнем регистре, русское и английское.
func metadataManagers(proj *project.Project) map[string]metadataManager {
	if proj == nil {
		return nil
	}
	lower := func(names ...string) map[string]string {
		out := make(map[string]string, len(names))
		for _, n := range names {
			out[strings.ToLower(n)] = n
		}
		return out
	}
	var docs, cats []string
	for _, e := range proj.Entities {
		switch e.Kind {
		case metadata.KindDocument:
			docs = append(docs, e.Name)
		case metadata.KindCatalog:
			cats = append(cats, e.Name)
		}
	}
	var accum, info, account, enums, consts []string
	for _, r := range proj.Registers {
		accum = append(accum, r.Name)
	}
	for _, r := range proj.InfoRegisters {
		info = append(info, r.Name)
	}
	for _, r := range proj.AccountRegisters {
		account = append(account, r.Name)
	}
	for _, e := range proj.Enums {
		enums = append(enums, e.Name)
	}
	for _, c := range proj.Constants {
		consts = append(consts, c.Name)
	}
	movements := lower(append(append(append([]string{}, accum...), info...), account...)...)
	out := map[string]metadataManager{}
	add := func(m metadataManager, keys ...string) {
		for _, k := range keys {
			out[k] = m
		}
	}
	add(metadataManager{"документ", lower(docs...)}, "документы", "documents")
	add(metadataManager{"справочник", lower(cats...)}, "справочники", "catalogs")
	add(metadataManager{"регистр накопления", lower(accum...)}, "регистрынакопления", "accumulationregisters")
	add(metadataManager{"регистр сведений", lower(info...)}, "регистрысведений", "inforegisters")
	add(metadataManager{"перечисление", lower(enums...)}, "перечисления", "enums")
	add(metadataManager{"константа", lower(consts...)}, "константы", "constants")
	add(metadataManager{"регистр", movements}, "движения", "movements")
	return out
}

// lintUnknownMetadataObjects помечает обращение к несуществующему объекту
// конфигурации через глобальный менеджер: Документы.СписаниеСРасчётныйСчёт при
// документе «СписаниеСРасчётногоСчёта».
//
// Последствие опечатки зависит от менеджера: Документы возвращает
// Неопределено, а Движения создаёт коллектор даже для неизвестного регистра.
// Проверка видит опечатку статически: имя сравнивается со списком объектов
// того вида, который отдаёт менеджер.
//
// Имя менеджера, объявленное в модуле как переменная (Документы = Новый
// Соответствие), — уже не менеджер: такое обращение не проверяется.
func lintUnknownMetadataObjects(dir string, lp lintProgram, managers map[string]metadataManager) []Issue {
	if lp.prog == nil || len(managers) == 0 {
		return nil
	}
	shadowed := map[string]bool{}
	for _, decl := range lp.prog.ModuleVars {
		for _, tok := range decl.Names {
			shadowed[strings.ToLower(tok.Literal)] = true
		}
	}
	collectDeclaredAndAssigned(lp.prog.Body, shadowed)

	var issues []Issue
	seen := map[string]bool{}
	check := func(local map[string]bool) func(base, field token.Token) {
		return func(base, field token.Token) {
			name := strings.ToLower(base.Literal)
			if local[name] {
				return
			}
			m, ok := managers[name]
			if !ok {
				return
			}
			if _, known := m.names[strings.ToLower(field.Literal)]; known {
				return
			}
			key := tokenKey(field)
			if seen[key] {
				return
			}
			seen[key] = true
			file := lp.label
			if field.File != "" {
				file = relLabel(dir, field.File)
			}
			issues = append(issues, Issue{
				File:   file,
				Object: lp.object,
				Kind:   lp.kind,
				Code:   "dsl.unknown-metadata-object",
				Line:   field.Line,
				Column: field.Col,
				Message: fmt.Sprintf("%s.%s: такого объекта (%s) в конфигурации нет",
					base.Literal, field.Literal, m.what),
				SuggestedFix: suggestMetadataName(field.Literal, m),
			})
		}
	}

	for _, pr := range lp.prog.Procedures {
		local := map[string]bool{}
		// Загрузчик объединяет объектный модуль и модуль проведения, но
		// переменные остаются в области видимости исходного модуля процедуры.
		for _, decl := range pr.ModuleVars {
			for _, tok := range decl.Names {
				local[strings.ToLower(tok.Literal)] = true
			}
		}
		// Дефолты вычисляются до привязки параметров и выполнения тела:
		// в caller-env (legacy) либо module-env/root (strict lexical).
		// В статической области процедуры их затеняют только переменные
		// исходного модуля, а не её параметры и локальные присваивания.
		for _, def := range pr.Defaults {
			walkManagerMembersExpr(def, check(local))
		}
		for _, p := range pr.Params {
			local[strings.ToLower(p.Literal)] = true
		}
		collectDeclaredAndAssigned(pr.Body, local)
		visit := check(local)
		walkManagerMembersStmts(pr.Body, visit)
	}
	walkManagerMembersStmts(lp.prog.Body, check(shadowed))
	return issues
}

// suggestMetadataName подсказывает близкие по написанию объекты того же вида.
func suggestMetadataName(got string, m metadataManager) string {
	want := strings.ToLower(got)
	var candidates []string
	for low, name := range m.names {
		if closeMetadataName(want, low) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return "Сверьте имя с объектами конфигурации: опечатка в имени не видна до выполнения строки."
	}
	sort.Strings(candidates)
	return fmt.Sprintf("Возможно, имелось в виду: %s (имя сравнивается без учёта регистра).", strings.Join(candidates, ", "))
}

// closeMetadataName — имена отличаются не больше чем на пятую часть длины
// (но не меньше двух правок: вставка, удаление, замена символа). Опечатка в
// окончаниях длинного имени — типичный случай: «СписаниеСРасчётныйСчёт» против
// «СписаниеСРасчётногоСчёта» — это четыре правки на 24 буквы.
func closeMetadataName(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	limit := max(len(ra), len(rb)) / 5
	if limit < 2 {
		limit = 2
	}
	if d := len(ra) - len(rb); d > limit || d < -limit {
		return false
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)] <= limit
}

func walkManagerMembersExpr(expr ast.Expr, visit func(base, field token.Token)) {
	if expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.MemberExpr:
		if ident, ok := v.Object.(*ast.Ident); ok {
			visit(ident.Tok, v.Field)
		}
		walkManagerMembersExpr(v.Object, visit)
	case *ast.CallExpr:
		walkManagerMembersExpr(v.Callee, visit)
		for _, arg := range v.Args {
			walkManagerMembersExpr(arg, visit)
		}
	case *ast.BinaryExpr:
		walkManagerMembersExpr(v.Left, visit)
		walkManagerMembersExpr(v.Right, visit)
	case *ast.UnaryExpr:
		walkManagerMembersExpr(v.Operand, visit)
	case *ast.NewExpr:
		for _, arg := range v.Args {
			walkManagerMembersExpr(arg, visit)
		}
	case *ast.ArrayLit:
		for _, elem := range v.Elements {
			walkManagerMembersExpr(elem, visit)
		}
	case *ast.IndexExpr:
		walkManagerMembersExpr(v.Object, visit)
		walkManagerMembersExpr(v.Index, visit)
	case *ast.TernaryExpr:
		walkManagerMembersExpr(v.Cond, visit)
		walkManagerMembersExpr(v.True, visit)
		walkManagerMembersExpr(v.False, visit)
	}
}

func walkManagerMembersStmts(stmts []ast.Stmt, visit func(base, field token.Token)) {
	for _, stmt := range stmts {
		switch v := stmt.(type) {
		case *ast.ExprStmt:
			walkManagerMembersExpr(v.X, visit)
		case *ast.AssignStmt:
			walkManagerMembersExpr(v.Target, visit)
			walkManagerMembersExpr(v.Value, visit)
		case *ast.ReturnStmt:
			walkManagerMembersExpr(v.Value, visit)
		case *ast.IfStmt:
			walkManagerMembersExpr(v.Cond, visit)
			walkManagerMembersStmts(v.Then, visit)
			for _, ei := range v.ElseIfs {
				walkManagerMembersExpr(ei.Cond, visit)
				walkManagerMembersStmts(ei.Body, visit)
			}
			walkManagerMembersStmts(v.Else, visit)
		case *ast.ForEachStmt:
			walkManagerMembersExpr(v.Collection, visit)
			walkManagerMembersStmts(v.Body, visit)
		case *ast.NumericForStmt:
			walkManagerMembersExpr(v.Start, visit)
			walkManagerMembersExpr(v.End, visit)
			walkManagerMembersStmts(v.Body, visit)
		case *ast.WhileStmt:
			walkManagerMembersExpr(v.Cond, visit)
			walkManagerMembersStmts(v.Body, visit)
		case *ast.TryStmt:
			walkManagerMembersStmts(v.Try, visit)
			walkManagerMembersStmts(v.Except, visit)
		}
	}
}
