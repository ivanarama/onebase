package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

type infoRegPageForm struct {
	action string
	values url.Values
}

type infoRegHTMLPage struct {
	rows   []string
	forms  []infoRegPageForm
	links  map[string]string
	filter url.Values
	status string
}

func infoRegNodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(infoRegNodeText(child))
	}
	return b.String()
}

func readInfoRegHTMLPage(t *testing.T, body string) infoRegHTMLPage {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	p := infoRegHTMLPage{links: map[string]string{}, filter: url.Values{}}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if _, ok := deleteMatrixHTMLAttr(n, "data-ob-list-row"); ok {
				p.rows = append(p.rows, infoRegNodeText(n))
			}
			if _, ok := deleteMatrixHTMLAttr(n, "data-ob-inforeg-pagination"); ok {
				p.status = strings.Join(strings.Fields(infoRegNodeText(n)), " ")
			}
			if n.Data == "a" {
				if rel, ok := deleteMatrixHTMLAttr(n, "rel"); ok {
					p.links[rel], _ = deleteMatrixHTMLAttr(n, "href")
				}
			}
			if n.Data == "form" {
				values := url.Values{}
				var inputs func(*html.Node)
				inputs = func(node *html.Node) {
					if node.Type == html.ElementNode && node.Data == "input" {
						name, _ := deleteMatrixHTMLAttr(node, "name")
						value, _ := deleteMatrixHTMLAttr(node, "value")
						if name != "" {
							values.Add(name, value)
						}
					}
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						inputs(child)
					}
				}
				inputs(n)
				method, _ := deleteMatrixHTMLAttr(n, "method")
				action, _ := deleteMatrixHTMLAttr(n, "action")
				if strings.EqualFold(method, "POST") && strings.Contains(action, "/delete") {
					p.forms = append(p.forms, infoRegPageForm{action, values})
				} else if strings.EqualFold(method, "get") && values.Has("flt_Owner") {
					p.filter = values
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return p
}

// The router is mounted exactly as in production: no direct calls to the list,
// delete, scanner or pagination implementation are used by the regression.
func infoRegPageRequest(router http.Handler, user *auth.User, method, target string, form url.Values) *httptest.ResponseRecorder {
	var body strings.Reader
	if form != nil {
		body = *strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, &body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != nil {
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestUIInfoRegPaginationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, periodic := range []bool{false, true} {
			t.Run(fmt.Sprintf("periodic=%t", periodic), func(t *testing.T) {
				ctx := context.Background()
				ir := &metadata.InfoRegister{
					Name: fmt.Sprintf("Paged%d", map[bool]int{false: 0, true: 1}[periodic]), Periodic: periodic,
					Dimensions: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Seq", Type: metadata.FieldTypeNumber, Length: 20, Scale: 2}},
					Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}, {Name: "Secret", Type: metadata.FieldTypeString}},
				}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				period := time.Date(2026, 10, 1, 11, 22, 33, 0, time.UTC)
				for _, owner := range []string{"mine", "other"} {
					for i := 1; i <= 7; i++ {
						var periodPtr *time.Time
						if periodic {
							periodPtr = &period
						}
						if err := db.InfoRegSet(ctx, ir,
							map[string]any{"Owner": owner, "Seq": fmt.Sprintf("900719925474099%d.01", i)},
							map[string]any{"Value": fmt.Sprintf("%s-row-%d", owner, i), "Secret": "private-resource"}, periodPtr); err != nil {
							t.Fatal(err)
						}
					}
				}
				s := deleteMatrixServer(db, nil, []*metadata.InfoRegister{ir}, nil)
				router := chi.NewRouter()
				s.Mount(router)
				policy := auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "mine"}}
				role := &auth.Role{Permissions: auth.Permission{
					InfoRegs:    map[string][]string{ir.Name: {"read", "delete"}},
					RowAccess:   auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{ir.Name: {"read": policy, "delete": policy}}},
					FieldAccess: auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{ir.Name: {"Secret": {Read: "mask_all"}}}},
				}}
				user := &auth.User{ID: "paged", Login: "paged", Roles: []*auth.Role{role}}
				base := "/ui/inforeg/" + strings.ToLower(ir.Name)
				query := url.Values{"limit": {"2"}, "subsystem": {"Sales"}, "submit": {"hostile-control"}}
				if periodic {
					query.Set("from", "2026-10-01")
					query.Set("to", "2026-10-02")
				}
				get := func(q url.Values) (infoRegHTMLPage, string) {
					t.Helper()
					w := infoRegPageRequest(router, user, http.MethodGet, base+"?"+q.Encode(), nil)
					if w.Code != http.StatusOK {
						t.Fatalf("GET status=%d: %s", w.Code, w.Body.String())
					}
					return readInfoRegHTMLPage(t, w.Body.String()), w.Body.String()
				}
				var second infoRegHTMLPage
				seen := map[string]bool{}
				for page := 1; page <= 4; page++ {
					query.Set("page", strconv.Itoa(page))
					p, body := get(query)
					wantRows := 2
					if page == 4 {
						wantRows = 1
					}
					if len(p.rows) != wantRows || !strings.Contains(p.status, "Всего: 7") || !strings.Contains(p.status, fmt.Sprintf("Стр. %d из 4", page)) {
						t.Fatalf("page %d: rows=%d status=%q", page, len(p.rows), p.status)
					}
					if strings.Contains(body, "other-row-") || strings.Contains(body, "private-resource") {
						t.Fatal("page leaked a row excluded by RLS or a masked resource")
					}
					for _, form := range p.forms {
						key := form.values.Get("Seq")
						if seen[key] {
							t.Fatalf("duplicate key on pages: %s", key)
						}
						seen[key] = true
						if periodic && form.values.Get("period") != period.Format(time.RFC3339Nano) {
							t.Fatalf("lost exact period: %v", form.values)
						}
					}
					if page == 2 {
						second = p
					}
					if (p.links["prev"] != "") != (page > 1) || (p.links["next"] != "") != (page < 4) {
						t.Fatalf("incorrect pagination links: %v", p.links)
					}
				}
				if len(seen) != 7 {
					t.Fatalf("missing or repeated keys: %v", seen)
				}
				query.Set("flt_Owner", "mine")
				query.Set("page", "2")
				filtered, body := get(query)
				for _, link := range filtered.links {
					u, err := url.Parse(link)
					if err != nil || u.Query().Get("flt_Owner") != "mine" || u.Query().Get("limit") != "2" || u.Query().Get("subsystem") != "Sales" || u.Query().Has("submit") {
						t.Fatalf("lost filter/context or copied unsafe control: %s", link)
					}
					if periodic && (u.Query().Get("from") != "2026-10-01" || u.Query().Get("to") != "2026-10-02") {
						t.Fatalf("lost period filter: %s", link)
					}
				}
				if filtered.filter.Get("limit") != "2" || filtered.filter.Get("subsystem") != "Sales" || filtered.filter.Has("page") || filtered.filter.Has("submit") || len(filtered.filter["flt_Owner"]) != 1 {
					t.Fatalf("filter submit does not reset page/preserve context: %v", filtered.filter)
				}
				requireListRefreshURL(t, body, base+"?"+query.Encode())
				// Delete the lossless composite key taken from the second page.
				form := second.forms[0]
				if form.values.Get("Seq") != "9007199254740993.01" {
					t.Fatalf("numeric machine key rounded: %v", form.values)
				}
				w := infoRegPageRequest(router, user, http.MethodPost, form.action, form.values)
				if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "page=2") {
					t.Fatalf("delete status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
				}
				rows, err := db.InfoRegList(ctx, ir, storage.RegFilter{})
				if err != nil || len(rows) != 13 {
					t.Fatalf("delete did not affect exactly one row: rows=%d err=%v", len(rows), err)
				}
				for _, row := range rows {
					if row["Value"] == "mine-row-3" {
						t.Fatal("second-page delete missed exact key")
					}
				}
				query.Set("page", strconv.Itoa(int(^uint(0)>>1)))
				last, _ := get(query)
				if len(last.rows) != 2 || !strings.Contains(last.status, "Всего: 6") || !strings.Contains(last.status, "Стр. 3 из 3") {
					t.Fatalf("large/stale page not clamped: %#v", last)
				}
				// Masked primary-key fields must not leak their exact hidden key.
				role.Permissions.FieldAccess.InfoRegs[ir.Name]["Seq"] = auth.FieldPolicy{Read: "mask_all"}
				query.Set("page", "2")
				masked, body := get(query)
				if len(masked.rows) != 2 || strings.Contains(body, "900719925474099") || strings.Contains(body, "period_key") {
					t.Fatal("masked second page leaked a delete key")
				}
				if len(masked.forms) > 0 {
					form := masked.forms[0]
					w := infoRegPageRequest(router, user, http.MethodPost, form.action, form.values)
					if w.Code != http.StatusForbidden {
						t.Fatalf("masked-key delete: status=%d", w.Code)
					}
				}
				query.Set("flt_Owner", "missing")
				empty, _ := get(query)
				if len(empty.rows) != 0 || !strings.Contains(empty.status, "Всего: 0") || !strings.Contains(empty.status, "Стр. 1 из 1") || len(empty.links) != 0 {
					t.Fatalf("empty result pagination: %#v", empty)
				}
			})
		}
	})
}

func TestUIInfoRegPaginationUsesDatabasePageSize(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{Name: "PagedDefault", Dimensions: []metadata.Field{{Name: "Owner", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveListPageSize(ctx, 2); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			if err := db.InfoRegSet(ctx, ir, map[string]any{"Owner": strconv.Itoa(i)}, map[string]any{"Value": "value"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
		s := &Server{store: db, reg: registry}
		router := chi.NewRouter()
		s.Mount(router)
		for _, query := range []string{"", "?page=-1&limit=0", "?page=overflow&limit=1001", "?page=9223372036854775808&limit=-1"} {
			w := infoRegPageRequest(router, nil, http.MethodGet, "/ui/inforeg/pageddefault"+query, nil)
			p := readInfoRegHTMLPage(t, w.Body.String())
			if w.Code != http.StatusOK || len(p.rows) != 2 || !strings.Contains(p.status, "Всего: 5") {
				t.Fatalf("default/invalid pagination %q: status=%d rows=%d footer=%q", query, w.Code, len(p.rows), p.status)
			}
		}
	})
}
