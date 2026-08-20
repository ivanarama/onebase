package cli

// Бюджет сборки сервера (#787 ARCH-01, план 137).
//
// Запуск сервера — одна функция, которая открывает базу, гонит миграции,
// поднимает интерпретатор, планировщик, очередь заданий, бэкапы, почту,
// веб-хуки, API и hot-reload. Развязать её на `Build()/Run()/Close()` владелец
// заявки сознательно отложил: во время активной работы над фичами такой
// рефакторинг конфликтует с каждым вторым PR.
//
// Отложенное решение имеет цену, и цена росла: аудит 11.08 намерил 545 строк,
// через четыре дня их было 823. Каждая новая строка здесь делает будущий
// рефакторинг дороже — и делает это молча, потому что «функция и так большая»
// перестаёт быть новостью после первого прочтения.
//
// Поэтому бюджет: расти нельзя, уменьшаться можно. Тест не требует делать
// рефакторинг сейчас — он требует не увеличивать его цену, пока решение ждёт.
//
// Считаем ОПЕРАТОРЫ, а не строки: комментарии в этом репозитории несут
// объяснение «почему», и гейт, наказывающий за них, вреден.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// startupStatementBudget — сколько операторов сейчас в runServerGeneration.
// Уменьшили функцию — уменьшите и число: бюджет, оторвавшийся от факта,
// перестаёт что-либо держать.
const startupStatementBudget = 519

// startupFunc — точка сборки сервера, за размером которой следим.
const startupFunc = "runServerGeneration"

func TestStartupCompositionDoesNotGrow(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "run.go", nil, 0)
	if err != nil {
		t.Fatalf("разбор run.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == startupFunc && d.Recv == nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatalf("в run.go нет функции %s — если сборку вынесли, перенесите бюджет за ней "+
			"(или снимите тест и закройте #787)", startupFunc)
	}

	got := countStatements(fn.Body)
	lines := fset.Position(fn.End()).Line - fset.Position(fn.Pos()).Line + 1
	switch {
	case got > startupStatementBudget:
		t.Errorf("%s вырос до %d операторов (%d строк) при бюджете %d.\n"+
			"Поднимать бюджет — не выход: он и есть цена отложенного рефакторинга (#787, план 137).\n"+
			"Вынесите новую фазу запуска отдельной функцией — так сборка разбирается по частям, "+
			"не дожидаясь большого рефакторинга.", startupFunc, got, lines, startupStatementBudget)
	case got < startupStatementBudget:
		t.Errorf("%s ужался до %d операторов (%d строк) при бюджете %d — опустите "+
			"startupStatementBudget до %d, иначе освобождённое место молча займут обратно.",
			startupFunc, got, lines, startupStatementBudget, got)
	}
}

// countStatements считает операторы во всём дереве тела функции, включая
// вложенные в if/for/switch и в литералы функций: замыкание, объявленное внутри
// сборки, — такая же её часть, как и всё остальное.
func countStatements(body *ast.BlockStmt) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch node.(type) {
		case nil, *ast.BlockStmt:
			return true
		}
		if _, ok := node.(ast.Stmt); ok {
			n++
		}
		return true
	})
	return n
}
