package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/project"
	"golang.org/x/net/html"
)

// Поле со списком, варианты которого строит НачалоВыбора
// (ДобавитьЗначениеСписка), при открытии формы вариантов не имеет. Раньше
// <select> выводил только варианты, текущее значение не было выбрано, и
// «Записать» без открытия списка отправляло пустое — сохранённое «Что
// сделано» жалобы стиралось. Тест: YAML проекта → project.Load → GET формы
// записанного документа; выбранный вариант — то, что отправит браузер.
func TestChoiceListKeepsSavedValueWithoutOpeningList(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
	}
	write("documents/жалоба.yaml", "name: Жалоба\nfields:\n  - name: ЧтоСделано\n    type: string\n  - name: Статус\n    type: string\n")
	write("forms/жалоба/объекта.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Жалоба
elements:
  - kind: ПолеСписка
    name: ПолеЧтоСделано
    data_path: Объект.ЧтоСделано
    events: {НачалоВыбора: ЧтоСделаноНачалоВыбора}
  - kind: ПолеСписка
    name: ПолеСтатус
    data_path: Объект.Статус
    choices:
      - {value: Открыта}
      - {value: Закрыта}
`)
	write("forms/жалоба/объекта.form.os", `Процедура ЧтоСделаноНачалоВыбора()
	ДобавитьЗначениеСписка("Объяснительная");
	ДобавитьЗначениеСписка("Штраф");
КонецПроцедуры
`)
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	s, ctx := newSubmitTestServer(t, proj.Entities)
	id := uuid.New()
	// «Статус» хранит значение, которого больше нет среди статических вариантов.
	if err := s.store.Upsert(ctx, "Жалоба", id, map[string]any{"ЧтоСделано": "Объяснительная", "Статус": "В работе"}, s.reg.GetEntity("Жалоба")); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Жалоба")+"/"+id.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET form: %d %.600s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"ЧтоСделано": "Объяснительная", "Статус": "В работе"} {
		sel := findSelectByName(doc, field)
		if sel == nil {
			t.Fatalf("%s: no select", field)
		}
		selected, count := "", 0
		for o := sel.FirstChild; o != nil; o = o.NextSibling {
			if o.Type != html.ElementNode || o.Data != "option" {
				continue
			}
			if _, ok := managedHTMLAttr(o, "selected"); ok {
				v, _ := managedHTMLAttr(o, "value")
				selected = v
				count++
			}
		}
		if selected != want || count != 1 {
			t.Fatalf("%s: selected %q (%d selected options), want %q — «Записать» стёр бы значение", field, selected, count, want)
		}
	}
	// Значение, которое уже есть среди вариантов, не дублируется.
	if n := strings.Count(rec.Body.String(), `<option value="Закрыта"`); n != 1 {
		t.Fatalf("static option duplicated: %d", n)
	}
}
