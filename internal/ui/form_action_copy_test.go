package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
)

// #2241: actions.copy.visible:false в YAML управляемой формы убирает
// «Скопировать» с карточки записанного объекта. Право создания не меняется:
// форма создания открывается, прямой адрес ?copy= по-прежнему копирует.
func TestManagedFormActionCopyVisible_HTTP(t *testing.T) {
	const copyLink = `/new?copy=`
	for _, tc := range []struct {
		name     string
		actions  string
		wantCopy bool
	}{
		{"без ключа", "", true},
		{"visible true", "actions:\n  copy:\n    visible: true\n", true},
		{"visible false", "actions:\n  copy:\n    visible: false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\n" + tc.actions +
				"elements:\n  - kind: ПолеВвода\n    name: ПолеТема\n    data_path: Объект.Тема\n"
			path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
			if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Звонок")
			if err != nil {
				t.Fatal(err)
			}
			ent := &metadata.Entity{Name: "Звонок", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Тема", Type: metadata.FieldTypeString}},
				Forms:  []*metadata.FormModule{fm}}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			id := uuid.New()
			if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{"Тема": "Течёт кран"}, ent); err != nil {
				t.Fatal(err)
			}
			get := func(handler func(http.ResponseWriter, *http.Request), target string, params map[string]string) string {
				t.Helper()
				req := httptest.NewRequest("GET", target, nil)
				route := chi.NewRouteContext()
				for k, v := range params {
					route.URLParams.Add(k, v)
				}
				req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
				rec := httptest.NewRecorder()
				handler(rec, req)
				if rec.Code != 200 {
					t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body.String())
				}
				return rec.Body.String()
			}

			card := get(s.formEdit, "/ui/catalog/звонок/"+id.String(), map[string]string{"kind": "catalog", "entity": "звонок", "id": id.String()})
			if !strings.Contains(card, "ПолеТема") {
				t.Fatal("карточка не отрисовала форму из YAML")
			}
			if got := strings.Contains(card, copyLink+id.String()); got != tc.wantCopy {
				t.Fatalf("кнопка «Скопировать» на карточке = %v, ожидалось %v", got, tc.wantCopy)
			}

			// Право создания и прямое копирование ключ не трогает.
			fresh := get(s.form, "/ui/catalog/звонок/new?copy="+id.String(), map[string]string{"entity": "звонок"})
			if !strings.Contains(fresh, "Течёт кран") {
				t.Fatal("прямой адрес ?copy= перестал копировать значения")
			}
		})
	}
}
