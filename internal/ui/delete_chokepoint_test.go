package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Хуки удаления («ПередУдалением»/«ПослеУдаления») держатся на том, что ВСЕ
// пути удаления идут через entityservice.Delete. Стоит появиться новому
// хендлеру со своим store.Delete — и запрет, написанный в конфигурации,
// обходится сменой способа удаления. Это ровно тот дефект, который мы чинили:
// защита, которая не защищает.
//
// Сканер повторяет идею row_access_chokepoint_test: прямой вызов
// store.Delete/Exec("DELETE FROM …") для таблицы сущности вне entityservice
// обязан стоять в списке исключений с обоснованием.
//
// Добавляя код, физически удаляющий объект: зовите entityservice.Delete. Если
// удаление не относится к объектам конфигурации (служебные таблицы, регистры,
// вложения) — внесите функцию в deleteChokepointExempt с причиной.
var deleteChokepointExempt = map[string]string{
	// Сам чокпоинт и его внутренности.
	"deleteInTx": "это и есть общая точка удаления объекта",
	// Пометка на удаление — обратимая операция над флагом, не физическое
	// удаление; хуки к ней не относятся (в 1С тоже).
	"markRef": "пометка на удаление, а не физическое удаление",
}

// deleteCallScanner ищет вызовы вида store.Delete(...) / s.store.Delete(...),
// а также db.Delete(...) / p.db.Delete(...) — под именем db хранилище живёт в
// DSL-прокси (internal/dsl/interpreter), где раньше прятался обходной путь
// удаления справочников (issue #854).
func callsStoreDelete(fd *ast.FuncDecl) bool {
	found := false
	storeNames := map[string]bool{"store": true, "db": true}
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Delete" {
			return true
		}
		// Отсекаем Delete у чего угодно, кроме store/db: карты, кэши, репозитории
		// пользователей и т.п. живут своей жизнью.
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && storeNames[inner.Sel.Name] {
			found = true
		}
		if id, ok := sel.X.(*ast.Ident); ok && storeNames[id.Name] {
			found = true
		}
		return true
	})
	return found
}

func TestDeleteChokepoint_NoDirectStoreDelete(t *testing.T) {
	var offenders []string
	// ParseFile по явному списку, как в соседних стражах: ParseDir объявлен
	// устаревшим и валит линтер.
	for _, dir := range []string{".", filepath.Join("..", "api"), filepath.Join("..", "dsl", "interpreter")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("список файлов %s: %v", dir, err)
		}
		fset := token.NewFileSet()
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("разбор %s: %v", path, err)
			}
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Name == nil {
					continue
				}
				if _, exempt := deleteChokepointExempt[fd.Name.Name]; exempt {
					continue
				}
				if callsStoreDelete(fd) {
					offenders = append(offenders, fd.Name.Name+" ("+filepath.Base(path)+")")
				}
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("прямое store.Delete в обход entityservice.Delete — хуки удаления "+
			"там не сработают (%d): %s\n\nЗовите entityservice.Delete либо внесите "+
			"функцию в deleteChokepointExempt с обоснованием.",
			len(offenders), strings.Join(offenders, ", "))
	}
}
