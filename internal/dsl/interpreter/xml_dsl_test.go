package interpreter_test

import (
	"math"
	"testing"

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
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	var result any
	return interp.RunWithResult(prog.Procedures[0], obj, &result, vars)
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
		Возврат XMLТипЗнч(42) = "decimal"
			И XMLТипЗнч("текст") = "string"
			И XMLТипЗнч(Истина) = "boolean"
			И XMLТипЗнч(ТекущаяДата()) = "dateTime";
	КонецПроцедуры`
	assert.Equal(t, true, evalXML(t, src))
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

	src = `Процедура Тест()
		Возврат XMLСтрока(Значение);
	КонецПроцедуры`
	err = evalXMLWithVarsError(t, src, map[string]any{
		"Значение": decimal.New(1, int32(2_147_483_647)),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decimal")
}

func TestDSL_XML_RejectsNonFiniteFloat(t *testing.T) {
	values := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, value := range values {
		for _, expression := range []string{
			"XMLСтрока(Значение)",
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
