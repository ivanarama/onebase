package interpreter_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Вызов несуществующего метода обязан падать, а не возвращать Неопределено.
//
// Прежде выражение молча давало Неопределено и выполнение шло дальше: опечатка
// в имени метода или неверный тип значения превращались в бесшумную потерю
// функциональности — код «отрабатывал», не сделав ничего (#718).

func runSnippet(t *testing.T, src string) (any, error) {
	t.Helper()
	prog, err := parser.New(lexer.New("Функция Тест()\n"+src+"\nКонецФункции", "t.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	in := interpreter.New()
	var res any
	err = in.RunWithResult(prog.Procedures[0], runtime.NewObject("T", metadata.KindCatalog), &res)
	return res, err
}

// Тип без методов вовсе: строка, число, булево, дата. Общий случай — закрыт
// в диспетчере и работает для любого такого значения.
func TestМетод_УТипаБезМетодовЭтоОшибка(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"строка", `С = "просто строка"; Возврат С.НесуществующийМетод();`, "Строка"},
		{"число", `Ч = 42; Возврат Ч.НесуществующийМетод();`, "Число"},
		{"булево", `Б = Истина; Возврат Б.НесуществующийМетод();`, "Булево"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := runSnippet(t, c.src)
			if err == nil {
				t.Fatalf("вызов несуществующего метода прошёл молча, результат %#v", res)
			}
			if !strings.Contains(err.Error(), "НесуществующийМетод") {
				t.Errorf("в ошибке нет имени метода: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("в ошибке нет типа значения (%s): %v", c.want, err)
			}
		})
	}
}

// Коллекции: методы есть, но не такой. Ошибка обязана перечислить доступные —
// ошибаются почти всегда в написании.
func TestМетод_УКоллекцииОшибкаСоСпискомДоступных(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"массив", `М = Новый Массив; М.Добавить(1); Возврат М.СовсемНетТакогоМетода(5);`, "Добавить"},
		{"структура", `С = Новый Структура; Возврат С.НетТакого();`, "Вставить"},
		{"соответствие", `С = Новый Соответствие; Возврат С.НетТакого();`, "Вставить"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := runSnippet(t, c.src)
			if err == nil {
				t.Fatalf("вызов несуществующего метода прошёл молча, результат %#v", res)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("в ошибке нет списка доступных методов: %v", err)
			}
		})
	}
}

// Обратная сторона: существующие методы обязаны работать как прежде. Без этого
// «починка» свелась бы к тому, что падает всё подряд.
func TestМетод_СуществующиеРаботаютКакПрежде(t *testing.T) {
	res, err := runSnippet(t, `
		М = Новый Массив;
		М.Добавить(1);
		М.Добавить(2);
		С = Новый Структура;
		С.Вставить("К", М.Количество());
		Возврат С.Свойство("К");`)
	if err != nil {
		t.Fatalf("рабочий код упал: %v", err)
	}
	if got := toFloat(res); got != 2 {
		t.Errorf("результат %#v, ждали 2", res)
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case interface{ InexactFloat64() float64 }:
		return x.InexactFloat64()
	}
	return -1
}
