package interpreter

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func evalBreakFunc(t *testing.T, code string, extra ...map[string]any) any {
	t.Helper()
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	proc := prog.Procedures[0]
	i := New()
	this := &MapThis{M: map[string]any{}}
	var result any
	if err := i.RunWithResult(proc, this, &result, extra...); err != nil {
		t.Fatalf("run: %v", err)
	}
	return result
}

func TestBreak_ForEach(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  сумма = 0;
  М = Новый Массив;
  М.Добавить(1);
  М.Добавить(2);
  М.Добавить(3);
  М.Добавить(4);
  Для Каждого Э Из М Цикл
    Если Э = 3 Тогда
      Прервать;
    КонецЕсли;
    сумма = сумма + Э;
  КонецЦикла;
  Возврат сумма;
КонецФункции`)
	if !numEq(result, 3) {
		t.Errorf("expected 3, got %v", result)
	}
}

func TestBreak_NumericFor(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  Сумма = 0;
  Для i = 1 По 10 Цикл
    Если i > 3 Тогда
      Прервать;
    КонецЕсли;
    Сумма = Сумма + i;
  КонецЦикла;
  Возврат Сумма;
КонецФункции`)
	if !numEq(result, 6) {
		t.Errorf("expected 6, got %v", result)
	}
}

func TestContinue_ForEach(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  сумма = 0;
  М = Новый Массив;
  М.Добавить(1);
  М.Добавить(2);
  М.Добавить(3);
  Для Каждого Э Из М Цикл
    Если Э = 2 Тогда
      Продолжить;
    КонецЕсли;
    сумма = сумма + Э;
  КонецЦикла;
  Возврат сумма;
КонецФункции`)
	if !numEq(result, 4) {
		t.Errorf("expected 4, got %v", result)
	}
}

func TestContinue_NumericFor(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  сумма = 0;
  Для i = 1 По 5 Цикл
    Если i = 3 Тогда
      Продолжить;
    КонецЕсли;
    сумма = сумма + i;
  КонецЦикла;
  Возврат сумма;
КонецФункции`)
	if !numEq(result, 12) {
		t.Errorf("expected 12, got %v", result)
	}
}

func TestBreak_InsideIf(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  Результат = "";
  М = Новый Массив;
  М.Добавить("A");
  М.Добавить("B");
  М.Добавить("C");
  Для Каждого Э Из М Цикл
    Если Э = "B" Тогда
      Прервать;
    КонецЕсли;
    Результат = Результат + Э;
  КонецЦикла;
  Результат = Результат + "!";
  Возврат Результат;
КонецФункции`)
	if result != "A!" {
		t.Errorf("expected A!, got %v", result)
	}
}

func TestBreak_FIFO(t *testing.T) {
	// Simulates the FIFO posting pattern from trade config
	result := evalBreakFunc(t, `Функция Тест()
  Списано = 0;
  Остаток = 1;
  Партии = Новый Массив;
  Пар = Новый Структура("КоличествоОстаток,СуммаОстаток", 2, 600);
  Партии.Добавить(Пар);
  Пар2 = Новый Структура("КоличествоОстаток,СуммаОстаток", 2, 200);
  Партии.Добавить(Пар2);
  Для Каждого Партия Из Партии Цикл
    Если Остаток <= 0 Тогда
      Прервать;
    КонецЕсли;
    КолВоИзПартии = Мин(Остаток, Партия.КоличествоОстаток);
    Списано = Списано + КолВоИзПартии;
    Остаток = Остаток - КолВоИзПартии;
  КонецЦикла;
  Возврат Списано;
КонецФункции`)
	if !numEq(result, 1) {
		t.Errorf("expected 1, got %v", result)
	}
}

func TestBreak_InsideTry(t *testing.T) {
	result := evalBreakFunc(t, `Функция Тест()
  Сумма = 0;
  М = Новый Массив;
  М.Добавить(10);
  М.Добавить(20);
  Для Каждого Э Из М Цикл
    Попытка
      Если Э = 10 Тогда
        Прервать;
      КонецЕсли;
      Сумма = Сумма + Э;
    Исключение
      Сумма = -1;
    КонецПопытки;
  КонецЦикла;
  Возврат Сумма;
КонецФункции`)
	if !numEq(result, 0) {
		t.Errorf("expected 0, got %v", result)
	}
}
