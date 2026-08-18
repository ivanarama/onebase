package interpreter_test

// Тесты плана 124. Всё проверяется исполнением DSL-кода — тем же путём, каким
// регекс использует конфигуратор, а не вызовом Go-функций напрямую.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func runRegexDSL(t *testing.T, src string) (any, error) {
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
	return result, err
}

func evalRegex(t *testing.T, src string) any {
	t.Helper()
	v, err := runRegexDSL(t, src)
	require.NoError(t, err)
	return v
}

func TestRegex_Matches(t *testing.T) {
	src := `Процедура Тест()
		Рег = Новый Регекс("^\+7\d{10}$");
		Возврат Рег.Совпадает("+79161234567") И Не Рег.Совпадает("8916123");
	КонецПроцедуры`
	assert.Equal(t, true, evalRegex(t, src))
}

func TestRegex_CaseSensitiveByDefault(t *testing.T) {
	src := `Процедура Тест()
		Рег = Новый Регекс("итого");
		Возврат Рег.Совпадает("ИТОГО");
	КонецПроцедуры`
	assert.Equal(t, false, evalRegex(t, src))
}

func TestRegex_Flags(t *testing.T) {
	t.Run("i — регистронезависимо", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс("итого", "i").Совпадает("ИТОГО: 100");
		КонецПроцедуры`
		assert.Equal(t, true, evalRegex(t, src))
	})

	t.Run("m — ^ на каждой строке", func(t *testing.T) {
		src := `Процедура Тест()
			Текст = "первая
вторая";
			Возврат Новый Регекс("^вторая$", "m").Совпадает(Текст);
		КонецПроцедуры`
		assert.Equal(t, true, evalRegex(t, src))
	})

	t.Run("s — точка ловит перенос", func(t *testing.T) {
		src := `Процедура Тест()
			Текст = "а
б";
			Возврат Новый Регекс("а.б", "s").Совпадает(Текст) И Не Новый Регекс("а.б").Совпадает(Текст);
		КонецПроцедуры`
		assert.Equal(t, true, evalRegex(t, src))
	})

	t.Run("неизвестный флаг — ошибка с указанием буквы", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс("а", "x").Совпадает("а");
		КонецПроцедуры`
		_, err := runRegexDSL(t, src)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "«x»")
	})
}

func TestRegex_Find(t *testing.T) {
	src := `Процедура Тест()
		Рег = Новый Регекс("(\d+)-(\d+)");
		М = Рег.Найти("код 12-34 конец");
		Возврат М.Значение + "|" + М.Позиция + "|" + М.Длина + "|" + М.Группы[1] + "|" + М.Группы[2];
	КонецПроцедуры`
	assert.Equal(t, "12-34|5|5|12|34", evalRegex(t, src))
}

// Позиция считается в РУНАХ и с единицы — как СтрНайти. Байтовое смещение Go
// на кириллице дало бы 13 вместо 8, и код, выглядящий корректным, молча
// разъехался бы с остальным DSL.
func TestRegex_PositionIsRuneBased(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("мир").Найти("Привет, мир!");
		Возврат М.Позиция = СтрНайти("Привет, мир!", "мир");
	КонецПроцедуры`
	assert.Equal(t, true, evalRegex(t, src))
}

func TestRegex_FindNotFoundReturnsUndefined(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("\d+").Найти("букв нет цифр");
		Возврат М = Неопределено;
	КонецПроцедуры`
	assert.Equal(t, true, evalRegex(t, src))
}

func TestRegex_FindAll(t *testing.T) {
	t.Run("количество и порядок", func(t *testing.T) {
		src := `Процедура Тест()
			Совпадения = Новый Регекс("\d+").НайтиВсе("1 22 333");
			Возврат Совпадения.Количество() + "|" + Совпадения[0].Значение + "|" + Совпадения[2].Значение;
		КонецПроцедуры`
		assert.Equal(t, "3|1|333", evalRegex(t, src))
	})

	t.Run("нет совпадений — пустой массив, не Неопределено", func(t *testing.T) {
		src := `Процедура Тест()
			Совпадения = Новый Регекс("\d+").НайтиВсе("букв нет цифр");
			Возврат Совпадения.Количество();
		КонецПроцедуры`
		assert.True(t, numEq(evalRegex(t, src), 0))
	})

	t.Run("явный предел соблюдается", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс("\d").НайтиВсе("12345", 2).Количество();
		КонецПроцедуры`
		assert.True(t, numEq(evalRegex(t, src), 2))
	})
}

func TestRegex_NamedGroups(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("(?P<год>\d{4})-(?P<месяц>\d{2})").Найти("2026-08");
		Возврат М.ГруппыПоИмени.Получить("год") + "/" + М.ГруппыПоИмени.Получить("месяц");
	КонецПроцедуры`
	assert.Equal(t, "2026/08", evalRegex(t, src))
}

// Латинские имена групп RE2 принимает как есть — подмена псевдонимами их не
// касается и не должна ломать.
func TestRegex_NamedGroupsASCII(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("(?P<year>\d{4})-(?P<month>\d{2})").Найти("2026-08");
		Возврат М.ГруппыПоИмени.Получить("year") + "/" + М.ГруппыПоИмени.Получить("month");
	КонецПроцедуры`
	assert.Equal(t, "2026/08", evalRegex(t, src))
}

// Смешанный шаблон: русское имя получает псевдоним, латинское остаётся своим.
func TestRegex_NamedGroupsMixed(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("(?P<код>[A-Z]{2})-(?P<num>\d+)").Найти("AB-1024");
		Возврат М.ГруппыПоИмени.Получить("код") + "/" + М.ГруппыПоИмени.Получить("num");
	КонецПроцедуры`
	assert.Equal(t, "AB/1024", evalRegex(t, src))
}

// Не участвовавшая в совпадении группа даёт пустую строку — конкатенация с ней
// не должна падать.
func TestRegex_UnmatchedGroupIsEmptyString(t *testing.T) {
	src := `Процедура Тест()
		М = Новый Регекс("(а)|(б)").Найти("а");
		Возврат "[" + М.Группы[2] + "]";
	КонецПроцедуры`
	assert.Equal(t, "[]", evalRegex(t, src))
}

func TestRegex_Replace(t *testing.T) {
	t.Run("нумерованная группа", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс("^8(\d{10})$").Заменить("89161234567", "+7$1");
		КонецПроцедуры`
		assert.Equal(t, "+79161234567", evalRegex(t, src))
	})

	t.Run("именованная группа", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс("(?P<имя>\w+)@example\.com").Заменить("ivan@example.com", "${имя}");
		КонецПроцедуры`
		assert.Equal(t, "ivan", evalRegex(t, src))
	})
}

func TestRegex_Split(t *testing.T) {
	t.Run("без предела", func(t *testing.T) {
		src := `Процедура Тест()
			Части = Новый Регекс("\s*[;,]\s*").Разделить("а, б;  в");
			Возврат Части.Количество() + "|" + Части[0] + Части[1] + Части[2];
		КонецПроцедуры`
		assert.Equal(t, "3|абв", evalRegex(t, src))
	})

	t.Run("с пределом", func(t *testing.T) {
		src := `Процедура Тест()
			Возврат Новый Регекс(",").Разделить("а,б,в", 2).Количество();
		КонецПроцедуры`
		assert.True(t, numEq(evalRegex(t, src), 2))
	})
}

func TestRegex_BadPattern(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый Регекс("(");
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Регекс")
}

// Ошибка компиляции — пользовательская, а не паника Go: ловится Попыткой.
func TestRegex_BadPatternCatchable(t *testing.T) {
	src := `Процедура Тест()
		Поймали = Ложь;
		Попытка
			Рег = Новый Регекс("(");
		Исключение
			Поймали = Истина;
		КонецПопытки;
		Возврат Поймали;
	КонецПроцедуры`
	assert.Equal(t, true, evalRegex(t, src))
}

func TestRegex_PatternLengthLimit(t *testing.T) {
	src := `Процедура Тест()
		Шаблон = "";
		Для Сч = 1 По 4100 Цикл
			Шаблон = Шаблон + "а";
		КонецЦикла;
		Возврат Новый Регекс(Шаблон).Совпадает("а");
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "длиннее")
}

// Слишком много совпадений — внятная ошибка, а не молча усечённый результат:
// усечение выглядит как корректный ответ с пропавшими строками.
func TestRegex_TooManyMatchesRaises(t *testing.T) {
	src := `Процедура Тест()
		Текст = "";
		Для Сч = 1 По 10001 Цикл
			Текст = Текст + "а";
		КонецЦикла;
		Возврат Новый Регекс("а").НайтиВсе(Текст).Количество();
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "предел")
}

func TestRegex_TooManyMatchesAllowedWithExplicitLimit(t *testing.T) {
	src := `Процедура Тест()
		Текст = "";
		Для Сч = 1 По 10001 Цикл
			Текст = Текст + "а";
		КонецЦикла;
		Возврат Новый Регекс("а").НайтиВсе(Текст, 5).Количество();
	КонецПроцедуры`
	assert.True(t, numEq(evalRegex(t, src), 5))
}

func TestRegexEscape(t *testing.T) {
	src := `Процедура Тест()
		Рег = Новый Регекс("^" + РегексЭкранировать("1.5*") + "$");
		Возврат Рег.Совпадает("1.5*") И Не Рег.Совпадает("175x");
	КонецПроцедуры`
	assert.Equal(t, true, evalRegex(t, src))
}

func TestRegex_PatternProperty(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый Регекс("\d+").Шаблон;
	КонецПроцедуры`
	assert.Equal(t, `\d+`, evalRegex(t, src))
}

// Объект обязан существовать в контексте БЕЗ инжектированных фабрик окружения
// (регламентное задание, procrun, HTTP-сервис). Тест фиксирует решение 1 плана:
// если регистрацию перенесут в «__factory_», он упадёт.
func TestRegex_AvailableWithoutInjectedFactories(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый Регекс("\d+").Совпадает("42");
	КонецПроцедуры`
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	require.NoError(t, interp.Run(prog.Procedures[0], obj, map[string]any{}))
}

// RE2 не поддерживает обратные ссылки и lookahead — это осознанный размен на
// гарантию линейного времени (защита публичного контура от ReDoS). Тест
// закрепляет ожидание: такой шаблон не компилируется, а не «работает иногда».
func TestRegex_RE2RejectsBacktrackingSyntax(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый Регекс("(?=абв)").Совпадает("абв");
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "Регекс"))
}
