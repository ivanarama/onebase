package interpreter_test

import (
	"testing"

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
