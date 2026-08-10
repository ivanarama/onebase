package interpreter_test

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Проверка идёт тем же путём, что и у пользователя: исходник модуля → лексер →
// парсер → интерпретатор. Вызов builtin-функций напрямую доказал бы только то,
// что функция написана, но не то, что она доступна из DSL.
func evalXML(t *testing.T, src string) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	err = interp.RunWithResult(prog.Procedures[0], obj, &result)
	require.NoError(t, err)
	return result
}

func evalXMLError(t *testing.T, src string) error {
	return evalXMLWithVarsError(t, src, nil)
}

func evalXMLWithVarsError(t *testing.T, src string, vars map[string]any) error {
	_, err := evalXMLWithVars(t, src, vars)
	return err
}

func evalXMLWithVars(t *testing.T, src string, vars map[string]any) (any, error) {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	err = interp.RunWithResult(prog.Procedures[0], obj, &result, vars)
	return result, err
}

func evalXMLWithVarsTimeout(t *testing.T, src string, vars map[string]any, timeout time.Duration) (any, error) {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	type outcome struct {
		result any
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		interp := interpreter.New()
		obj := runtime.NewObject("Test", metadata.KindDocument)
		var result any
		err := interp.RunWithResult(prog.Procedures[0], obj, &result, vars)
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		return got.result, got.err
	case <-time.After(timeout):
		t.Fatalf("публичный XML-путь не завершился за %s", timeout)
		return nil, nil
	}
}

type xmlPoisonStringer struct {
	called *bool
}

func (p *xmlPoisonStringer) String() string {
	*p.called = true
	return "<Корень/>"
}

func TestDSL_ReadXML_NameAttrsText(t *testing.T) {
	src := `Процедура Тест()
		Дерево = ПрочитатьXML("<Заказ Номер=""7""><Строка>Стол</Строка></Заказ>");
		Возврат Дерево.Имя = "Заказ"
			И Дерево.Атрибуты.Получить("Номер") = "7"
			И Дерево.Элементы[0].Текст = "Стол";
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
}

func TestDSL_XML_RoundTrip(t *testing.T) {
	src := `Процедура Тест()
		Исходный = "<Заказ Номер=""7""><Строка>Стол</Строка></Заказ>";
		Возврат ЗаписатьXML(ПрочитатьXML(Исходный)) = Исходный;
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
}

func TestDSL_WriteXML_FromStructure(t *testing.T) {
	src := `Процедура Тест()
		Товар = Новый Структура("Код, Наименование", 7, "Стол");
		Возврат ЗаписатьXML(Товар, "Товар");
	КонецПроцедуры`
	assert.Equal(t, "<Товар><код>7</код><наименование>Стол</наименование></Товар>", evalXML(t, src))
}

func TestDSL_XMLStringAndValue(t *testing.T) {
	src := `Процедура Тест()
		Возврат XMLСтрока(Истина) = "true" И XMLЗначение("Число", "42.5") = 42.5;
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
}

func TestDSL_FindDisallowedXMLCharacters(t *testing.T) {
	src := `Процедура Тест()
		Возврат НайтиНедопустимыеСимволыXML("обычный текст");
	КонецПроцедуры`
	assert.EqualValues(t, 0, evalXML(t, src))
}

// Английские псевдонимы должны работать так же — иначе конфигурации на латинице
// получат «неизвестную функцию» там, где для кириллицы всё в порядке.
func TestDSL_XML_EnglishAliases(t *testing.T) {
	src := `Процедура Тест()
		Возврат WriteXML(ReadXML("<a><b>1</b></a>")) = "<a><b>1</b></a>"
			И XMLString(Истина) = "true";
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
}

func TestDSL_XMLTypeOf(t *testing.T) {
	src := `Процедура Тест()
		Возврат XMLТипЗнч(Неопределено) = "undefined"
			И XMLТипЗнч(42) = "decimal"
			И XMLТипЗнч("текст") = "string"
			И XMLТипЗнч(Истина) = "boolean"
			И XMLТипЗнч(ТекущаяДата()) = "dateTime";
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
}

func TestDSL_XMLUndefinedRoundTripAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			name: "russian",
			src: `Процедура Тест()
				ИмяТипа = XMLТипЗнч(Неопределено);
				Возврат ИмяТипа = "undefined"
					И XMLЗначение(ИмяТипа, XMLСтрока(Неопределено)) = Неопределено
					И XMLЗначение("Неопределено", "") = Неопределено;
			КонецПроцедуры`,
		},
		{
			name: "english",
			src: `Procedure Test()
				TypeName = XMLTypeOf(Undefined);
				Return TypeName = "undefined"
					And XMLValue(TypeName, XMLString(Undefined)) = Undefined
					And XMLValue("nil", "") = Undefined;
			EndProcedure`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, true, evalXML(t, tc.src))
		})
	}
}

func TestDSL_XMLUndefinedRejectsAmbiguousInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		want       string
	}{
		{"missing value", `XMLТипЗнч()`, "ожидается 1 аргумент"},
		{"empty type", `XMLЗначение("", "")`, "неизвестный тип"},
		{"non-empty value", `XMLЗначение("undefined", "данные")`, "пустая строка"},
		{"whitespace value", `XMLЗначение("undefined", " ")`, "пустая строка"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `Процедура Тест()
				Возврат ` + tc.expression + `;
			КонецПроцедуры`
			err := evalXMLError(t, src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Негативные проверки идут через публичный DSL-путь, чтобы гарантировать, что
// строгие ошибки builtin действительно доходят до автора модуля, а не только
// работают при прямом вызове внутренней функции.
func TestDSL_XML_RejectsUnsupportedMixedContent(t *testing.T) {
	src := `Процедура Тест()
		Возврат ПрочитатьXML("<a>данные<b/></a>");
	КонецПроцедуры`
	err := evalXMLError(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "смешанное содержимое")
}

func TestDSL_XML_RejectsInjectedElementName(t *testing.T) {
	src := `Процедура Тест()
		Узел = Новый Структура;
		Узел.Вставить("Имя", "a></a><evil");
		Возврат ЗаписатьXML(Узел);
	КонецПроцедуры`
	err := evalXMLError(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "недопустимое имя")
}

func TestDSL_XML_RejectsEncodedWhitespaceOutsideRoot(t *testing.T) {
	for _, doc := range []string{
		`&#x20;<a/>`,
		`<a/>&#9;`,
		`<![CDATA[ ]]><a/>`,
	} {
		src := `Процедура Тест()
		Возврат ПрочитатьXML("` + doc + `");
	КонецПроцедуры`
		err := evalXMLError(t, src)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "вне корневого")
	}
}

func TestDSL_XML_RejectsUnboundedDecimal(t *testing.T) {
	src := `Процедура Тест()
		Возврат XMLЗначение("decimal", "1e2147483647");
	КонецПроцедуры`
	err := evalXMLError(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "без экспоненты")

	for _, expression := range []string{"XMLСтрока(Значение)", "XMLТипЗнч(Значение)"} {
		src = `Процедура Тест()
			Возврат ` + expression + `;
		КонецПроцедуры`
		err = evalXMLWithVarsError(t, src, map[string]any{
			"Значение": decimal.New(1, int32(2_147_483_647)),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decimal")
	}
}

func TestDSL_XML_RejectsNonFiniteFloat(t *testing.T) {
	values := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, value := range values {
		for _, expression := range []string{
			"XMLСтрока(Значение)",
			"XMLТипЗнч(Значение)",
			`ЗаписатьXML(Значение, "Число")`,
		} {
			src := `Процедура Тест()
			Возврат ` + expression + `;
		КонецПроцедуры`
			err := evalXMLWithVarsError(t, src, map[string]any{"Значение": value})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "конечным")
		}
	}
}

// Строковые XML-аргументы и XML-примитивы не должны обращаться к Stringer.
// Decimal.String разворачивает exponent, поэтому такой неявный вызов позволял
// маленькому значению занять гигантский объём памяти до проверки XML-лимитов.
func TestDSL_XML_NeverCoercesUntrustedStringer(t *testing.T) {
	cases := []string{
		"ПрочитатьXML(Значение)",
		`XMLЗначение(Значение, "1")`,
		`XMLЗначение("decimal", Значение)`,
		"НайтиНедопустимыеСимволыXML(Значение)",
		"XMLСтрока(Значение)",
		`ЗаписатьXML(Значение, "Корень")`,
		"XMLТипЗнч(Значение)",
	}
	for _, expression := range cases {
		t.Run(expression, func(t *testing.T) {
			called := false
			src := `Процедура Тест()
				Возврат ` + expression + `;
			КонецПроцедуры`
			_, err := evalXMLWithVarsTimeout(t, src, map[string]any{
				"Значение": &xmlPoisonStringer{called: &called},
			}, 2*time.Second)
			require.Error(t, err)
			assert.False(t, called, "XML-функция вызвала String() до проверки типа")
		})
	}
}

func xmlDocumentWithAttributes(attributeCount int) string {
	var doc strings.Builder
	doc.Grow(attributeCount * 18)
	doc.WriteString("<Корень")
	for i := 0; i < attributeCount; i++ {
		n := strconv.Itoa(i)
		doc.WriteString(" a")
		doc.WriteString(n)
		doc.WriteString(`="v`)
		doc.WriteString(n)
		doc.WriteByte('"')
	}
	doc.WriteString("/>")
	return doc.String()
}

func TestDSL_XML_LargeAttributeMapIsLinear(t *testing.T) {
	const attributeCount = 10_000

	src := `Процедура Тест()
		Дерево = ПрочитатьXML(Текст);
		Возврат ЗаписатьXML(Дерево.Атрибуты, "Корень");
	КонецПроцедуры`
	result, err := evalXMLWithVarsTimeout(t, src, map[string]any{"Текст": xmlDocumentWithAttributes(attributeCount)}, 5*time.Second)
	require.NoError(t, err)
	out, ok := result.(string)
	require.True(t, ok, "ожидалась строка, получено %T", result)
	assert.True(t, strings.HasPrefix(out, "<Корень><a0>v0</a0>"), out[:min(len(out), 100)])
	assert.True(t, strings.HasSuffix(out, "<a9999>v9999</a9999></Корень>"))
}

func TestDSL_XML_SharedSubtreeChecksTotalAttributesBeforeCopy(t *testing.T) {
	const attributeCount = 10_000
	src := `Процедура Тест()
		ОбщийУзел = ПрочитатьXML(Текст);
		Дети = Новый Массив;
		Для Индекс = 1 По 11 Цикл
			Дети.Добавить(ОбщийУзел);
		КонецЦикла;

		// Этот узел обязан остаться недостигнутым: общий предел превышается
		// на одиннадцатой ссылке на ОбщийУзел, до копирования её атрибутов.
		ПлохойУзел = Новый Структура;
		ПлохойУзел.Вставить("Имя", "Плохой");
		ПлохойУзел.Вставить("Текст", 1);
		Дети.Добавить(ПлохойУзел);

		Корень = Новый Структура;
		Корень.Вставить("Имя", "Корень");
		Корень.Вставить("Элементы", Дети);
		Возврат ЗаписатьXML(Корень);
	КонецПроцедуры`
	err := evalXMLWithVarsError(t, src, map[string]any{"Текст": xmlDocumentWithAttributes(attributeCount)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "общее число атрибутов XML")
}

func TestDSL_XML_SharedSubtreeChecksTextBudgetBeforeValidation(t *testing.T) {
	cases := []struct {
		name  string
		setup string
	}{
		{
			name: "node text",
			setup: `ОбщийУзел.Вставить("Имя", "Узел");
			ОбщийУзел.Вставить("Текст", БольшаяСтрока);`,
		},
		{
			name:  "node name",
			setup: `ОбщийУзел.Вставить("Имя", БольшаяСтрока);`,
		},
		{
			name: "attribute name",
			setup: `ОбщийУзел.Вставить("Имя", "Узел");
			Атрибуты = Новый Соответствие;
			Атрибуты.Вставить(БольшаяСтрока, "v");
			ОбщийУзел.Вставить("Атрибуты", Атрибуты);`,
		},
		{
			name: "attribute value",
			setup: `ОбщийУзел.Вставить("Имя", "Узел");
			Атрибуты = Новый Соответствие;
			Атрибуты.Вставить("a", БольшаяСтрока);
			ОбщийУзел.Вставить("Атрибуты", Атрибуты);`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `Процедура Тест()
				ОбщийУзел = Новый Структура;
				` + tc.setup + `
				Дети = Новый Массив;
				Для Индекс = 1 По 17 Цикл
					Дети.Добавить(ОбщийУзел);
				КонецЦикла;

				// До этого узла доходить нельзя: общий byte-budget уже исчерпан.
				ПлохойУзел = Новый Структура;
				ПлохойУзел.Вставить("Имя", "Плохой");
				ПлохойУзел.Вставить("Текст", 1);
				Дети.Добавить(ПлохойУзел);

				Корень = Новый Структура;
				Корень.Вставить("Имя", "Корень");
				Корень.Вставить("Элементы", Дети);
				Возврат ЗаписатьXML(Корень);
			КонецПроцедуры`
			_, err := evalXMLWithVarsTimeout(t, src, map[string]any{
				"БольшаяСтрока": strings.Repeat("a", 1<<20),
			}, 3*time.Second)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "объём текстовых данных XML")
			assert.NotContains(t, err.Error(), "должно быть строкой")
		})
	}
}

func TestDSL_XML_TreeTextBudgetExactBoundary(t *testing.T) {
	src := `Процедура Тест()
		Узел = Новый Структура;
		Узел.Вставить("Имя", "r");
		Узел.Вставить("Текст", Текст);
		Возврат ЗаписатьXML(Узел);
	КонецПроцедуры`
	_, err := evalXMLWithVars(t, src, map[string]any{
		// Имя "r" + текст занимают ровно логический budget 16 МиБ.
		"Текст": strings.Repeat("x", (16<<20)-1),
	})
	require.Error(t, err)
	// Логический budget пройден; следующий, фактический document budget
	// закономерно отклоняет добавившиеся <r></r>.
	assert.Contains(t, err.Error(), "размер XML")
	assert.NotContains(t, err.Error(), "объём текстовых данных XML")
}

func TestDSL_XML_NameStartAndNameCharRanges(t *testing.T) {
	valid := []string{
		"a\u00B7b",
		"\u200Croot",
		"a\u203Fb",
		"\u3001root",
		"\U00010000root",
	}
	for _, name := range valid {
		t.Run("valid "+name, func(t *testing.T) {
			src := `Процедура Тест()
				Текст = ЗаписатьXML("x", Имя);
				Возврат ПрочитатьXML(Текст).Имя;
			КонецПроцедуры`
			result, err := evalXMLWithVars(t, src, map[string]any{"Имя": name})
			require.NoError(t, err)
			assert.Equal(t, name, result)
		})
	}

	invalid := []string{
		"\u037Eroot",
		"\u200Broot",
		"\u3000root",
		"\uFDD0root",
		"\U000F0000root",
	}
	for _, name := range invalid {
		t.Run("invalid "+name, func(t *testing.T) {
			src := `Процедура Тест()
				Возврат ЗаписатьXML("x", Имя);
			КонецПроцедуры`
			_, err := evalXMLWithVars(t, src, map[string]any{"Имя": name})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "недопустимое имя")

			src = `Процедура Тест()
				Возврат ПрочитатьXML(Текст);
			КонецПроцедуры`
			_, err = evalXMLWithVars(t, src, map[string]any{"Текст": "<" + name + "/>"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "недопустимое имя")
		})
	}

	t.Run("attribute round trip", func(t *testing.T) {
		src := `Процедура Тест()
			Атрибуты = Новый Соответствие;
			Атрибуты.Вставить(ИмяАтрибута, "value");
			Узел = Новый Структура;
			Узел.Вставить("Имя", "root");
			Узел.Вставить("Атрибуты", Атрибуты);
			Обратно = ПрочитатьXML(ЗаписатьXML(Узел));
			Возврат Обратно.Атрибуты.Получить(ИмяАтрибута);
		КонецПроцедуры`
		result, err := evalXMLWithVars(t, src, map[string]any{"ИмяАтрибута": "a\u203Fb"})
		require.NoError(t, err)
		assert.Equal(t, "value", result)
	})
}

func TestDSL_XML_RejectsEscapedDocumentOverByteBudget(t *testing.T) {
	src := `Процедура Тест()
		Возврат ЗаписатьXML(Текст, "Корень");
	КонецПроцедуры`
	err := evalXMLWithVarsError(t, src, map[string]any{"Текст": strings.Repeat("&", 4<<20)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "размер XML")
}

func TestDSL_XML_DateTimeBounds(t *testing.T) {
	src := `Процедура Тест()
		Возврат XMLСтрока(Дата(10000, 1, 1));
	КонецПроцедуры`
	err := evalXMLError(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0000..9999")

	oddOffset := time.Date(2026, 8, 10, 12, 30, 0, 0, time.FixedZone("odd", 3*60*60+1))
	src = `Процедура Тест()
		Возврат XMLСтрока(Значение);
	КонецПроцедуры`
	err = evalXMLWithVarsError(t, src, map[string]any{"Значение": oddOffset})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "кратно минуте")

	for _, tc := range []struct {
		value string
		want  string
	}{
		{"2026-08-10T12:30:00+24:00", "-14:00..+14:00"},
		{"2026-08-10T12:30:00+14:01", "-14:00..+14:00"},
		{"2026-08-10T12:30:00-14:01", "-14:00..+14:00"},
		{"2026-08-10T12:30:00+00:60", "00..59"},
	} {
		src = `Процедура Тест()
			Возврат XMLЗначение("dateTime", "` + tc.value + `");
		КонецПроцедуры`
		err = evalXMLError(t, src)
		require.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}

	for _, value := range []string{
		"2026-08-10T12:30:00+14:00",
		"2026-08-10T12:30:00-14:00",
	} {
		src = `Процедура Тест()
			Возврат XMLСтрока(XMLЗначение("dateTime", Значение));
		КонецПроцедуры`
		result, err := evalXMLWithVars(t, src, map[string]any{"Значение": value})
		require.NoError(t, err)
		assert.Equal(t, value, result)
	}

	valid := time.Date(2026, 8, 10, 12, 30, 0, 123456789, time.FixedZone("MSK", 3*60*60))
	src = `Процедура Тест()
		Тип = XMLТипЗнч(Значение);
		Текст = XMLСтрока(Значение);
		Возврат XMLСтрока(XMLЗначение(Тип, Текст));
	КонецПроцедуры`
	result, err := evalXMLWithVars(t, src, map[string]any{"Значение": valid})
	require.NoError(t, err)
	assert.Equal(t, "2026-08-10T12:30:00.123456789+03:00", result)
}

func TestDSL_XML_DateTimeFractionNeverLosesPrecision(t *testing.T) {
	src := `Процедура Тест()
		Возврат XMLСтрока(XMLЗначение("dateTime", Значение));
	КонецПроцедуры`

	result, err := evalXMLWithVars(t, src, map[string]any{
		"Значение": "2026-08-10T12:30:00.123456789Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "2026-08-10T12:30:00.123456789Z", result)

	for _, value := range []string{
		"2026-08-10T12:30:00.1234567899Z",
		"2026-08-10T12:30:00,5Z",
	} {
		_, err := evalXMLWithVars(t, src, map[string]any{"Значение": value})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "дробн")
	}
}
