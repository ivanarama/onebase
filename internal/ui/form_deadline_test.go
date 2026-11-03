package ui

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Поведение подсветки поля-срока — в браузере, проверяется node на настоящем
// коде managed.js.
func TestDeadlineBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("для проверки поля-срока нужен node")
	}
	cmd := exec.Command(node, "--test", "static/deadline_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node deadline test: %v\n%s", err, out)
	}
}

// deadline_warn доезжает до разметки поля: карточка открыта штатным
// обработчиком формы.
func TestManagedFormDeadlineWarnAttr(t *testing.T) {
	for _, typ := range []metadata.FieldType{metadata.FieldTypeString, metadata.FieldTypeDate} {
		t.Run(string(typ), func(t *testing.T) {
			el := fieldEl("ПолеСрок", "Объект.Комментарий")
			el.DeadlineWarn = 30
			ent := layoutTestEntity(el)
			for i := range ent.Fields {
				if ent.Fields[i].Name == "Комментарий" {
					ent.Fields[i].Type = typ
				}
			}
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
			rec := httptest.NewRecorder()
			srv.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("open form: status=%d body=%s", rec.Code, rec.Body.String())
			}
			input := regexp.MustCompile(`<input[^>]*name="Комментарий"[^>]*>`).FindString(rec.Body.String())
			if !strings.Contains(input, `data-ob-deadline="30"`) {
				t.Fatalf("у поля нет признака поля-срока: %s", input)
			}
			if typ == metadata.FieldTypeDate && !strings.Contains(input, `type="datetime-local"`) {
				t.Fatalf("ожидалось поле даты: %s", input)
			}
		})
	}
}
