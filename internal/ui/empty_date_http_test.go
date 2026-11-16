package ui

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// Exercise the mounted HTTP routes: testing only fmtDateValue would miss a
// template that sends a missing date through a different formatter.
func TestEmptyDate_HTTPListsAndPicker(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("MSK", 3*60*60)
	t.Cleanup(func() { time.Local = saved })

	var entities []*metadata.Entity
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		entities = append(entities, &metadata.Entity{
			Name: "Dates" + string(kind), Kind: kind,
			Fields: []metadata.Field{
				{Name: "Name", Type: metadata.FieldTypeString},
				{Name: "OptionalDate", Type: metadata.FieldTypeDate},
			},
			ListForm: []string{"Name", "OptionalDate"},
		})
	}
	s, ctx := newSubmitTestServer(t, entities)
	rows := []struct {
		id   uuid.UUID
		date any
		want string
	}{
		{uuid.New(), nil, ""},
		{uuid.New(), time.Date(2026, 10, 17, 13, 35, 30, 0, time.UTC), "17.10.2026 16:35:30"},
		{uuid.New(), time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local), "17.10.2026"},
	}
	for _, entity := range entities {
		for _, row := range rows {
			if err := s.store.Upsert(ctx, entity.Name, row.id, map[string]any{
				"Name": row.id.String(), "OptionalDate": row.date,
			}, entity); err != nil {
				t.Fatal(err)
			}
		}
	}

	var journals []*metadata.Journal
	for _, format := range []string{"", "date"} {
		journals = append(journals, &metadata.Journal{
			Name: "DatesJournal" + format, Documents: []string{entities[1].Name},
			Columns: []metadata.JournalColumn{{Field: "OptionalDate", Format: format}},
		})
	}
	s.reg.LoadJournals(journals)
	router := chi.NewRouter()
	s.Mount(router)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	for _, route := range []struct {
		path string
		cell int
	}{
		{"/ui/catalog/" + entities[0].Name, 1},
		{"/ui/document/" + entities[1].Name, 2},
		{"/ui/journal/" + journals[0].Name, 1},
		{"/ui/journal/" + journals[1].Name, 1},
	} {
		t.Run(route.path, func(t *testing.T) {
			page := getBody(t, ts.URL+route.path)
			for _, row := range rows {
				cells := emptyDateRowCells(t, page, row.id.String())
				if len(cells) <= route.cell {
					t.Fatalf("date cell missing for %s: %q", row.id, cells)
				}
				if got := cells[route.cell]; got != row.want {
					t.Errorf("date for %s = %q, want %q", row.id, got, row.want)
				}
			}
		})
	}

	// The selection form receives raw dates as JSON, not formatted HTML. Keep
	// NULL as null and preserve the instant of both populated dates.
	for _, entity := range entities {
		t.Run("picker/"+entity.Name, func(t *testing.T) {
			var response struct {
				Items []struct {
					ID   string  `json:"id"`
					Date *string `json:"OptionalDate"`
				} `json:"items"`
			}
			if err := json.Unmarshal([]byte(getBody(t, ts.URL+"/ui/_ref-options/"+entity.Name)), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Items) != len(rows) {
				t.Fatalf("picker returned %d rows, want %d", len(response.Items), len(rows))
			}
			for _, row := range rows {
				found := false
				for _, item := range response.Items {
					if item.ID != row.id.String() {
						continue
					}
					found = true
					if row.date == nil {
						if item.Date != nil {
							t.Errorf("empty picker date = %q, want null", *item.Date)
						}
					} else if item.Date == nil {
						t.Error("populated picker date is null")
					} else if got, err := time.Parse(time.RFC3339, *item.Date); err != nil || !got.Equal(row.date.(time.Time)) {
						t.Errorf("picker date = %q, want instant %v (parse: %v)", *item.Date, row.date, err)
					}
				}
				if !found {
					t.Errorf("picker omitted %s", row.id)
				}
			}
		})
	}
}

func emptyDateRowCells(t *testing.T, page, id string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var out strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out.WriteString(text(c))
		}
		return out.String()
	}
	var cells []string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			for _, attr := range n.Attr {
				if (attr.Key == "data-ob-entity-id" && attr.Val == id) ||
					(attr.Key == "data-open-url" && strings.HasSuffix(attr.Val, "/"+id)) {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && c.Data == "td" {
							cells = append(cells, strings.TrimSpace(text(c)))
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if cells == nil {
		t.Fatalf("row %s missing from HTTP table", id)
	}
	return cells
}
