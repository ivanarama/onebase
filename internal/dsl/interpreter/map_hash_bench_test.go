package interpreter_test

import (
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Нагрузка, на которой Соответствие с перебором ключей держало отчёт cc2 440 с:
// вставка N строковых ключей и поиск по ним, плюс поиск по составному ключу из
// семи частей. Запуск: go test ./internal/dsl/interpreter/ -run ^$ -bench MapKeys
func runMapBench(b *testing.B, src string) {
	b.Helper()
	prog, err := parser.New(lexer.New(src, "bench.os")).ParseProgram()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result any
		if err := interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("Bench", metadata.KindDocument), &result); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMapKeys_InsertAndGet(b *testing.B) {
	for _, n := range []int{1000, 10000, 48000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			runMapBench(b, fmt.Sprintf(`Функция Тест()
	С = Новый Соответствие;
	Для Н = 1 По %[1]d Цикл
		С.Вставить("ключ-" + Строка(Н), Н);
	КонецЦикла;
	Сумма = 0;
	Для Н = 1 По %[1]d Цикл
		Сумма = Сумма + С.Получить("ключ-" + Строка(Н));
	КонецЦикла;
	Возврат Сумма;
КонецФункции`, n))
		})
	}
}

func BenchmarkMapKeys_CompositeKey(b *testing.B) {
	runMapBench(b, `Функция Тест()
	С = Новый Соответствие;
	Для Н = 1 По 5000 Цикл
		С.Вставить("склад|" + Строка(Н % 7) + "|мастер|" + Строка(Н) + "|деталь|" + Строка(Н * 3) + "|резерв", Н);
	КонецЦикла;
	Найдено = 0;
	Для Н = 1 По 5000 Цикл
		Если С.Получить("склад|" + Строка(Н % 7) + "|мастер|" + Строка(Н) + "|деталь|" + Строка(Н * 3) + "|резерв") <> Неопределено Тогда
			Найдено = Найдено + 1;
		КонецЕсли;
	КонецЦикла;
	Возврат Найдено;
КонецФункции`)
}
