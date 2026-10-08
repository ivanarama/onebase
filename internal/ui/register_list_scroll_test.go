package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
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
