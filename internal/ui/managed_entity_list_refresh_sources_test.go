package ui

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

func TestManagedEntityListSourceControlNames(t *testing.T) {
	for _, source := range []string{"объект.направление", "фОрМа.поиск"} {
		t.Run(source, func(t *testing.T) {
			f := entityListFixture(t)
			form := f.owner.Forms[0]
			controlName := "Направление"
			if source == "фОрМа.поиск" {
				controlName = "Поиск"
				form.Attributes = []*metadata.FormAttribute{{Name: controlName, TypeRef: "CatalogRef." + f.direction.Name}}
				form.Elements = append(form.Elements, &metadata.FormElement{Name: "Search", Kind: metadata.FormElementField, DataPath: "Форма." + controlName})
			}
			form.Elements[2].EntityList.Filter[0].Source = source
			if err := metadata.ValidateFormEntityLists(f.owner, form, f.server.reg.Entities()); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			f.server.Mount(router)
			req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
			req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			match := regexp.MustCompile(`data-ob-list-sources="([^"]+)"`).FindStringSubmatch(rec.Body.String())
			if rec.Code != 200 || len(match) != 2 {
				t.Fatalf("missing source binding: %d %s", rec.Code, rec.Body.String())
			}
			var controls map[string]string
			if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &controls); err != nil {
				t.Fatal(err)
			}
			if len(controls) != 1 || controls[source] != controlName {
				t.Fatalf("source does not bind HTML control %q: %v", controlName, controls)
			}
		})
	}
}

func TestManagedEntityListNumberSourceRefresh(t *testing.T) {
	for _, source := range []string{"Объект.Сумма", "Форма.Поиск"} {
		t.Run(source, func(t *testing.T) {
			f := entityListFixture(t)
			form := f.owner.Forms[0]
			f.owner.Fields = append(f.owner.Fields, metadata.Field{Name: "Сумма", Type: metadata.FieldTypeNumber})
			f.target.Fields = append(f.target.Fields, metadata.Field{Name: "Сумма", Type: metadata.FieldTypeNumber})
			form.Attributes = []*metadata.FormAttribute{{Name: "Поиск", TypeRef: "Number"}}
			form.Elements = append(form.Elements, &metadata.FormElement{Name: "NumberSource", Kind: metadata.FormElementField, DataPath: source})
			form.Elements[2].EntityList.Filter = []metadata.FormEntityListCondition{{Field: "Сумма", Op: "eq", Source: source}}
			ctx := context.Background()
			if err := f.server.store.Migrate(ctx, []*metadata.Entity{f.target, f.owner}); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{"12.5", "0", "-0.5"} {
				if err := f.server.store.Upsert(ctx, f.target.Name, uuid.New(), map[string]any{
					"Наименование": raw, "Аудитория": "anna", "Сумма": decimal.RequireFromString(raw),
				}, f.target); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct{ raw, status, name string }{
				{"12.5", "ready", "12.5"}, {"12,5", "ready", "12.5"},
				{" 12,50 ", "ready", "12.5"}, {"0", "ready", "0"},
				{"-,5", "ready", "-0.5"}, {"", "empty", ""},
				{"12,5.0", "error", ""}, {"oops", "error", ""},
			} {
				t.Run(tc.raw, func(t *testing.T) {
					sources, err := json.Marshal(map[string]string{source: tc.raw})
					if err != nil {
						t.Fatal(err)
					}
					list := decodeList(t, listHTTP(t, f, f.ownerID.String(), "", url.Values{"sources": {string(sources)}}))
					if list.Status != tc.status {
						t.Fatalf("source %q: %+v", tc.raw, list)
					}
					if tc.status == "ready" && (len(list.Rows) != 1 || len(list.Rows[0].Cells) != 1 || list.Rows[0].Cells[0] != tc.name) {
						t.Fatalf("wrong numeric filter %q: %+v", tc.raw, list)
					}
					if tc.status != "ready" && len(list.Rows) != 0 {
						t.Fatalf("invalid/empty source retained rows: %+v", list)
					}
				})
			}
		})
	}
}
