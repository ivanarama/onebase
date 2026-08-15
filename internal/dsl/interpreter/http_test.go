package interpreter_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runHTTPSrc(t *testing.T, src string, extra map[string]any) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	var result any
	err = interp.RunWithResult(prog.Procedures[0], nil, &result, extra)
	require.NoError(t, err)
	return result
}

func runHTTPSrcError(t *testing.T, src string, extra map[string]any) error {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)
	var result any
	return interpreter.New().RunWithResult(prog.Procedures[0], nil, &result, extra)
}

func TestHTTPBuiltins_ExecutionContextCancelsBlockingRequest(t *testing.T) {
	for _, connection := range []bool{false, true} {
		name := "shorthand"
		if connection {
			name = "connection"
		}
		t.Run(name, func(t *testing.T) {
			requestCanceled := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
					close(requestCanceled)
				case <-time.After(4 * time.Second):
					_, _ = fmt.Fprint(w, "too late")
				}
			}))
			defer srv.Close()

			src := fmt.Sprintf(`Procedure Test()
  HTTPGet("%s");
EndProcedure`, srv.URL)
			if connection {
				src = fmt.Sprintf(`Procedure Test()
  Connection = New HTTPConnection("%s");
  Request = New HTTPRequest("/");
  Connection.Get(Request);
EndProcedure`, srv.Listener.Addr().String())
			}

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			started := time.Now()
			err := runHTTPSrcError(t, src, interpreter.NewHTTPFunctions(nil, interpreter.NewStaticCtx(ctx)))
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), "врем")
			assert.Less(t, time.Since(started), 3*time.Second)
			select {
			case <-requestCanceled:
			case <-time.After(time.Second):
				t.Fatal("HTTP request context was not canceled")
			}
		})
	}
}

func TestHTTPGet_Shorthand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	src := fmt.Sprintf(`Функция Тест()
  Ответ = HTTPПолучить("%s/data");
  Возврат Ответ.КодСостояния;
КонецФункции`, srv.URL)

	result := runHTTPSrc(t, src, interpreter.NewHTTPFunctions(nil))
	assert.Equal(t, float64(200), result)
}

func TestHTTPConnection_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rates", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		_, _ = fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	src := fmt.Sprintf(`Функция Тест()
  Соединение = Новый HTTPСоединение("%s");
  Запрос = Новый HTTPЗапрос("/rates");
  Ответ = Соединение.Получить(Запрос);
  Возврат Ответ.ПолучитьТелоКакСтроку();
КонецФункции`, host)

	result := runHTTPSrc(t, src, interpreter.NewHTTPFunctions(nil))
	assert.Equal(t, "hello", result)
}

func TestHTTPConnection_Post(t *testing.T) {
	var gotBody string
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		gotHeader = r.Header.Get("Content-Type")
		w.WriteHeader(201)
		_, _ = fmt.Fprint(w, `{"created":true}`)
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	src := fmt.Sprintf(`Функция Тест()
  Соединение = Новый HTTPСоединение("%s");
  Запрос = Новый HTTPЗапрос("/orders");
  Запрос.УстановитьЗаголовок("Content-Type", "application/json");
  Запрос.УстановитьТелоИзСтроки("{""status"":""new""}");
  Ответ = Соединение.ОтправитьДля(Запрос, "POST");
  Возврат Ответ.КодСостояния;
КонецФункции`, host)

	result := runHTTPSrc(t, src, interpreter.NewHTTPFunctions(nil))
	assert.Equal(t, float64(201), result)
	assert.Equal(t, `{"status":"new"}`, gotBody)
	assert.Equal(t, "application/json", gotHeader)
}

func TestHTTPResponse_GetHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "abc123")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	src := fmt.Sprintf(`Функция Тест()
  Ответ = HTTPПолучить("%s");
  Возврат Ответ.ПолучитьЗаголовок("X-Request-Id");
КонецФункции`, srv.URL)

	result := runHTTPSrc(t, src, interpreter.NewHTTPFunctions(nil))
	assert.Equal(t, "abc123", result)
}

func TestHTTPGet_WithJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"rate":75}`)
	}))
	defer srv.Close()

	src := fmt.Sprintf(`Функция Тест()
  Ответ = HTTPПолучить("%s/v1/rates");
  Если Ответ.КодСостояния = 200 Тогда
    данные = ПрочитатьJSON(Ответ.ПолучитьТелоКакСтроку());
    Возврат данные.Получить("rate");
  КонецЕсли;
  Возврат 0;
КонецФункции`, srv.URL)

	result := runHTTPSrc(t, src, interpreter.NewHTTPFunctions(nil))
	assert.Equal(t, int64(75), result)
}
