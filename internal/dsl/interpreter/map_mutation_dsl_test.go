package interpreter_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func TestMapMutationsThroughDSL(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "XML after deletion",
			body: `
М = Новый Соответствие;
М.Вставить("a", "keep");
М.Вставить("b", "remove");
М.Вставить("c", "last");
М.Удалить("b");
Возврат ЗаписатьXML(М, "root");`,
			want: "<root><a>keep</a><c>last</c></root>",
		},
		{
			name: "mutable array key takes precedence by insertion order",
			body: `
Ключ = Новый Массив;
Ключ.Добавить("first");
М = Новый Соответствие;
М.Вставить(Ключ, "value");
М.Вставить("Массив[2]", "string key");
Ключ.Добавить("second");
Если НЕ М.СодержитКлюч(Ключ) ИЛИ М.Получить(Ключ) <> "value"
    ИЛИ М.Получить("Массив[2]") <> "value" Тогда
    Возврат "missing mutable key";
КонецЕсли;
М.Вставить(Ключ, "updated");
Если М.Количество() <> 2 ИЛИ М.Получить(Ключ) <> "updated" Тогда
    Возврат "duplicate mutable key";
КонецЕсли;
М.Удалить(Ключ);
Возврат М.Получить("Массив[2]");`,
			want: "string key",
		},
		{
			name: "iteration reads current values after append",
			body: `
М = Новый Соответствие;
М.Вставить("a", "old-a");
М.Вставить("b", "old-b");
Р = "";
Для Каждого Пара Из М Цикл
    Если Пара.Ключ = "a" Тогда
        М.Вставить("c", "new-c");
        М.Вставить("b", "new-b");
    КонецЕсли;
    Р = Р + Пара.Значение + ";";
КонецЦикла;
Возврат Р;`,
			want: "old-a;new-b;",
		},
		{
			name: "nested iteration survives compaction",
			body: `
М = Новый Соответствие;
М.Вставить("a", "a");
М.Вставить("b", "old-b");
М.Вставить("c", "c");
Р = "";
Для Каждого Внешняя Из М Цикл
    Если Внешняя.Ключ = "a" Тогда
        М.Вставить("d", "d");
        Для Каждого Внутренняя Из М Цикл
            Если Внутренняя.Ключ = "a" Тогда
                М.Удалить("a");
                М.Удалить("c");
                СтрокаJSON = ЗаписатьJSON(М);
                М.Вставить("b", "new-b");
            КонецЕсли;
            Р = Р + Внутренняя.Значение + ",";
        КонецЦикла;
    КонецЕсли;
    Р = Р + Внешняя.Значение + ";";
КонецЦикла;
Возврат Р;`,
			want: "a,new-b,d,a;new-b;",
		},
		{
			name: "clear ends active traversal",
			body: `
М = Новый Соответствие;
М.Вставить("a", "a");
М.Вставить("b", "b");
Р = "";
Для Каждого Пара Из М Цикл
    Р = Р + Пара.Значение;
    М.Очистить();
    М.Вставить("c", "c");
КонецЦикла;
Возврат Р + М.Получить("c");`,
			want: "ac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "Функция Тест()\n" + tt.body + "\nКонецФункции"
			program, err := parser.New(lexer.New(source, "map_mutation.os")).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			var result any
			if err := interpreter.New().RunWithResult(program.Procedures[0], nil, &result); err != nil {
				t.Fatal(err)
			}
			if result != tt.want {
				t.Fatalf("получено %q, ожидалось %q", result, tt.want)
			}
		})
	}
}
