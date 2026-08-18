package interpreter_test

// Тесты плана 125. Всё проверяется исполнением DSL-кода — тем же путём, каким
// шаблон применяет конфигурация.

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

func evalTemplate(t *testing.T, src string) string {
	t.Helper()
	v, err := runRegexDSL(t, src) // общий хелпер: разбор + исполнение первой процедуры
	require.NoError(t, err)
	s, ok := v.(string)
	require.Truef(t, ok, "ожидалась строка, получено %T", v)
	return s
}

// Главный тест плана: значение из данных не может стать разметкой.
func TestHTMLTemplate_Escaping(t *testing.T) {
	src := `Процедура Тест()
		Т = Новый ШаблонHTML("<h1>{{.Имя}}</h1>");
		Д = Новый Структура;
		Д.Вставить("Имя", "<script>alert(1)</script>");
		Возврат Т.Заполнить(Д);
	КонецПроцедуры`
	out := evalTemplate(t, src)
	assert.NotContains(t, out, "<script>")
	assert.Contains(t, out, "&lt;script&gt;")
}

// Одно и то же значение экранируется по-разному в тексте и в URL — этого
// нельзя добиться ручной функцией «экранировать строку».
func TestHTMLTemplate_ContextEscaping(t *testing.T) {
	src := `Процедура Тест()
		Т = Новый ШаблонHTML("<a href=""{{.Ссылка}}"">тут</a>");
		Д = Новый Структура;
		Д.Вставить("Ссылка", "javascript:alert(1)");
		Возврат Т.Заполнить(Д);
	КонецПроцедуры`
	out := evalTemplate(t, src)
	assert.NotContains(t, out, "javascript:alert(1)")
	assert.Contains(t, out, "ZgotmplZ") // html/template обезвредил URL
}

func TestHTMLTemplate_SafeHTML(t *testing.T) {
	t.Run("разметка сохраняется", func(t *testing.T) {
		src := `Процедура Тест()
			Т = Новый ШаблонHTML("<div>{{.Описание}}</div>");
			Д = Новый Структура;
			Д.Вставить("Описание", БезопасныйHTML("<b>жирный</b>"));
			Возврат Т.Заполнить(Д);
		КонецПроцедуры`
		assert.Contains(t, evalTemplate(t, src), "<b>жирный</b>")
	})

	t.Run("скрипт вырезается", func(t *testing.T) {
		src := `Процедура Тест()
			Т = Новый ШаблонHTML("<div>{{.Описание}}</div>");
			Д = Новый Структура;
			Д.Вставить("Описание", БезопасныйHTML("<b>ок</b><script>alert(1)</script>"));
			Возврат Т.Заполнить(Д);
		КонецПроцедуры`
		out := evalTemplate(t, src)
		assert.Contains(t, out, "<b>ок</b>")
		assert.NotContains(t, out, "alert(1)")
	})

	t.Run("обработчик события вырезается", func(t *testing.T) {
		src := `Процедура Тест()
			Т = Новый ШаблонHTML("<div>{{.Описание}}</div>");
			Д = Новый Структура;
			Д.Вставить("Описание", БезопасныйHTML("<img src=x onerror=alert(1)>"));
			Возврат Т.Заполнить(Д);
		КонецПроцедуры`
		assert.NotContains(t, evalTemplate(t, src), "onerror")
	})
}

// Структура DSL хранит ключи в нижнем регистре, а в шаблоне пишут
// «{{.Наименование}}» — имена полей нормализуются по дереву разбора.
func TestHTMLTemplate_RussianFieldNames(t *testing.T) {
	src := `Процедура Тест()
		Т = Новый ШаблонHTML("{{.Наименование}}|{{.ДатаПоставки}}");
		Д = Новый Структура;
		Д.Вставить("Наименование", "Насос");
		Д.Вставить("ДатаПоставки", "завтра");
		Возврат Т.Заполнить(Д);
	КонецПроцедуры`
	assert.Equal(t, "Насос|завтра", evalTemplate(t, src))
}

func TestHTMLTemplate_StructMapArray(t *testing.T) {
	t.Run("range по массиву структур", func(t *testing.T) {
		src := `Процедура Тест()
			Строки = Новый Массив;
			С1 = Новый Структура; С1.Вставить("Имя", "А"); Строки.Добавить(С1);
			С2 = Новый Структура; С2.Вставить("Имя", "Б"); Строки.Добавить(С2);
			Д = Новый Структура;
			Д.Вставить("Строки", Строки);
			Т = Новый ШаблонHTML("{{range .Строки}}<li>{{.Имя}}</li>{{end}}");
			Возврат Т.Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "<li>А</li><li>Б</li>", evalTemplate(t, src))
	})

	// Поля после переменной шаблона нормализуются так же, как после точки:
	// без этого «{{$стр.Имя}}» молча давал пустоту (missingkey=zero).
	t.Run("range с переменными", func(t *testing.T) {
		src := `Процедура Тест()
			Строки = Новый Массив;
			С1 = Новый Структура; С1.Вставить("Имя", "А"); Строки.Добавить(С1);
			С2 = Новый Структура; С2.Вставить("Имя", "Б"); Строки.Добавить(С2);
			Д = Новый Структура;
			Д.Вставить("Строки", Строки);
			Т = Новый ШаблонHTML("{{range $и, $стр := .Строки}}<li>{{$и}}:{{$стр.Имя}}</li>{{end}}");
			Возврат Т.Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "<li>0:А</li><li>1:Б</li>", evalTemplate(t, src))
	})

	t.Run("with с переменной", func(t *testing.T) {
		src := `Процедура Тест()
			Внутр = Новый Структура; Внутр.Вставить("Цена", "100");
			Д = Новый Структура; Д.Вставить("Товар", Внутр);
			Возврат Новый ШаблонHTML("{{with $т := .Товар}}{{$т.Цена}}{{end}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "100", evalTemplate(t, src))
	})

	t.Run("вложенная структура", func(t *testing.T) {
		src := `Процедура Тест()
			Внутр = Новый Структура; Внутр.Вставить("Цена", "100");
			Д = Новый Структура; Д.Вставить("Товар", Внутр);
			Возврат Новый ШаблонHTML("{{.Товар.Цена}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "100", evalTemplate(t, src))
	})

	t.Run("соответствие", func(t *testing.T) {
		src := `Процедура Тест()
			М = Новый Соответствие;
			М.Вставить("Заголовок", "Привет");
			Возврат Новый ШаблонHTML("{{.Заголовок}}").Заполнить(М);
		КонецПроцедуры`
		assert.Equal(t, "Привет", evalTemplate(t, src))
	})

	t.Run("условие", func(t *testing.T) {
		src := `Процедура Тест()
			Д = Новый Структура; Д.Вставить("Активен", Истина);
			Возврат Новый ШаблонHTML("{{if .Активен}}да{{else}}нет{{end}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "да", evalTemplate(t, src))
	})
}

// Пропуск необязательного поля не должен ронять страницу целиком.
func TestHTMLTemplate_MissingField(t *testing.T) {
	src := `Процедура Тест()
		Д = Новый Структура;
		Д.Вставить("Есть", "1");
		Возврат "[" + Новый ШаблонHTML("{{.НетТакого}}").Заполнить(Д) + "]";
	КонецПроцедуры`
	assert.Equal(t, "[]", evalTemplate(t, src))
}

func TestHTMLTemplate_Funcs(t *testing.T) {
	t.Run("число с разделителем разрядов", func(t *testing.T) {
		src := `Процедура Тест()
			Д = Новый Структура;
			Д.Вставить("Сумма", 1234567.5);
			Возврат Новый ШаблонHTML("{{число .Сумма 2}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "1 234 567.50", evalTemplate(t, src))
	})

	// Реквизит типа date на SQLite приходит строкой: пустой <time> в вёрстке
	// вместо даты — дефект, который видно только в исходнике шаблона.
	t.Run("дата строкой из базы", func(t *testing.T) {
		src := `Процедура Тест()
			Д = Новый Структура;
			Д.Вставить("Дата", "2026-08-17 00:00:00");
			Возврат Новый ШаблонHTML("{{дата .Дата ""02.01.2006""}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "17.08.2026", evalTemplate(t, src))
	})

	t.Run("неразобранная строка отдаётся как есть", func(t *testing.T) {
		src := `Процедура Тест()
			Д = Новый Структура;
			Д.Вставить("Дата", "скоро");
			Возврат Новый ШаблонHTML("{{дата .Дата ""02.01.2006""}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "скоро", evalTemplate(t, src))
	})

	t.Run("дата по образцу", func(t *testing.T) {
		src := `Процедура Тест()
			Д = Новый Структура;
			Д.Вставить("Дата", Дата(2026, 8, 17));
			Возврат Новый ШаблонHTML("{{дата .Дата ""02.01.2006""}}").Заполнить(Д);
		КонецПроцедуры`
		assert.Equal(t, "17.08.2026", evalTemplate(t, src))
	})
}

func TestHTMLTemplate_ParseError(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый ШаблонHTML("{{if .А}}без конца");
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ШаблонHTML")
}

func TestHTMLTemplate_ParseErrorCatchable(t *testing.T) {
	src := `Процедура Тест()
		Поймали = Ложь;
		Попытка
			Т = Новый ШаблонHTML("{{if .А}}без конца");
		Исключение
			Поймали = Истина;
		КонецПопытки;
		Возврат Поймали;
	КонецПроцедуры`
	v, err := runRegexDSL(t, src)
	require.NoError(t, err)
	assert.Equal(t, true, v)
}

func TestHTMLTemplate_SourceTooLarge(t *testing.T) {
	src := `Процедура Тест()
		Кусок = "";
		Для Сч = 1 По 1000 Цикл
			Кусок = Кусок + "0123456789";
		КонецЦикла;
		Текст = "";
		Для Сч = 1 По 110 Цикл
			Текст = Текст + Кусок;
		КонецЦикла;
		Возврат Новый ШаблонHTML(Текст).Заполнить(Новый Структура);
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "больше")
}

// Результат сверх предела — ошибка, а не съеденная память процесса.
func TestHTMLTemplate_OutputTooLarge(t *testing.T) {
	src := `Процедура Тест()
		Строки = Новый Массив;
		Кусок = "";
		Для Сч = 1 По 1000 Цикл
			Кусок = Кусок + "0123456789";
		КонецЦикла;
		Для Сч = 1 По 2000 Цикл
			Строки.Добавить(Кусок);
		КонецЦикла;
		Д = Новый Структура;
		Д.Вставить("Строки", Строки);
		Возврат Новый ШаблонHTML("{{range .Строки}}{{.}}{{end}}").Заполнить(Д);
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "больше")
}

// Структура, ссылающаяся на себя, обрывается по циклу, а не вешает рендер.
func TestHTMLTemplate_Cycle(t *testing.T) {
	src := `Процедура Тест()
		Д = Новый Структура;
		Д.Вставить("Имя", "верх");
		Д.Вставить("Сам", Д);
		Возврат Новый ШаблонHTML("{{.Имя}}").Заполнить(Д);
	КонецПроцедуры`
	assert.Equal(t, "верх", evalTemplate(t, src))
}

// Объект обязан существовать в контексте без инжектированных фабрик окружения
// (регламентное задание, procrun, HTTP-сервис).
func TestHTMLTemplate_AvailableWithoutInjectedFactories(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый ШаблонHTML("<p>{{.А}}</p>").Заполнить(Новый Структура);
	КонецПроцедуры`
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	require.NoError(t, interp.Run(prog.Procedures[0], obj, map[string]any{}))
}

// Шаблон не должен уметь подтягивать файлы: он приходит строкой из справочника
// или модуля, а не из файловой системы.
func TestHTMLTemplate_NoFileAccess(t *testing.T) {
	src := `Процедура Тест()
		Возврат Новый ШаблонHTML("{{template ""внешний""}}").Заполнить(Новый Структура);
	КонецПроцедуры`
	_, err := runRegexDSL(t, src)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "ШаблонHTML"))
}
