package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestRLSDiagnosticsShowsAutoFillAndReferencePolicy(t *testing.T) {
	owner := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Клиент", Type: metadata.FieldTypeString, RefEntity: owner.Name},
		},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{owner, order})
	role := &auth.Role{Name: "Менеджер", Permissions: auth.Permission{
		Catalogs:  map[string][]string{owner.Name: {"write"}},
		Documents: map[string][]string{order.Name: {"read"}},
		RowAccess: auth.RowAccess{
			Catalogs: map[string]auth.RowPolicies{
				owner.Name: {"write": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
			},
			Documents: map[string]auth.RowPolicies{
				order.Name: {"read": {Field: "Клиент.Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
			},
		},
	}}

	rows := s.buildRLSDiagnosticRows([]*auth.Role{role})
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want 2 diagnostics", rows)
	}
	var sawAutoFill, sawReference bool
	for _, row := range rows {
		if row.Object == owner.Name && row.Op == "write" {
			sawAutoFill = row.Status == "active" && row.AutoFill == "Owner"
		}
		if row.Object == order.Name && row.Field == "Клиент.Owner" {
			sawReference = row.Status == "active"
		}
	}
	if !sawAutoFill {
		t.Fatalf("diagnostics must report Owner auto-fill: %#v", rows)
	}
	if !sawReference {
		t.Fatalf("diagnostics must report active reference policy: %#v", rows)
	}
}

// Exercise the mounted page and persisted roles, not the row-building helper.
func TestRLSDiagnosticsUserCardAutoFillPublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newCardFixture(t, db)
		f.saveCard(t, "PRIVATE-BRANCH")
		f.orders.Fields = append(f.orders.Fields, metadata.Field{Name: "Карточка", Type: metadata.FieldType("reference:Сотрудник"), RefEntity: f.card.Name})
		if err := db.Migrate(f.ctx, []*metadata.Entity{f.card, f.orders}); err != nil {
			t.Fatal(err)
		}
		f.s.authRepo = auth.NewRepo(db)
		leaf := func() auth.RowPolicy {
			return auth.RowPolicy{Field: "филиал", Op: "eq", Value: auth.RowValue{UserCard: &auth.UserCardSource{
				Catalog: f.card.Name, AccountField: "Учётка", Field: "Филиал",
			}}}
		}
		tests := []struct {
			name         string
			edit         func(auth.RowPolicy) auth.RowPolicy
			sameAs, deny bool
			status, fill string
		}{
			{name: "direct", status: "active", fill: "Филиал"},
			{name: "same_as", sameAs: true, status: "active", fill: "Филиал"},
			{name: "implicit_eq", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Op = ""; return p }, status: "active", fill: "Филиал"},
			{name: "no_permission", deny: true, status: "ignored", fill: "нет"},
			{name: "invalid_source", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Value.UserCard.Field = "Missing"; return p }, status: "invalid", fill: "нет"},
			{name: "invalid_target", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Field = "Missing"; return p }, status: "invalid", fill: "нет"},
			{name: "invalid_account", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Value.UserCard.AccountField = "Филиал"; return p }, status: "invalid", fill: "нет"},
			{name: "broken_same_as", edit: func(p auth.RowPolicy) auth.RowPolicy { return auth.RowPolicy{SameAs: "missing"} }, status: "invalid", fill: "нет"},
			{name: "reference", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Field = "Карточка.Филиал"; return p }, status: "active", fill: "нет"},
			{name: "ne", edit: func(p auth.RowPolicy) auth.RowPolicy { p.Op = "ne"; return p }, status: "active", fill: "нет"},
			{name: "all", edit: func(p auth.RowPolicy) auth.RowPolicy { return auth.RowPolicy{All: []auth.RowPolicy{p}} }, status: "active", fill: "нет"},
			{name: "any", edit: func(p auth.RowPolicy) auth.RowPolicy { return auth.RowPolicy{Any: []auth.RowPolicy{p}} }, status: "active", fill: "нет"},
			{name: "not", edit: func(p auth.RowPolicy) auth.RowPolicy { return auth.RowPolicy{Not: &p} }, status: "active", fill: "нет"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				p := leaf()
				if tc.edit != nil {
					p = tc.edit(p)
				}
				policies := auth.RowPolicies{"write": p}
				if tc.sameAs {
					policies = auth.RowPolicies{"read": p, "write": {SameAs: "read"}}
				}
				ops := []string{"read", "write"}
				if tc.deny {
					ops = []string{"read"}
				}
				role := &auth.Role{Name: "DiagnosticRole", Permissions: auth.Permission{
					Documents: map[string][]string{f.orders.Name: ops},
					RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{f.orders.Name: policies}},
				}}
				if err := f.s.authRepo.SyncRoles(f.ctx, []*auth.Role{role}); err != nil {
					t.Fatal(err)
				}
				f.user.IsAdmin = true
				w := f.request("GET", "/ui/admin/rls", nil)
				if w.Code != 200 {
					t.Fatalf("diagnostics: %d %s", w.Code, w.Body.String())
				}
				body := w.Body.String()
				for _, private := range []string{"PRIVATE-CARD", "PRIVATE-BRANCH", f.cardID.String(), f.user.ID} {
					if strings.Contains(body, private) {
						t.Fatalf("diagnostics leaked %q", private)
					}
				}
				rows := rlsDiagnosticHTMLRows(t, body)
				found := false
				for _, row := range rows {
					if len(row) == 9 && row[0] == role.Name && row[3] == "write" {
						found = true
						if row[6] != tc.status || row[7] != tc.fill {
							t.Fatalf("status/auto-fill = %q/%q, want %q/%q", row[6], row[7], tc.status, tc.fill)
						}
					}
				}
				if !found {
					t.Fatalf("write diagnostic missing: %#v", rows)
				}
			})
		}
		// A missing card cannot hide the static indicator: the page must not resolve
		// card data or interpret availability as a change in policy metadata.
		if err := db.Delete(f.ctx, f.card.Name, f.cardID); err != nil {
			t.Fatal(err)
		}
		role := f.user.Roles[0]
		if err := f.s.authRepo.SyncRoles(f.ctx, []*auth.Role{role}); err != nil {
			t.Fatal(err)
		}
		w := f.request("GET", "/ui/admin/rls", nil)
		if w.Code != 200 {
			t.Fatalf("missing card diagnostics: %d", w.Code)
		}
		for _, row := range rlsDiagnosticHTMLRows(t, w.Body.String()) {
			if len(row) == 9 && row[0] == role.Name && row[3] == "write" {
				if row[7] != "Филиал" {
					t.Fatalf("missing card auto-fill = %q", row[7])
				}
				return
			}
		}
		t.Fatal("missing card write diagnostic absent")
	})
}

func rlsDiagnosticHTMLRows(t *testing.T, body string) [][]string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var out string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			out += text(c)
		}
		return out
	}
	var rows [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			var row []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "td" {
					row = append(row, strings.TrimSpace(text(c)))
				}
			}
			rows = append(rows, row)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return rows
}
