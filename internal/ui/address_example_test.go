package ui

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// The shipped example runs through HTTP save/print and the public processor
// runner used by procrun. Calling the formatter alone would miss a dead hook.
func TestAddressExample_SavePrintAndExchange(t *testing.T) {
	ctx := t.Context()
	proj, err := project.Load("../../examples/address")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "address.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatal(err)
	}
	reg.LoadLayoutForms(proj.LayoutForms)
	router := chi.NewRouter()
	s.Mount(router)

	cases := []struct {
		name   string
		fields url.Values
		want   string
	}{
		{"full", url.Values{
			"Город": {"Белград"}, "Улица": {"Примерная"}, "Дом": {"12А"},
			"Корпус": {"2"}, "Квартира": {"07"}, "Подъезд": {"3"},
			"Этаж": {"0"}, "Домофон": {"07#"},
			"ИсходныйАдрес":      {"старое написание"},
			"АдресПредставление": {"подставленная клиентом строка"},
		}, "г. Белград, ул. Примерная, д. 12А, корп. 2, кв. 07, подъезд 3, этаж 0, домофон 07#"},
		{"partial", url.Values{"Улица": {"  Тихая  "}, "Дом": {"  "}, "Этаж": {"0"}}, "ул. Тихая, этаж 0"},
		{"legacy", url.Values{"ИсходныйАдрес": {"  старый адрес; дом неизвестен  "}}, "  старый адрес; дом неизвестен  "},
		{"empty", url.Values{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fields.Set("Номер", tc.name)
			save := func(target string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(tc.fields.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusSeeOther {
					t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
				}
				return rec
			}
			save("/ui/document/Наряд/new")
			rows, err := db.List(ctx, "Наряд", proj.Entities[0], storage.ListParams{})
			if err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			for _, candidate := range rows {
				if candidate["Номер"] == tc.name {
					row = candidate
					break
				}
			}
			if row == nil {
				t.Fatal("saved work order not found")
			}
			if got := row["АдресПредставление"]; got != tc.want {
				t.Fatalf("stored address = %q, want %q", got, tc.want)
			}
			if got := row["ИсходныйАдрес"]; tc.fields.Get("ИсходныйАдрес") != "" && got != tc.fields.Get("ИсходныйАдрес") {
				t.Fatalf("original changed: %q", got)
			}

			messages, runErr, err := RunProcessorOffline(ctx, proj, db, "ЭкспортНаряда", map[string]string{"Номер": tc.name}, nil)
			if err != nil || runErr != nil {
				t.Fatalf("export: setup=%v run=%v", err, runErr)
			}
			if len(messages) != 1 {
				t.Fatalf("export messages = %v", messages)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(messages[0]), &payload); err != nil {
				t.Fatal(err)
			}
			if got := payload["АдресПредставление"]; got != tc.want {
				t.Fatalf("export address = %q, want %q; JSON: %s", got, tc.want, messages[0])
			}
			for _, field := range []string{"Город", "Улица", "Дом", "Корпус", "Квартира", "Подъезд", "Этаж", "Домофон", "ИсходныйАдрес"} {
				if got := payload[field]; got != tc.fields.Get(field) {
					t.Errorf("export %s = %q, want %q", field, got, tc.fields.Get(field))
				}
			}
			id := row["id"].(string)
			target := "/ui/document/Наряд/" + id + "/print/НарядНаВыезд"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("print: %d %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(html.UnescapeString(rec.Body.String()), "Адрес: "+tc.want) {
				t.Fatalf("print lacks address: %s", rec.Body.String())
			}
			if tc.name == "full" {
				tc.fields.Set("Дом", "14Б")
				save("/ui/document/Наряд/" + id)
				updated, err := db.GetByID(ctx, "Наряд", uuid.MustParse(id), proj.Entities[0])
				if err != nil {
					t.Fatal(err)
				}
				want := strings.Replace(tc.want, "12А", "14Б", 1)
				if got := updated["АдресПредставление"]; got != want {
					t.Fatalf("edit kept stale address: %q", got)
				}
				if got := updated["ИсходныйАдрес"]; got != "старое написание" {
					t.Fatalf("edit changed original: %q", got)
				}
				messages, runErr, err := RunProcessorOffline(ctx, proj, db, "ЭкспортНаряда", map[string]string{"Номер": tc.name}, nil)
				if err != nil || runErr != nil {
					t.Fatalf("export after edit: %v %v", err, runErr)
				}
				if len(messages) != 1 {
					t.Fatalf("export messages after edit: %v", messages)
				}
				if err := json.Unmarshal([]byte(messages[0]), &payload); err != nil {
					t.Fatal(err)
				}
				if payload["АдресПредставление"] != want || payload["Дом"] != "14Б" {
					t.Fatalf("stale export after edit: %v", payload)
				}
				rec = httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
				if rec.Code != http.StatusOK || !strings.Contains(html.UnescapeString(rec.Body.String()), "Адрес: "+want) {
					t.Fatalf("stale print after edit: %d %s", rec.Code, rec.Body.String())
				}
			}
		})
	}
}
