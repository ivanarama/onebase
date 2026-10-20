package interpreter_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// benchSrc — представительная бизнес-процедура: цикл с decimal-арифметикой,
// присваиваниями и ветвлением. Нагружает горячие пути интерпретатора —
// поиск имён в окружении (O(глубина)), decimal-операции, диспетчер выражений.
const benchSrc = `Процедура Расчёт()
  Итог = 0;
  Сч = 1;
  Пока Сч <= 500 Цикл
    Если Сч > 250 Тогда
      Итог = Итог + Сч * 2 - 1;
    Иначе
      Итог = Итог + Сч;
    КонецЕсли;
    Сч = Сч + 1;
  КонецЦикла;
  this.Итог = Итог;
КонецПроцедуры`

// BenchmarkInterpreter_Run меряет throughput интерпретатора: парсинг вынесен
// из измеряемого окна, в цикле — только Run. Это аналог «сколько раз в секунду
// отработает OnWrite/OnPost» без накладных БД и сети.
func BenchmarkInterpreter_Run(b *testing.B) {
	prog, err := parser.New(lexer.New(benchSrc, "bench.os")).ParseProgram()
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	proc := prog.Procedures[0]
	interp := interpreter.New()
	obj := runtime.NewObject("Расчёт", metadata.KindCatalog)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := interp.Run(proc, obj); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}

// BenchmarkInterpreter_Parse меряет фронтенд (лексер + парсер) на той же
// процедуре — отдельно от исполнения, чтобы регрессии парсинга были видны.
func BenchmarkInterpreter_Parse(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := parser.New(lexer.New(benchSrc, "bench.os")).ParseProgram(); err != nil {
			b.Fatalf("parse: %v", err)
		}
	}
}

// BenchmarkInterpreter_UserProcedureCalls isolates repeated DSL calls from
// parsing and arithmetic. Each operation invokes 100 helpers with a parameter.
func BenchmarkInterpreter_UserProcedureCalls(b *testing.B) {
	src := "Процедура Старт()\n" + strings.Repeat("Касание(\"value\");\n", 100) + `КонецПроцедуры
Процедура Касание(value)
	Возврат;
КонецПроцедуры`
	prog, err := parser.New(lexer.New(src, "calls.os")).ParseProgram()
	if err != nil {
		b.Fatal(err)
	}
	for _, strict := range []bool{false, true} {
		name := "legacy"
		if strict {
			name = "strict"
		}
		b.Run(name, func(b *testing.B) {
			in := interpreter.New()
			in.StrictLexicalScope = strict
			in.LookupProc = func(name string) *ast.ProcedureDecl {
				if strings.EqualFold(name, "Касание") {
					return prog.Procedures[1]
				}
				return nil
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if _, err := in.Call(prog.Procedures[0], nil, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
