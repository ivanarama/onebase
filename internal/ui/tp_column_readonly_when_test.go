package ui

import (
	"encoding/json"
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

// readonly_when у колонки табличной части (Колонка внутри ТабличнаяЧасть)
// раньше не доходило до сетки: колонка оставалась редактируемой. Теперь
// колонка сетки несёт readOnly (вычисленное при отрисовке) и path элемента —
// по нему ответ события формы присылает новое состояние. Тест: YAML проекта →
// project.Load → GET формы записанного документа и POST form-event.
func tpReadonlyWhenProject(t *testing.T) *project.Project {
	t.Helper()
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
	write("documents/заказ.yaml", `name: Заказ
fields:
  - name: Статус
    type: string
tableparts:
  - name: Товары
    fields:
      - name: Наименование
        type: string
      - name: Количество
        type: number
`)
	write("forms/заказ/объекта.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеСтатус
    data_path: Объект.Статус
    events: {ПриИзменении: СтатусПриИзменении}
  - kind: ТабличнаяЧасть
    name: ТабТовары
    data_path: Объект.Товары
    children:
      - kind: Колонка
        name: КолНаименование
        data_path: Объект.Товары.Наименование
      - kind: Колонка
        name: КолКоличество
        data_path: Объект.Товары.Количество
        readonly_when: 'Статус = "Закрыт"'
`)
	write("forms/заказ/объекта.form.os", "Процедура СтатусПриИзменении()\nКонецПроцедуры\n")
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	return proj
}

type tpColumnState struct {
	ID       string `json:"id"`
	ReadOnly bool   `json:"readOnly"`
	Path     string `json:"path"`
}

func renderedTPColumns(t *testing.T, page string) map[string]tpColumnState {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if raw != "" {
			return
		}
		if n.Type == html.ElementNode {
			if v, ok := managedHTMLAttr(n, "data-sg-cols"); ok {
				if tp, _ := managedHTMLAttr(n, "data-sg-tp"); tp == "Товары" {
					raw = v
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if raw == "" {
		t.Fatalf("no grid for Товары: %.600s", page)
	}
	var cols []tpColumnState
	if err := json.Unmarshal([]byte(raw), &cols); err != nil {
		t.Fatalf("data-sg-cols: %v (%s)", err, raw)
	}
	out := map[string]tpColumnState{}
	for _, c := range cols {
		out[c.ID] = c
	}
	return out
}

func TestTablePartColumnReadonlyWhenThroughHTTP(t *testing.T) {
	proj := tpReadonlyWhenProject(t)
	s, ctx := newSubmitTestServer(t, proj.Entities)
	router := chi.NewRouter()
	s.Mount(router)
	ent := s.reg.GetEntity("Заказ")

	var qtyPath string
	for _, tc := range []struct {
		status string
		locked bool
	}{{"Открыт", false}, {"Закрыт", true}} {
		id := uuid.New()
		if err := s.store.Upsert(ctx, "Заказ", id, map[string]any{"Статус": tc.status}, ent); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Заказ")+"/"+id.String(), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %.600s", tc.status, rec.Code, rec.Body.String())
		}
		cols := renderedTPColumns(t, rec.Body.String())
		if cols["Количество"].ReadOnly != tc.locked {
			t.Fatalf("%s: Количество readOnly=%t, want %t", tc.status, cols["Количество"].ReadOnly, tc.locked)
		}
		if cols["Наименование"].ReadOnly {
			t.Fatalf("%s: column without condition is read-only", tc.status)
		}
		if cols["Количество"].Path == "" {
			t.Fatalf("%s: conditional column has no element path", tc.status)
		}
		qtyPath = cols["Количество"].Path
	}

	// Событие формы присылает состояние колонки по тому же пути: клиент
	// запирает/отпирает столбец сетки без перезагрузки.
	for _, tc := range []struct {
		status string
		locked bool
	}{{"Закрыт", true}, {"Открыт", false}} {
		body := url.Values{"_element": {"ПолеСтатус"}, "_event": {"ПриИзменении"}, "Статус": {tc.status}}
		req := httptest.NewRequest(http.MethodPost, "/ui/document/"+url.PathEscape("Заказ")+"/form-event", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("form-event %s: %d %s", tc.status, rec.Code, rec.Body.String())
		}
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		got, ok := resp.ElementStates.ReadOnly[qtyPath]
		if !ok || got != tc.locked {
			t.Fatalf("form-event %s: state[%s]=%v (present=%t), want %t; states=%+v", tc.status, qtyPath, got, ok, tc.locked, resp.ElementStates)
		}
	}
}
