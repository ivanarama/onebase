package ui

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

var listRefreshHrefRE = regexp.MustCompile(`<a class="btn btn-secondary btn-sm" data-ob-list-refresh href="([^"]+)"`)

func requireListRefreshURL(t *testing.T, page, want string) {
	t.Helper()
	match := listRefreshHrefRE.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("в командной панели нет кнопки обновления: %s", page)
	}
	if got := html.UnescapeString(match[1]); got != want {
		t.Fatalf("URL обновления = %q, ожидался текущий URL %q", got, want)
	}
}

func TestListRefreshKeepsCurrentRequestURI(t *testing.T) {
	t.Run("обычный список", func(t *testing.T) {
		entity := &metadata.Entity{
			Name: "Items",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Name", Type: metadata.FieldTypeString},
				{Name: "Price", Type: metadata.FieldTypeNumber},
			},
		}
		s, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
		target := "/ui/catalog/items?q=bolt&sort=Price&dir=desc&f.Price=10&page=3&subsystem=Sales"
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"entity": entity.Name})
		rec := httptest.NewRecorder()

		s.list(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
		}
		requireListRefreshURL(t, rec.Body.String(), target)
	})

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
		target := "/ui/inforeg/rates?Warehouse=Main&from=2026-08-01&to=2026-09-01"
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"name": infoReg.Name})
		rec := httptest.NewRecorder()

		s.infoRegList(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("infoRegList: status=%d body=%s", rec.Code, rec.Body.String())
		}
		requireListRefreshURL(t, rec.Body.String(), target)
	})

	t.Run("журнал", func(t *testing.T) {
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
			Filters: []metadata.JournalFilter{{Field: "Title", Type: "string"}},
		}
		s, _ := newSubmitTestServer(t, []*metadata.Entity{document})
		s.reg.LoadJournals([]*metadata.Journal{journal})
		target := "/ui/journal/orderjournal?f.Title=urgent&offset=50&subsystem=Sales"
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"name": journal.Name})
		rec := httptest.NewRecorder()

		s.journalList(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("journalList: status=%d body=%s", rec.Code, rec.Body.String())
		}
		requireListRefreshURL(t, rec.Body.String(), target)
	})
}
