package ui

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/incident"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the mounted routes, including Save, its database classification and
// the real error form. A duplicate must remain a retryable form submission.
func TestStandardFormUniqueConflictHTTP(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprintf("edit=%t", edit), func(t *testing.T) {
			entity := &metadata.Entity{
				Name: "Контрагенты", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{
					{Name: "ИНН", Type: metadata.FieldTypeString},
					{Name: "Наименование", Type: metadata.FieldTypeString},
				},
				Indexes: []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}},
				TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
					{Name: "Описание", Type: metadata.FieldTypeString},
				}}},
			}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
			s.incidents = incident.NewStore(10)
			occupiedID := uuid.New()
			if err := s.store.Upsert(ctx, entity.Name, occupiedID, map[string]any{
				"ИНН": "77", "Наименование": "Занято",
			}, entity); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			endpoint := "/ui/catalog/" + entity.Name + "/new?lang=ru"
			if edit {
				if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{
					"ИНН": "88", "Наименование": "Исходное",
				}, entity); err != nil {
					t.Fatal(err)
				}
				if err := s.store.UpsertTablePartRows(ctx, entity.Name, "Строки", id,
					[]map[string]any{{"Описание": "Исходная строка"}}, entity.TableParts[0]); err != nil {
					t.Fatal(err)
				}
				endpoint = "/ui/catalog/" + entity.Name + "/" + id.String() + "?lang=ru"
			}
			body := url.Values{
				"ИНН": {"77"}, "Наименование": {`Введено "<значение>"`},
				"tp.Строки.0.Описание": {`Строка "<один>"`},
				"tp.Строки.1.Описание": {"Строка два"},
			}
			if edit {
				body.Set("_version", "1")
			}
			router := chi.NewRouter()
			s.Mount(router)
			post := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				return w
			}
			w := post()
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("not an HTML form: %v", w.Header())
			}
			response := w.Body.String()
			for _, want := range []string{"ИНН", "77", "уже занято другой записью"} {
				if !strings.Contains(response, want) {
					t.Errorf("form lacks duplicate explanation %q", want)
				}
			}
			for key, values := range body {
				want := `name="` + key + `" value="` + html.EscapeString(values[0]) + `"`
				if !strings.Contains(response, want) {
					t.Errorf("form lost input %s=%q", key, values[0])
				}
			}
			if edit && !strings.Contains(response, id.String()) {
				t.Error("form lost edit identity")
			}
			if incidents := s.Incidents().Recent("", 10); len(incidents) != 0 {
				t.Fatalf("expected conflict registered incidents: %+v", incidents)
			}
			rows, err := s.store.List(ctx, entity.Name, entity, storage.ListParams{})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if edit {
				wantCount = 2
				row, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err != nil || row["ИНН"] != "88" || row["Наименование"] != "Исходное" || row["_version"] != int64(1) {
					t.Fatalf("rejected edit changed header/version: row=%v err=%v", row, err)
				}
				tpRows, err := s.store.GetTablePartRows(ctx, entity.Name, "Строки", id, entity.TableParts[0])
				if err != nil || len(tpRows) != 1 || tpRows[0]["Описание"] != "Исходная строка" {
					t.Fatalf("rejected edit changed table part: rows=%v err=%v", tpRows, err)
				}
			}
			if len(rows) != wantCount {
				t.Fatalf("duplicate persisted: rows=%v", rows)
			}
			// Retry the same input with an available key and the preserved token.
			body.Set("ИНН", "99")
			if retry := post(); retry.Code != http.StatusSeeOther {
				t.Fatalf("retry status=%d: %s", retry.Code, retry.Body.String())
			}
		})
	}
}

func TestStandardFormTechnicalSaveFailureHTTP(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprintf("edit=%t", edit), func(t *testing.T) {
			entity := &metadata.Entity{Name: "СбойЗаписи", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
			s.incidents = incident.NewStore(10)
			endpoint := "/ui/catalog/" + entity.Name + "/new"
			if edit {
				id := uuid.New()
				if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "До"}, entity); err != nil {
					t.Fatal(err)
				}
				endpoint = "/ui/catalog/" + entity.Name + "/" + id.String()
			}
			s.store.Close()
			router := chi.NewRouter()
			s.Mount(router)
			r := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(url.Values{"Наименование": {"После"}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusInternalServerError || len(s.Incidents().Recent("", 10)) != 1 {
				t.Fatalf("technical error lost 500/incident: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
