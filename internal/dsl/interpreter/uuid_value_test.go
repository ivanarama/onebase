package interpreter_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func runUUIDFunc(t *testing.T, src string) (any, error) {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "uuid.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var result any
	err = interpreter.New().RunWithResult(prog.Procedures[0], runtime.NewObject("Test", metadata.KindDocument), &result)
	return result, err
}

var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Новый УникальныйИдентификатор() — случайный v4, уникальный на каждом
// вызове (ключ идемпотентности на каждое нажатие «Позвонить»); Строка — в
// каноническом виде нижним регистром; ТипЗнч — УникальныйИдентификатор.
func TestUUIDValue_NewRandomV4(t *testing.T) {
	got, err := runUUIDFunc(t, `Функция Тест()
	Результат = Новый Массив;
	Для Н = 1 По 500 Цикл
		Ключ = Новый УникальныйИдентификатор();
		Если ТипЗнч(Ключ) <> Тип("УникальныйИдентификатор") Тогда
			ВызватьИсключение "ТипЗнч: " + ТипЗнч(Ключ);
		КонецЕсли;
		Результат.Добавить(Строка(Ключ));
	КонецЦикла;
	Возврат СтрСоединить(Результат, ",");
КонецФункции`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range strings.Split(got.(string), ",") {
		if !canonicalUUIDv4.MatchString(s) {
			t.Fatalf("не канонический v4 нижним регистром: %q", s)
		}
		if seen[s] {
			t.Fatalf("повтор идентификатора %q", s)
		}
		seen[s] = true
	}
	if len(seen) != 500 {
		t.Fatalf("ждали 500 разных, получили %d", len(seen))
	}
}

func TestUUIDValue_ParseCompareAndJSON(t *testing.T) {
	got, err := runUUIDFunc(t, `Функция Тест()
	А = Новый УникальныйИдентификатор("1D2C3B4A-5E6F-4A1B-9C2D-3E4F5A6B7C8D");
	Б = Новый УникальныйИдентификатор("1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	В = Новый УникальныйИдентификатор();
	Итог = Строка(А) + "|" + Строка(А = Б) + "|" + Строка(А <> В) + "|" + Строка(А = Строка(А));
	Итог = Итог + "|" + ЗаписатьJSON(Новый Структура("Ключ", А));
	Возврат Итог;
КонецФункции`)
	if err != nil {
		t.Fatal(err)
	}
	// Строка(Булево) — «true»/«false»; ключи Структуры в JSON — как их хранит
	// Структура (нижним регистром), это не часть УникальныйИдентификатор.
	want := `1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d|true|true|false|{"ключ":"1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d"}`
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestUUIDValue_BadStringIsClearError(t *testing.T) {
	for _, arg := range []string{`"не-идентификатор"`, `"{1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d}"`, `"urn:uuid:1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d"`, `15`} {
		_, err := runUUIDFunc(t, `Функция Тест()
	Возврат Новый УникальныйИдентификатор(`+arg+`);
КонецФункции`)
		if err == nil || !strings.Contains(err.Error(), "УникальныйИдентификатор") {
			t.Errorf("%s: ждали понятную ошибку, получили %v", arg, err)
		}
	}
}

// Хост получает значение и может вернуть его в DSL (параметр фонового
// задания): тип и текст сохраняются.
func TestUUIDValue_HostRoundTrip(t *testing.T) {
	id := uuid.MustParse("1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d")
	v := interpreter.NewUUIDValue(id)
	if v.UUID() != id || v.String() != id.String() || v.TypeName() != "УникальныйИдентификатор" {
		t.Fatalf("round trip: %v %s %s", v.UUID(), v.String(), v.TypeName())
	}
	if b, _ := v.MarshalJSON(); string(b) != `"1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d"` {
		t.Fatalf("JSON: %s", b)
	}
}

// Ссылка.УникальныйИдентификатор() остаётся строкой: на этом стоят
// существующие конфигурации. Из неё же строится значение нового типа.
func TestUUIDValue_RefMethodStaysString(t *testing.T) {
	prog, err := parser.New(lexer.New(`Функция Тест()
	С = Ссылка.УникальныйИдентификатор();
	Возврат ТипЗнч(С) + "|" + Строка(ТипЗнч(Новый УникальныйИдентификатор(С)) = Тип("УникальныйИдентификатор"));
КонецФункции`, "ref.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	ref := &interpreter.Ref{UUID: "1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d", Type: "Клиент"}
	var got any
	if err := interpreter.New().RunWithResult(prog.Procedures[0], nil, &got, map[string]any{"Ссылка": ref}); err != nil {
		t.Fatal(err)
	}
	if got != "Строка|true" {
		t.Fatalf("got %v", got)
	}
}

func TestUUIDValue_CollectionIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"map UUID first", `
	С = Новый Соответствие;
	С.Вставить(Ключ, "uuid"); С.Вставить(Текст, "string");
	С.Вставить(Копия, "updated");
	Возврат Строка(С.Количество()) + "|" + С.Получить(Ключ) + "|" + С.Получить(Текст);`, "2|updated|string"},
		{"map string first", `
	С = Новый Соответствие;
	С.Вставить(Текст, "string"); С.Вставить(Ключ, "uuid");
	С.Удалить(Копия);
	Возврат Строка(С.Количество()) + "|" + С.Получить(Текст) + "|" + Строка(С.Получить(Ключ) = Неопределено);`, "1|string|true"},
		{"array searches both types", `
	А = Новый Массив;
	А.Добавить(Ключ);
	Б = Новый Массив;
	Б.Добавить(Текст);
	Возврат ТипЗнч(А.Найти(Текст)) + "|" + ТипЗнч(Б.Найти(Ключ)) + "|" + Строка(А.Найти(Копия));`, "Неопределено|Неопределено|0"},
		{"value table lookup and sorting", `
	Т = Новый ТаблицаЗначений;
	Т.Колонки.Добавить("Ключ"); Т.Колонки.Добавить("Метка");
	Р = Т.Добавить(); Р.Ключ = Ключ; Р.Метка = "uuid";
	Р = Т.Добавить(); Р.Ключ = Текст; Р.Метка = "string";
	Р = Т.Добавить(); Р.Ключ = Другой; Р.Метка = "other";
	А = Т.НайтиСтроки(Новый Структура("Ключ", Копия));
	Б = Т.НайтиСтроки(Новый Структура("Ключ", Текст));
	Найден = Т.Найти(Текст, "Ключ");
	Т.Сортировать("Ключ Возр");
	Возврат Строка(А.Количество()) + "|" + А[0].Метка + "|" + Строка(Б.Количество()) + "|" + Б[0].Метка + "|" + Найден.Метка + "|" + Т.Получить(0).Метка + "|" + Т.Получить(1).Метка + "|" + Т.Получить(2).Метка;`, "1|uuid|1|string|string|string|uuid|other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runUUIDFunc(t, `Функция Тест()
	Ключ = Новый УникальныйИдентификатор("1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	Копия = Новый УникальныйИдентификатор("1D2C3B4A-5E6F-4A1B-9C2D-3E4F5A6B7C8D");
	Другой = Новый УникальныйИдентификатор("2d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	Текст = Строка(Ключ);
`+tc.body+`
КонецФункции`)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}

func TestUUIDValue_ReferenceCollectionCompatibility(t *testing.T) {
	prog, err := parser.New(lexer.New(`Функция Тест()
	С = Новый Соответствие;
	С.Вставить(Ссылка, "ref");
	Ключ = Новый УникальныйИдентификатор(Ссылка.УникальныйИдентификатор());
	С.Вставить(Ключ, "uuid");
	А = Новый Массив; А.Добавить(Ссылка);
	Возврат Строка(С.Количество()) + "|" + С.Получить(Ссылка.УникальныйИдентификатор()) + "|" + С.Получить("Клиент") + "|" + С.Получить(Ключ) + "|" + ТипЗнч(А.Найти(Ключ));
КонецФункции`, "uuid-ref.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	ref := &interpreter.Ref{UUID: "1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d", Name: "Клиент", Type: "Клиенты"}
	var got any
	if err := interpreter.New().RunWithResult(prog.Procedures[0], nil, &got, map[string]any{"Ссылка": ref}); err != nil {
		t.Fatal(err)
	}
	if got != "2|ref|ref|uuid|Неопределено" {
		t.Fatalf("reference/string compatibility changed: %v", got)
	}
}

type uuidConditionHook struct{ *condHook }

func (h *uuidConditionHook) HookCheckBreakpoint(file string, line int, cond func(string) (bool, error)) bool {
	if line != 5 {
		return false
	}
	return h.condHook.HookCheckBreakpoint(file, line, cond)
}

func TestUUIDValue_BreakpointConditions(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`ТипЗнч(Ключ) = Тип("УникальныйИдентификатор")`, true},
		{`Строка(Ключ) = Текст`, true},
		{`Ключ = Копия`, true},
		{`Ключ = Текст`, false},
		{`Ключ < Копия`, false},
		{`ЗаписатьJSON(Ключ) = ЗаписатьJSON(Текст)`, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			hook := &uuidConditionHook{condHook: &condHook{expr: tc.expr}}
			interp := interpreter.New()
			interp.DebugSource = func() interpreter.DebugHook { return hook }
			proc := parseProcFile(t, "uuid-cond.os", `Процедура Работа()
	Ключ = Новый УникальныйИдентификатор("1d2c3b4a-5e6f-4a1b-9c2d-3e4f5a6b7c8d");
	Копия = Новый УникальныйИдентификатор("1D2C3B4A-5E6F-4A1B-9C2D-3E4F5A6B7C8D");
	Текст = Строка(Ключ);
	Результат = 1;
КонецПроцедуры`)
			if err := interp.Run(proc, nil); err != nil {
				t.Fatal(err)
			}
			if !hook.asked.Load() || hook.err != nil || hook.stopped != tc.want {
				t.Fatalf("condition: asked=%v stopped=%v err=%v, want %v", hook.asked.Load(), hook.stopped, hook.err, tc.want)
			}
		})
	}
}
