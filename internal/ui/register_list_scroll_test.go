package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Широкая таблица списка (много измерений и ресурсов, длинные значения)
// должна прокручиваться внутри белой карточки, а не вылезать за её край.
// У справочников и документов таблица давно обёрнута в overflow-x:auto;
// у регистров и журнала обёртки не было, и строки уходили за рамку формы.
const scrollTableOpen = "<div style=\"overflow-x:auto\">\n<table><thead><tr>"

func requireScrollableListTable(t *testing.T, router http.Handler, target string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", target, w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<table><thead><tr>") {
		t.Fatalf("GET %s: в ответе нет таблицы списка: %s", target, body)
	}
	if !strings.Contains(body, scrollTableOpen) {
		t.Fatalf("GET %s: таблица списка не обёрнута в прокручиваемый блок overflow-x:auto", target)
	}
}

func TestListTablesScrollInsideCard(t *testing.T) {
	t.Run("регистр сведений", func(t *testing.T) {
		infoReg := &metadata.InfoRegister{
			Name:       "Rates",
			Periodic:   true,
			Dimensions: []metadata.Field{{Name: "Warehouse", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Rate", Type: metadata.FieldTypeNumber}},
		}
		s, ctx := newSubmitTestServer(t, nil)
		if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{infoReg}); err != nil {
			t.Fatal(err)
		}
		s.reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{infoReg}})
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if err := s.store.InfoRegSet(ctx, infoReg,
			map[string]any{"Warehouse": strings.Repeat("Очень длинное название склада ", 20)},
			map[string]any{"Rate": 1.5}, &period); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		requireScrollableListTable(t, router, "/ui/inforeg/rates")
	})

	t.Run("регистр накопления", func(t *testing.T) {
		f := newRegisterMaskFixture(t)
		router := chi.NewRouter()
		f.s.Mount(router)
		requireScrollableListTable(t, router, "/ui/register/"+strings.ToLower(f.reg.Name))
		requireScrollableListTable(t, router, "/ui/register/"+strings.ToLower(f.reg.Name)+"/balances")
	})

	t.Run("журнал документов", func(t *testing.T) {
		document := &metadata.Entity{
			Name:   "Orders",
			Kind:   metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString}},
		}
		journal := &metadata.Journal{
			Name:      "OrderJournal",
			Documents: []string{document.Name},
			Columns: []metadata.JournalColumn{{
				Field: "Title",
				Label: "Title",
				Map:   map[string]string{document.Name: "Title"},
			}},
		}
		s, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
		s.reg.LoadJournals([]*metadata.Journal{journal})
		if err := s.store.Upsert(ctx, document.Name, uuid.New(),
			map[string]any{"Title": strings.Repeat("Длинный заголовок ", 30)}, document); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		requireScrollableListTable(t, router, "/ui/journal/orderjournal")
	})
}

// Exercise the mounted HTTP boundary on both databases after updating the base:
// the scroll wrapper must preserve populated/empty rows, filters and field access.
func TestListTablesScrollHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		document := &metadata.Entity{Name: "Orders", Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString}, {Name: "Secret", Type: metadata.FieldTypeString}}}
		info := &metadata.InfoRegister{Name: "Rates", Periodic: true,
			Dimensions: []metadata.Field{{Name: "Warehouse", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Rate", Type: metadata.FieldTypeNumber}, {Name: "Secret", Type: metadata.FieldTypeString}}}
		register := &metadata.Register{Name: "Stock",
			Dimensions: []metadata.Field{{Name: "Warehouse", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber}}}
		journal := &metadata.Journal{Name: "OrderJournal", Documents: []string{document.Name},
			Columns: []metadata.JournalColumn{
				{Field: "Title", Label: "Title", Map: map[string]string{document.Name: "Title"}},
				{Field: "Secret", Label: "Secret", Map: map[string]string{document.Name: "Secret"}},
			}, Filters: []metadata.JournalFilter{{Field: "Title", Type: "string"}}}
		if err := db.Migrate(ctx, []*metadata.Entity{document}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{register}); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{document}, InfoRegs: []*metadata.InfoRegister{info}, Registers: []*metadata.Register{register}})
		registry.LoadJournals([]*metadata.Journal{journal})
		s := New(registry, db, interpreter.New(), nil, Config{}, nil)
		t.Cleanup(s.Close)
		router := chi.NewRouter()
		s.Mount(router)
		request := func(target string, user *auth.User) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if user != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			return w
		}
		pages := []struct {
			name, path, filter, empty, secret string
			details                           bool
		}{
			{"inforeg", "/ui/inforeg/rates", "flt_Warehouse", "Записей нет", "INFO-SECRET-1922", true},
			{"movements", "/ui/register/stock", "flt_Warehouse", "Движений нет", "987654.25", false},
			{"balances", "/ui/register/stock/balances", "flt_Warehouse", "Остатков нет", "987654.25", false},
			{"journal", "/ui/journal/orderjournal", "f.Title", "Документов нет", "DOC-SECRET-1922", true},
		}
		for _, page := range pages {
			w := request(page.path, nil)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), page.empty) {
				t.Fatalf("empty GET %s: %d %s", page.path, w.Code, w.Body.String())
			}
		}
		longValue := strings.Repeat("ДлинноеНеразрывноеЗначениеСклада", 30)
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		id := uuid.New()
		if err := db.Upsert(ctx, document.Name, id, map[string]any{"Title": longValue, "Secret": "DOC-SECRET-1922"}, document); err != nil {
			t.Fatal(err)
		}
		if err := db.SetPosted(ctx, document.Name, id, true); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, info, map[string]any{"Warehouse": longValue}, map[string]any{"Rate": 1.5, "Secret": "INFO-SECRET-1922"}, &period); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteMovements(ctx, register.Name, document.Name, id, []map[string]any{{"Warehouse": longValue, "Amount": 987654.25, "ВидДвижения": "Приход"}}, register, &period); err != nil {
			t.Fatal(err)
		}
		masked := &auth.User{ID: "scroll-reader", Login: "scroll-reader", Roles: []*auth.Role{{Name: "reader", Permissions: auth.Permission{
			Documents: map[string][]string{document.Name: {"read"}},
			InfoRegs:  map[string][]string{info.Name: {"read"}},
			Registers: map[string][]string{register.Name: {"read"}},
			FieldAccess: auth.FieldAccess{
				Documents: map[string]auth.FieldPolicies{document.Name: {"Secret": {Read: "mask_all"}}},
				InfoRegs:  map[string]auth.FieldPolicies{info.Name: {"Secret": {Read: "mask_all"}}},
				Registers: map[string]auth.FieldPolicies{register.Name: {"Amount": {Read: "mask_all"}}},
			},
		}}}}
		denied := &auth.User{ID: "scroll-denied", Login: "scroll-denied", Roles: []*auth.Role{{Name: "denied"}}}
		for _, page := range pages {
			t.Run(page.name, func(t *testing.T) {
				requireScrollableListTable(t, router, page.path)
				for _, target := range []string{page.path, page.path + "?" + url.Values{page.filter: {longValue}}.Encode()} {
					w := request(target, nil)
					body := w.Body.String()
					if w.Code != http.StatusOK || !strings.Contains(body, longValue) || !strings.Contains(body, scrollTableOpen) {
						t.Fatalf("populated/filtered GET %s: %d; row or scroll wrapper missing", page.path, w.Code)
					}
					if page.details {
						panel := firstDetailPanelData(t, body)
						label := "Warehouse"
						if page.name == "journal" {
							label = "Title"
						}
						if got, ok := detailPanelValueByLabel(panel, label); !ok || got != longValue {
							t.Fatalf("detail %s: got %q, present %v", page.path, got, ok)
						}
					}
				}
				empty := request(page.path+"?"+url.Values{page.filter: {"no-match-1922"}}.Encode(), nil)
				if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), page.empty) || strings.Contains(empty.Body.String(), scrollTableOpen) {
					t.Fatalf("empty filter %s: %d; empty state missing or populated table remains", page.path, empty.Code)
				}
				w := request(page.path, masked)
				body := w.Body.String()
				if w.Code != http.StatusOK || !strings.Contains(body, "••••••") || strings.Contains(body, page.secret) || !strings.Contains(body, scrollTableOpen) {
					t.Fatalf("masked GET %s: %d; field mask or wrapper not preserved", page.path, w.Code)
				}
				w = request(page.path, denied)
				wantStatus := http.StatusForbidden
				if page.name == "journal" {
					wantStatus = http.StatusOK
				}
				if w.Code != wantStatus || strings.Contains(w.Body.String(), longValue) || strings.Contains(w.Body.String(), page.secret) {
					t.Fatalf("denied GET %s: %d; expected %d with no protected rows", page.path, w.Code, wantStatus)
				}
			})
		}
	})
}
