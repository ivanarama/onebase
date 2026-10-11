package onec_forms_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func importCompatibilityModule(t *testing.T, body string) (*onec_forms.ImportReport, string) {
	t.Helper()
	dir := t.TempDir()
	bsl := filepath.Join(dir, "Module.bsl")
	source := "Функция Тест()\n" + body + "\nКонецФункции\n"
	if err := os.WriteFile(bsl, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := onec_forms.ImportFromOneC(onec_forms.ImportOptions{
		XMLPath: "fixtures/minimal-Form.xml", BSLPath: bsl,
		EntityName: "Тест", FormName: "Форма", FormKind: "object",
		DstYAMLPath: filepath.Join(dir, "test.form.yaml"), DstOSPath: filepath.Join(dir, "test.form.os"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report.ModulePath)
	if err != nil {
		t.Fatal(err)
	}
	return report, string(data)
}

func TestImportFromOneC_SupportedCompatibilitySignaturesExecute(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"raise_statement", `Попытка
ВызватьИсключение "ошибка";
Исключение
Возврат ОписаниеОшибки();
КонецПопытки;`, "ошибка"},
		{"raise_call", `Попытка
ВызватьИсключение("ошибка");
Исключение
Возврат ОписаниеОшибки();
КонецПопытки;`, "ошибка"},
		{"rethrow", `Попытка
Попытка
ВызватьИсключение "ошибка";
Исключение
ВызватьИсключение;
КонецПопытки;
Исключение
Возврат ОписаниеОшибки();
КонецПопытки;`, "ошибка"},
		{"nstr_default", `Возврат НСтр("ru='Привет';en='Hello'");`, "Привет"},
		{"nstr_language", `Возврат нСтР (
"ru='Привет';en='Hello'", // язык
"en");`, "Hello"},
		{"template_nested", `Возврат СтрШаблон("%1: %2", НСтр("ru='Текст, ()'"), СтрШаблон("%1", "да"));`, "Текст, (): да"},
		{"array", `А = Новый Массив; А.Добавить(7); Возврат Строка(А[0]);`, "7"},
		{"array_parentheses", `А = новый Массив (); Возврат Строка(А.Количество());`, "0"},
		{"map", `С = Новый Соответствие(); С.Вставить("А", 7); Возврат Строка(С.Получить("А"));`, "7"},
		{"structure", `С = Новый Структура("А,Б", СтрШаблон("%1", "да"), 7); Возврат С.А + Строка(С.Б);`, "да7"},
		{"structure_empty", `С = Новый Структура; С.Вставить("А", "да"); Возврат С.А;`, "да"},
		{"structure_dynamic_keys", `Имена = "А"; С = Новый Структура(Имена, "да"); Возврат С.А;`, "да"},
		{"structure_expression_keys", `С = Новый Структура(1 + "А", "да"); Возврат С.Свойство("1А");`, "да"},
		{"structure_member_keys", `С = Новый Структура(Новый Структура("Ключ", "А").Ключ, "да"); Возврат С.А;`, "да"},
		{"table", `Т = Новый ТаблицаЗначений(); Т.Колонки.Добавить("А"); Стр = Т.Добавить(); Стр.А = 7; Возврат Строка(Т.Получить(0).А);`, "7"},
		{"query", `З = Новый Запрос; З.Текст = "ВЫБРАТЬ 7 КАК Ответ"; Р = З.Выполнить(); Возврат Строка(Р[0].Ответ);`, "7"},
		{"query_parentheses", `З = Новый Запрос(); З.Текст = "ВЫБРАТЬ 7 КАК Ответ"; Р = З.Выполнить(); Возврат Строка(Р[0].Ответ);`, "7"},
		{"message", `Сообщить("Привет"); Возврат "да";`, "да"},
		{"transactions", `НачатьТранзакцию(); ЗафиксироватьТранзакцию(); НачатьТранзакцию(); ОтменитьТранзакцию(); Возврат "да";`, "да"},
		{"quoted_examples", `// Новый Массив(10); НСтр();
Возврат "Новый Массив(10); НСтр(); ВызватьИсключение(1, 2)";`, "Новый Массив(10); НСтр(); ВызватьИсключение(1, 2)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, source := importCompatibilityModule(t, tc.body)
			for _, w := range report.Warnings {
				if w.Code == onec_forms.W040_BSLNotInDSL {
					t.Errorf("supported signature: %s", w)
				}
			}
			prog, err := parser.New(lexer.New(source, report.ModulePath)).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			tx := interpreter.NewTxState(context.Background())
			extra := interpreter.NewTxFunctions(tx, db)
			extra["__factory_Запрос"] = interpreter.NewQueryFactory(context.Background(), db, runtime.NewRegistry())
			var messages []string
			extra["Сообщить"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
				messages = append(messages, fmt.Sprint(args[0]))
				return nil, nil
			})
			var result any
			if err := interpreter.New().RunWithResult(prog.Procedures[0], nil, &result, extra); err != nil {
				t.Fatal(err)
			}
			if result != tc.want {
				t.Fatalf("result = %#v, want %q", result, tc.want)
			}
			if tc.name == "message" && (len(messages) != 1 || messages[0] != "Привет") {
				t.Fatalf("messages = %v", messages)
			}
			if tx.HasOpen() {
				t.Fatal("transaction left open")
			}
		})
	}
}

func TestImportFromOneC_UnsupportedCompatibilitySignaturesWarn(t *testing.T) {
	cases := []struct{ name, body, field, suggestion string }{
		{"query_text", `З = Новый Запрос("ВЫБРАТЬ 7 КАК Ответ");`, "Новый Запрос", "Текст"},
		{"array_size", "А = новый\nМассив (\n10);", "Новый Массив", "размер"},
		{"array_dimensions", `А = Новый Массив(2, 3);`, "Новый Массив", "размер"},
		{"map_copy", `С = Новый Соответствие(Исходное);`, "Новый Соответствие", "без аргументов"},
		{"table_arguments", `Т = Новый ТаблицаЗначений(Колонки);`, "Новый ТаблицаЗначений", "без аргументов"},
		{"structure_copy", `С = Новый Структура(Новый Структура("А", 1));`, "Новый Структура", "строк"},
		{"structure_nonstring", `С = Новый Структура(42);`, "Новый Структура", "строк"},
		{"nstr_extra", `С = НСтр("ru='да'", "ru", 3);`, "НСтр(", "1–2"},
		{"nstr_empty", `С = НСтр();`, "НСтр(", "1–2"},
		{"nstr_missing_first", `С = НСтр(, "ru");`, "НСтр(", "1–2"},
		{"structure_missing_first", `С = Новый Структура(, 1);`, "Новый Структура", "строк"},
		{"template_empty", `С = СтрШаблон();`, "СтрШаблон(", "шаблон"},
		{"raise_extra", `ВызватьИсключение("ошибка", 2);`, "ВызватьИсключение", "описание"},
		{"message_status", `Сообщить("Привет", 1);`, "Сообщить(", "статус"},
		{"transaction_arguments", `НачатьТранзакцию(Уровень);`, "НачатьТранзакцию(", "без аргументов"},
		{"unsupported_constructor", `Х = Новый ХранилищеЗначения(1);`, "Новый ХранилищеЗначения", "отсутствует"},
		{"neighbor", `ПоказатьВопрос("да?");`, "ПоказатьВопрос(", "диалог"},
		{"unclosed_call", `А = Новый Массив(10;`, "Новый Массив", "размер"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, _ := importCompatibilityModule(t, tc.body)
			var warnings []onec_forms.Warning
			for _, w := range report.Warnings {
				if w.Code == onec_forms.W040_BSLNotInDSL {
					warnings = append(warnings, w)
				}
			}
			if len(warnings) != 1 {
				t.Fatalf("W040 = %+v, want one", warnings)
			}
			w := warnings[0]
			if w.Field != tc.field || w.Element != "Тест" || w.Line != 2 || w.Severity != onec_forms.SeverityWarn {
				t.Fatalf("warning location = %+v", w)
			}
			if !strings.Contains(w.Suggest, tc.suggestion) || strings.Contains(w.Suggest, "ВыброситьИсключение") {
				t.Fatalf("suggestion = %q", w.Suggest)
			}
		})
	}
}
