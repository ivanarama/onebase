package expreval

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// #788: формула компоновки, вызывающая зацикленную функцию конфигурации, обязана
// прерываться песочницей по лимиту итераций, а не считаться вечно. Раньше
// UI-путь исполнял формулы без песочницы вовсе — этот сценарий вешал хендлер.
func TestEvaluator_SandboxBoundsRunawayExpression(t *testing.T) {
	src := "Функция Вечность()\n\tПока Истина Цикл\n\tКонецЦикла;\n\tВозврат Истина;\nКонецФункции\n"
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	procs := map[string]*ast.ProcedureDecl{}
	for _, d := range prog.Procedures {
		procs[d.Name.Literal] = d
		procs[strings.ToLower(d.Name.Literal)] = d
	}
	interp := interpreter.New()
	interp.LookupProc = func(name string) *ast.ProcedureDecl {
		if p, ok := procs[name]; ok {
			return p
		}
		return procs[strings.ToLower(name)]
	}

	ev := New(interp, interpreter.SandboxProfile{MaxLoopIters: 10_000})

	done := make(chan struct{})
	var evalErr error
	go func() {
		_, evalErr = ev.EvalBool("Вечность()", compose.Row{})
		close(done)
	}()
	select {
	case <-done:
		if evalErr == nil {
			t.Fatal("зацикленная формула вернулась без ошибки — песочница не ограничила цикл")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("формула не прервана — песочница не применилась (вычисление зависло)")
	}
}

// Контроль: нормальная формула считается штатно — та же реализация, что и до
// объединения путей.
func TestEvaluator_NormalExpression(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	got, err := ev.EvalBool("1 = 1", compose.Row{})
	if err != nil {
		t.Fatalf("EvalBool: %v", err)
	}
	if !got {
		t.Fatal("ожидалось true для «1 = 1»")
	}
}
