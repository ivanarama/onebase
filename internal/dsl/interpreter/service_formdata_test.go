package interpreter_test

// Заявка #1003: ФормаДанные разбирала тело безусловно как urlencoded — multipart
// падал англоязычным «invalid semicolon separator in query», а JSON молча
// разбирался в мусор.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// runServiceSrcErr — как runServiceSrc, но отдаёт ошибку вместо require.NoError.
func runServiceSrcErr(t *testing.T, src string, extra map[string]any) (any, error) {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	vars := interpreter.NewServiceFunctions()
	for k, v := range extra {
		vars[k] = v
	}
	interp := interpreter.New()
	var result any
	err = interp.RunWithResult(prog.Procedures[0], nil, &result, vars)
	return result, err
}

// formRequest собирает запрос с заданными Content-Type и телом.
func formRequest(contentType string, body []byte) map[string]any {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return map[string]any{
		"Запрос": interpreter.NewServiceRequest("POST", "api", "/hs/api/form", nil, nil, h, body),
	}
}

const formFieldSrc = `Функция Т()
  Данные = Запрос.ФормаДанные();
  Возврат Данные.Получить("имя");
КонецФункции`

func TestFormData_URLEncoded(t *testing.T) {
	extra := formRequest("application/x-www-form-urlencoded", []byte("имя=%D0%98%D0%B2%D0%B0%D0%BD+%D0%9F%D0%B5%D1%82%D1%80%D0%BE%D0%B2&x=1"))
	assert.Equal(t, "Иван Петров", runServiceSrc(t, formFieldSrc, extra))
}

// Пустой Content-Type трактуем как форму: так шлют простые клиенты и curl без
// -H, и ломать их из-за отсутствующего заголовка смысла нет.
func TestFormData_NoContentTypeStillParsed(t *testing.T) {
	extra := formRequest("", []byte("имя=Иван"))
	assert.Equal(t, "Иван", runServiceSrc(t, formFieldSrc, extra))
}

// Параметры Content-Type (charset) разбору не мешают.
func TestFormData_ContentTypeWithCharset(t *testing.T) {
	extra := formRequest("application/x-www-form-urlencoded; charset=UTF-8", []byte("имя=Иван"))
	assert.Equal(t, "Иван", runServiceSrc(t, formFieldSrc, extra))
}

// JSON раньше разбирался БЕЗ ошибки в мусор: url.ParseQuery честно считает
// «{"имя":"Иван"}» именем параметра, и конфигурация получала пустые поля.
func TestFormData_JSONBodyIsRejected(t *testing.T) {
	extra := formRequest("application/json", []byte(`{"имя":"Иван"}`))
	_, err := runServiceSrcErr(t, formFieldSrc, extra)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "application/json", "в сообщении должен быть фактический тип тела")
	assert.Contains(t, err.Error(), "ТелоJSON", "сообщение должно подсказывать правильный метод")
}

func multipartBody(t *testing.T, withFile bool) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("имя", "Иван Петров"))
	require.NoError(t, w.WriteField("комментарий", "нужен счёт; срочно"))
	if withFile {
		fw, err := w.CreateFormFile("документ", "счёт.pdf")
		require.NoError(t, err)
		_, err = fw.Write([]byte("%PDF-1.4 fake"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

// Форма без файлов (enctype multipart всё равно бывает) раньше падала с
// англоязычным «invalid semicolon separator in query» — из-за точек с запятой
// в Content-Disposition.
func TestFormData_MultipartTextFields(t *testing.T) {
	ct, body := multipartBody(t, false)
	extra := formRequest(ct, body)
	assert.Equal(t, "Иван Петров", runServiceSrc(t, formFieldSrc, extra))
	src := `Функция Т()
  Возврат Запрос.ФормаДанные().Получить("комментарий");
КонецФункции`
	assert.Equal(t, "нужен счёт; срочно", runServiceSrc(t, src, extra))
}

// Файловая часть — явная ошибка с именем поля: у файла нет текстового значения,
// а пустая строка выглядела бы как «поле пришло пустым».
func TestFormData_MultipartWithFileIsRejected(t *testing.T) {
	ct, body := multipartBody(t, true)
	_, err := runServiceSrcErr(t, formFieldSrc, formRequest(ct, body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "документ", "в сообщении должно быть имя файлового поля")
	assert.Contains(t, err.Error(), "файл")
}

func TestFormData_MultipartWithoutBoundary(t *testing.T) {
	_, err := runServiceSrcErr(t, formFieldSrc, formRequest("multipart/form-data", []byte("что-то")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boundary")
}
