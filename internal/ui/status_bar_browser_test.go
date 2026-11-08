package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

func TestStatusBarBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprint(w, "ready")
	}))
	defer upstream.Close()
	s := newStatusBarTestServer(t)
	program, err := parser.New(lexer.New(fmt.Sprintf(`Функция Строка() Экспорт
 Ответ = HTTPПолучить("%s");
 Если Ответ.КодСостояния = 204 Тогда
  Возврат Неопределено;
 КонецЕсли;
 Возврат Новый Структура("Слева,Справа", "call active", "operator ready");
КонецФункции

Функция Нажатие(Кнопка) Экспорт
 Сообщить("status bar action message");
 Возврат "action complete";
КонецФункции`, upstream.URL), "telephony.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	s.reg.LoadModules(map[string]*ast.Program{"Телефония": program})
	if err := s.store.SaveNetworkEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	s.reg.HomePage().StatusBar.Interval = 1
	server := mountedStatusBarTestServer(t, s)
	browseracceptance.Run(t, "status-bar", map[string]string{"OB_ACCEPTANCE_URL": server.URL})
	if calls.Load() < 3 {
		t.Fatalf("visible/hidden/visible source calls incomplete: %d", calls.Load())
	}
}
