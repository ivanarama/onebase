package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/project"
)

func TestReferencePageHTTP(t *testing.T) {
	for _, local := range []bool{false, true} {
		proj := refCardProject(t, "", "    reference_page: Переписка\n    readonly: true\n", local)
		s, ctx := newSubmitTestServer(t, proj.Entities)
		if err := s.store.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		if err := s.store.Upsert(ctx, "Клиент", uuid.New(), map[string]any{"Наименование": "Клиент"}, s.reg.GetEntity("Клиент")); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Заказ")+"/new", nil))
		if r.Code != http.StatusOK {
			t.Fatalf("form status: %d", r.Code)
		}
		if !strings.Contains(r.Body.String(), `data-ref-page="Переписка"`) {
			t.Fatalf("YAML reference_page lost in rendered control (local=%v)", local)
		}
	}
}

func TestReferencePageProcessorFormAttributeHTTP(t *testing.T) {
	for _, page := range []string{"", "Переписка"} {
		t.Run("page="+page, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"config/app.yaml":        "name: reference-page-processor\nversion: '1.0'\n",
				"catalogs/клиент.yaml":   "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n",
				"processors/подбор.yaml": "name: Подбор\n",
				"pages/переписка.yaml":   "name: Переписка\n",
				"src/переписка.page.os":  "Процедура ПриФормировании(Страница)\nКонецПроцедуры\n",
				"forms/подбор/переход.form.yaml": `schema: onebase.form/v1
form:
  name: Переход
  kind: object
  entity: Подбор
attributes:
  - {name: Выбор, type: CatalogRef.Клиент, save: false}
  - {name: Сосед, type: CatalogRef.Клиент, save: false}
elements:
  - kind: ГруппаФормы
    name: Навигация
    children:
      - kind: ПолеВвода
        name: ПолеКлиент
        data_path: Форма.Выбор
        reference_page: ` + page + `
      - kind: ПолеВвода
        name: ПолеСосед
        data_path: Форма.Сосед
`,
			}
			for name, body := range files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			proj, err := project.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { proj.Close() })
			s, ctx := newSubmitTestServer(t, proj.Entities)
			s.reg.LoadProcessors(proj.Processors)
			id := uuid.New()
			if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{"Наименование": "Выбранный клиент"}, s.reg.GetEntity("Клиент")); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			s.Mount(router)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/processor/"+url.PathEscape("Подбор"), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("processor form: HTTP %d", rec.Code)
			}
			anchor := managedAnchorNode(t, rec.Body.String(), "ПолеКлиент")
			neighbor := managedAnchorNode(t, rec.Body.String(), "ПолеСосед")
			if refCardNode(neighbor, "input", "name") == nil || refCardNode(neighbor, "select", "data-ref-entity") != nil {
				t.Fatal("neighbor attribute without reference_page lost its text fallback")
			}
			if page == "" {
				if refCardNode(anchor, "input", "name") == nil || refCardNode(anchor, "select", "data-ref-entity") != nil {
					t.Fatal("processor attribute without reference_page lost its text fallback")
				}
				return
			}
			selectNode := refCardNode(anchor, "select", "data-ref-page")
			if selectNode == nil {
				t.Fatal("processor form attribute has no reference_page control")
			}
			if got, _ := managedHTMLAttr(selectNode, "data-ref-page"); got != page {
				t.Fatalf("reference_page = %q, want %q", got, page)
			}
			if refCardNode(anchor, "button", "data-ob-ref-current") == nil || !strings.Contains(rec.Body.String(), id.String()) {
				t.Fatal("processor attribute lost its navigation button or reference option")
			}
		})
	}
}

func TestReferencePageBrowserNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	cmd := exec.Command(node, "--test", "static/reference_page_behavior_test.js") //nolint:gosec // test-only node executable
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("navigation: %v\n%s", err, out)
	}
}
