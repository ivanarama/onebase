package ui

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestFormKeyboardBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("для проверки клавиатуры формы нужен node")
	}
	cmd := exec.Command(node, "--test", "static/form_keyboard_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node form keyboard test: %v\n%s", err, out)
	}
}

// tab_skip_readonly и autofocus формы доезжают до <form>: карточка открыта
// штатным обработчиком.
func TestManagedFormKeyboardAttrs(t *testing.T) {
	ent := layoutTestEntity(fieldEl("ПолеНаим", "Объект.Наименование"))
	ent.Forms[0].TabSkipReadonly = true
	ent.Forms[0].Autofocus = "first_empty"
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `data-ob-tab-skip-readonly="1" data-ob-autofocus="first_empty"`) {
		t.Fatalf("атрибуты клавиатуры формы не выведены:\n%s", body)
	}
}
