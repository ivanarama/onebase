package interpreter_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runServiceSrc(t *testing.T, src string, extra map[string]any) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	vars := interpreter.NewServiceFunctions()
	for k, v := range extra {
		vars[k] = v
	}

	interp := interpreter.New()
	var result any
	err = interp.RunWithResult(prog.Procedures[0], nil, &result, vars)
	require.NoError(t, err)
	return result
}

func TestServiceResponse_New(t *testing.T) {
	src := `Функция Тест()
  Ответ = Новый HTTPСервисОтвет(201);
  Ответ.УстановитьЗаголовок("X-Test", "yes");
  Ответ.УстановитьТелоИзСтроки("привет");
  Возврат Ответ;
КонецФункции`
	result := runServiceSrc(t, src, nil)
	resp, ok := result.(*interpreter.DSLServiceResponse)
	require.True(t, ok, "ожидается *DSLServiceResponse, получили %T", result)
	assert.Equal(t, 201, resp.StatusCodeValue())
	assert.Equal(t, "привет", string(resp.BodyBytes()))
	assert.Equal(t, "yes", resp.HeadersMap()["X-Test"])
}

// Тело ответа читается из DSL: без этого обработчик сервиса нечем проверить
// конфигурационным тестом — видно код состояния, но не разметку, которую
// получит посетитель.
func TestServiceResponse_ReadBody(t *testing.T) {
	src := `Функция Тест()
  Ответ = Новый HTTPСервисОтвет(200);
  Ответ.УстановитьТелоИзСтроки("<h1>Привет</h1>");
  Возврат Ответ.ПолучитьТелоКакСтроку();
КонецФункции`
	assert.Equal(t, "<h1>Привет</h1>", runServiceSrc(t, src, nil))
}

func TestServiceResponse_ReadBody_JSONAndEmpty(t *testing.T) {
	src := `Функция Тест()
  Пустой = Новый HTTPСервисОтвет(204);
  Ответ = Новый HTTPСервисОтвет(200);
  Ответ.УстановитьТелоJSON(Новый Структура("сумма", 100));
  Возврат Ответ.ПолучитьТелоКакСтроку() + "|" + Пустой.ПолучитьТелоКакСтроку();
КонецФункции`
	assert.Equal(t, `{"сумма":100}|`, runServiceSrc(t, src, nil))
}

func TestResponseJSON_Shorthand(t *testing.T) {
	src := `Функция Тест()
  стр = Новый Структура;
  стр.Вставить("ок", Истина);
  стр.Вставить("n", 7);
  Возврат ОтветJSON(200, стр);
КонецФункции`
	result := runServiceSrc(t, src, nil)
	resp, ok := result.(*interpreter.DSLServiceResponse)
	require.True(t, ok)
	assert.Equal(t, 200, resp.StatusCodeValue())
	assert.JSONEq(t, `{"ок":true,"n":7}`, string(resp.BodyBytes()))
	assert.Contains(t, resp.HeadersMap()["Content-Type"], "application/json")
}

func TestResponseText_Shorthand(t *testing.T) {
	src := `Функция Тест()
  Возврат ОтветТекст(404, "нет", "text/plain");
КонецФункции`
	result := runServiceSrc(t, src, nil)
	resp := result.(*interpreter.DSLServiceResponse)
	assert.Equal(t, 404, resp.StatusCodeValue())
	assert.Equal(t, "нет", string(resp.BodyBytes()))
	assert.Equal(t, "text/plain", resp.HeadersMap()["Content-Type"])
}

func TestServiceRequest_Accessors(t *testing.T) {
	req := interpreter.NewServiceRequest(
		"POST", "api", "/hs/api/orders/42",
		map[string]string{"id": "42"},
		url.Values{"q": {"abc"}},
		http.Header{"X-Token": {"secret"}},
		[]byte(`{"сумма":100}`),
	)
	extra := map[string]any{"Запрос": req}

	assert.Equal(t, "POST", runServiceSrc(t, `Функция Т() Возврат Запрос.Метод; КонецФункции`, extra))
	assert.Equal(t, "42", runServiceSrc(t, `Функция Т() Возврат Запрос.ПараметрыURL.Получить("id"); КонецФункции`, extra))
	assert.Equal(t, "abc", runServiceSrc(t, `Функция Т() Возврат Запрос.ПараметрЗапроса("q"); КонецФункции`, extra))
	assert.Equal(t, "secret", runServiceSrc(t, `Функция Т() Возврат Запрос.ПолучитьЗаголовок("X-Token"); КонецФункции`, extra))
	assert.Equal(t, `{"сумма":100}`, runServiceSrc(t, `Функция Т() Возврат Запрос.ПолучитьТелоКакСтроку(); КонецФункции`, extra))
	// ТелоJSON разбирает тело в Структуру/Соответствие
	assert.Equal(t, int64(100), runServiceSrc(t, `Функция Т() тело = Запрос.ТелоJSON(); Возврат тело.Получить("сумма"); КонецФункции`, extra))
}

func TestSetBodyJSON_SetsContentType(t *testing.T) {
	src := `Функция Тест()
  Ответ = Новый HTTPСервисОтвет(200);
  м = Новый Соответствие;
  м.Вставить("a", 1);
  Ответ.УстановитьТелоJSON(м);
  Возврат Ответ;
КонецФункции`
	resp := runServiceSrc(t, src, nil).(*interpreter.DSLServiceResponse)
	assert.JSONEq(t, `{"a":1}`, string(resp.BodyBytes()))
	assert.Contains(t, resp.HeadersMap()["Content-Type"], "application/json")
}
