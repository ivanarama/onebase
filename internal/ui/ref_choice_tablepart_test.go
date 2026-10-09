package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func addTPChoiceFixture(t *testing.T, f choiceHTTPFixture, grid bool) {
	t.Helper()
	f.owner.TableParts = []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
		{Name: "Направление", Type: "reference:Направление", RefEntity: f.direction.Name},
		{Name: "Глобальная", Type: "reference:Неисправность", RefEntity: f.target.Name},
		{Name: "Локальная", Type: "reference:Неисправность", RefEntity: f.target.Name},
	}}}
	table := &metadata.FormElement{ID: "rows", Name: "Строки", Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: !grid,
		Columns: []*metadata.FormElement{
			{ID: "direction", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Направление"},
			{ID: "global", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Глобальная", ChoiceFilter: []metadata.FormChoiceCondition{{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление"}}},
			{ID: "local", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Локальная", ChoiceFilter: []metadata.FormChoiceCondition{{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Строки.Направление"}}},
		}}
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements, table)
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.direction, f.target, f.owner}); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"Направление": f.rootA.String(), "Глобальная": f.pageTwo.String(), "Локальная": f.pageTwo.String()},
		{"Направление": f.rootB.String(), "Глобальная": f.pageTwo.String(), "Локальная": f.legacySelected.String()},
	}
	if err := f.server.store.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID, rows, f.owner.TableParts[0]); err != nil {
		t.Fatal(err)
	}
}

func tpChoiceQuery(f choiceHTTPFixture, column, path, value string) url.Values {
	sources, _ := json.Marshal(map[string]string{path: value})
	return url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"Строки." + column}, "row_id": {"0"}, "sources": {string(sources)}, "q": {"needle"}, "limit": {"100"}}
}

func TestTPChoicePublicHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceHTTPFixtureWithStore(t, db, false)
		addTPChoiceFixture(t, f, false)
		for _, column := range []string{"global", "local"} {
			path := "Объект.Направление"
			if column == "local" {
				path = "Строки.Направление"
			}
			q := tpChoiceQuery(f, column, path, f.rootA.String())
			q.Set("selected_id", f.pageTwo.String())
			r := f.serveRefOptions(t, f.target, q)
			if r.Code != 200 {
				t.Fatalf("%s %d %s", column, r.Code, r.Body.String())
			}
			got := decodeChoiceHTTP(t, r)
			if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
				t.Fatalf("%s: %#v", column, got)
			}
			for _, item := range got.Items {
				if strings.Contains(item["_label"].(string), "hidden") {
					t.Fatal("RLS leaked")
				}
			}
			q.Set("row_id", "1")
			q.Set("sources", `{"`+path+`":"`+f.rootB.String()+`"}`)
			got = decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
			if got.Total != 2 || got.SelectedAllowed == nil || *got.SelectedAllowed {
				t.Fatalf("other row: %#v", got)
			}
			q.Set("sources", `{"`+path+`":""}`)
			got = decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
			if got.Total != 0 || len(got.Items) != 0 {
				t.Fatalf("empty source: %#v", got)
			}
		}

		// Equality and hierarchy use the same public endpoint and List/CountList predicates.
		for _, column := range f.owner.Forms[0].Elements[len(f.owner.Forms[0].Elements)-1].Columns[1:] {
			column.ChoiceFilter[0].Op = metadata.FormChoiceOpEqual
		}
		for _, column := range []string{"global", "local"} {
			path := "Объект.Направление"
			if column == "local" {
				path = "Строки.Направление"
			}
			got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, tpChoiceQuery(f, column, path, f.rootA.String())))
			if got.Total != 1 || len(got.Items) != 1 {
				t.Fatalf("eq %s: %#v", column, got)
			}
		}
		for name, mutate := range map[string]func(url.Values){
			"unscoped":           func(q url.Values) { q.Set("element", "local") },
			"foreign column":     func(q url.Values) { q.Set("element", "Чужая.local") },
			"no row":             func(q url.Values) { q.Del("row_id") },
			"duplicate row":      func(q url.Values) { q.Add("row_id", "1") },
			"invalid row":        func(q url.Values) { q.Set("row_id", "../0") },
			"invalid UUID":       func(q url.Values) { q.Set("sources", `{"Строки.Направление":"O'Reilly"}`) },
			"foreign row source": func(q url.Values) { q.Set("sources", `{"Чужая.Направление":"`+f.rootA.String()+`"}`) },
			"extra source": func(q url.Values) {
				q.Set("sources", `{"Строки.Направление":"`+f.rootA.String()+`","sql":"true"}`)
			},
		} {
			t.Run(name, func(t *testing.T) {
				q := tpChoiceQuery(f, "local", "Строки.Направление", f.rootA.String())
				mutate(q)
				r := f.serveRefOptions(t, f.target, q)
				if r.Code != 400 {
					t.Fatalf("%d %s", r.Code, r.Body.String())
				}
			})
		}
		// A forged route cannot redirect a column's filter to another catalog.
		r := f.serveRefOptions(t, f.direction, tpChoiceQuery(f, "local", "Строки.Направление", f.rootA.String()))
		if r.Code != 400 {
			t.Fatalf("wrong target %d", r.Code)
		}
	})
}

func TestTPChoiceInitialPublicForm(t *testing.T) {
	for _, grid := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "grid"}[grid], func(t *testing.T) {
			f := newChoiceHTTPFixture(t)
			addTPChoiceFixture(t, f, grid)
			router := chi.NewRouter()
			f.server.Mount(router)
			req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
			req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
			r := httptest.NewRecorder()
			router.ServeHTTP(r, req)
			if r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			doc, err := html.Parse(strings.NewReader(r.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			if grid {
				if !strings.Contains(r.Body.String(), "data-sg-choice=") || !strings.Contains(r.Body.String(), "Строки.local") {
					t.Fatal("grid context missing")
				}
				return
			}
			for i := 0; i < 2; i++ {
				sel := findSelectByName(doc, []string{"tp.Строки.0.Локальная", "tp.Строки.1.Локальная"}[i])
				if sel == nil {
					t.Fatal("column select absent")
				}
				raw, _ := htmlAttribute(sel, "data-ref-choice-context")
				var ctx managedChoiceContext
				if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
					t.Fatal(err)
				}
				if ctx.Element != "Строки.local" || ctx.TablePart != "Строки" {
					t.Fatalf("%#v", ctx)
				}
				values := map[string]bool{}
				for o := sel.FirstChild; o != nil; o = o.NextSibling {
					v, _ := htmlAttribute(o, "value")
					values[v] = true
				}
				if i == 0 && (values[f.legacySelected.String()] || values[f.hidden.String()]) {
					t.Fatal("other row/RLS choice leaked")
				}
				if i == 1 && values[f.pageTwo.String()] {
					t.Fatal("first row choice leaked into second")
				}
			}
		})
	}
}
