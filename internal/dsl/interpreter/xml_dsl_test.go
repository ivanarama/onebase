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

func TestDSL_XML_LargeAttributeMapIsLinear(t *testing.T) {
	const attributeCount = 10_000
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

	src := `Процедура Тест()
		Дерево = ПрочитатьXML(Текст);
		Возврат ЗаписатьXML(Дерево.Атрибуты, "Корень");
	КонецПроцедуры`
	result, err := evalXMLWithVarsTimeout(t, src, map[string]any{"Текст": doc.String()}, 5*time.Second)
	require.NoError(t, err)
	out, ok := result.(string)
	require.True(t, ok, "ожидалась строка, получено %T", result)
	assert.True(t, strings.HasPrefix(out, "<Корень><a0>v0</a0>"), out[:min(len(out), 100)])
	assert.True(t, strings.HasSuffix(out, "<a9999>v9999</a9999></Корень>"))
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
