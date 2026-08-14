package interpreter_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"unicode"
)

// Список доступных методов в подсказке обязан совпадать с тем, что реально
// разбирает switch.
//
// Повод — issue #887: подсказка Массива рекламировала «Сортировать», которого
// в switch не было, и пользователь, послушавшийся подсказки, получал ту же
// ошибку ещё раз. В обратную сторону список разъехался тоже: «ВГраница» была
// реализована, но не показывалась.
//
// Тест читает ИСХОДНИК, а не вызывает методы: только так видно расхождение в
// обе стороны. Проверка «каждый метод из списка вызывается без ошибки» ловит
// лишь первую половину — метод, которого нет в списке, ей не с чем сравнить.

// caseNamesOf возвращает русские имена методов из case'ов CallMethod у типа.
// Английские синонимы (add, count, …) в подсказке намеренно не перечисляются —
// она отвечает на вопрос «как это пишется по-русски», поэтому в сверке
// участвуют только кириллические литералы.
func caseNamesOf(t *testing.T, file *ast.File, recvType string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "CallMethod" || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != recvType {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				name := strings.Trim(lit.Value, `"`)
				if hasCyrillic(name) {
					names[strings.ToLower(name)] = true
				}
			}
			return true
		})
	}
	if len(names) == 0 {
		t.Fatalf("в collections.go не найден CallMethod у *%s — тест перестал проверять то, ради чего написан", recvType)
	}
	return names
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// listNamesOf возвращает содержимое объявления `var <name> = []string{…}`.
func listNamesOf(t *testing.T, file *ast.File, varName string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, ident := range spec.Names {
			if ident.Name != varName || i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, el := range lit.Elts {
				if bl, ok := el.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					names[strings.ToLower(strings.Trim(bl.Value, `"`))] = true
				}
			}
		}
		return true
	})
	if len(names) == 0 {
		t.Fatalf("в unknown_method.go не найден список %s", varName)
	}
	return names
}

func TestПодсказка_СписокМетодовСовпадаетСРеализацией(t *testing.T) {
	fset := token.NewFileSet()
	collections, err := parser.ParseFile(fset, "collections.go", nil, 0)
	if err != nil {
		t.Fatalf("разбор collections.go: %v", err)
	}
	hints, err := parser.ParseFile(fset, "unknown_method.go", nil, 0)
	if err != nil {
		t.Fatalf("разбор unknown_method.go: %v", err)
	}

	for _, c := range []struct{ typeName, listVar, dslName string }{
		{"Array", "arrayMethods", "Массив"},
		{"Struct", "structMethods", "Структура"},
		{"Map", "mapMethods", "Соответствие"},
	} {
		t.Run(c.dslName, func(t *testing.T) {
			implemented := caseNamesOf(t, collections, c.typeName)
			advertised := listNamesOf(t, hints, c.listVar)

			for name := range advertised {
				if !implemented[name] {
					t.Errorf("подсказка %s обещает «%s», а switch его не разбирает — "+
						"пользователь, послушавшийся подсказки, получит ту же ошибку ещё раз", c.dslName, name)
				}
			}
			for name := range implemented {
				if !advertised[name] {
					t.Errorf("%s.%s реализован, но в подсказке не перечислен — метод есть, а найти его нечем",
						c.dslName, name)
				}
			}
		})
	}
}

// Сортировать() — тот самый метод, который подсказка обещала, а движок не умел.
// Проверяется публичным путём: через исполнение модуля, а не вызовом CallMethod.
func TestМассив_Сортировать(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			"числа по возрастанию",
			`М = Новый Массив; М.Добавить(3); М.Добавить(1); М.Добавить(2);
			 М.Сортировать();
			 Возврат Строка(М.Получить(0)) + Строка(М.Получить(1)) + Строка(М.Получить(2));`,
			"123",
		},
		{
			"числа по убыванию",
			`М = Новый Массив; М.Добавить(3); М.Добавить(1); М.Добавить(2);
			 М.Сортировать("Убыв");
			 Возврат Строка(М.Получить(0)) + Строка(М.Получить(1)) + Строка(М.Получить(2));`,
			"321",
		},
		{
			"строки",
			`М = Новый Массив; М.Добавить("бета"); М.Добавить("альфа");
			 М.Сортировать();
			 Возврат М.Получить(0) + "," + М.Получить(1);`,
			"альфа,бета",
		},
		{
			"пустой массив не падает",
			`М = Новый Массив; М.Сортировать(); Возврат Строка(М.Количество());`,
			"0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := runSnippet(t, c.src)
			if err != nil {
				t.Fatalf("Сортировать: %v", err)
			}
			if got := strings.TrimSpace(toStr(res)); got != c.want {
				t.Errorf("получено %q, ожидалось %q", got, c.want)
			}
		})
	}
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}
