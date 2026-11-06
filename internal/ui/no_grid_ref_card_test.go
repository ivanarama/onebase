package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
	"golang.org/x/net/html"
)

func TestNoGridRefCardPolicyThroughHTTPAndJS(t *testing.T) {
	operator := &auth.User{Login: "operator", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{"Клиент": {"read"}, "Заказ": {"read", "write"}}}}}}
	admin := &auth.User{Login: "admin", IsAdmin: true}
	for _, tc := range []struct {
		name, keys     string
		user           *auth.User
		withAuth, want bool
	}{
		{"default", "", operator, true, true},
		{"false-operator", "  ref_card_button: false\n", operator, true, false},
		{"false-admin", "  ref_card_button: false\n", admin, true, false},
		{"admin-only-operator", "  ref_card_button_admin_only: true\n", operator, true, false},
		{"admin-only-admin", "  ref_card_button_admin_only: true\n", admin, true, true},
		{"both-admin", "  ref_card_button: false\n  ref_card_button_admin_only: true\n", admin, true, false},
		{"true-does-not-override-admin-only", "  ref_card_button: true\n  ref_card_button_admin_only: true\n", operator, true, false},
		{"no-auth", "  ref_card_button_admin_only: true\n", nil, false, true},
	} {
		for _, readonly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/readonly=%t", tc.name, readonly), func(t *testing.T) {
				dir := t.TempDir()
				write := func(name, value string) {
					t.Helper()
					path := filepath.Join(dir, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G703: fixed fixture name under t.TempDir, no request-supplied paths
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(value), 0o600); err != nil { //nolint:gosec // G703: fixed fixture name under t.TempDir, no request-supplied paths
						t.Fatal(err)
					}
				}
				write("catalogs/клиент.yaml", "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n")
				write("catalogs/заказ.yaml", "name: Заказ\nfields:\n  - {name: Наименование, type: string}\n  - {name: Клиент, type: 'reference:Клиент'}\ntableparts:\n  - name: Строки\n    fields:\n      - {name: Клиент, type: 'reference:Клиент'}\n")
				write("forms/заказ/объекта.form.yaml", "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заказ\n"+tc.keys+fmt.Sprintf(`elements:
  - {kind: ПолеВвода, name: ПолеКлиент, data_path: Объект.Клиент, ref_card_button: true}
  - {kind: ТабличнаяЧасть, name: Простая, data_path: Объект.Строки, no_grid: true, readonly: %t}
  - {kind: ТабличнаяЧасть, name: Грид, data_path: Объект.Строки}
`, readonly))
				proj, err := project.Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer proj.Close()
				s, ctx := newSubmitTestServer(t, proj.Entities)
				if tc.withAuth {
					s.authRepo = auth.NewRepo(s.store)
				}
				client, id := uuid.New(), uuid.New()
				ent := s.reg.GetEntity("Заказ")
				if err := s.store.Upsert(ctx, "Клиент", client, map[string]any{"Наименование": "Client"}, s.reg.GetEntity("Клиент")); err != nil {
					t.Fatal(err)
				}
				if err := s.store.Upsert(ctx, "Заказ", id, map[string]any{"Наименование": "Order", "Клиент": client}, ent); err != nil {
					t.Fatal(err)
				}
				if err := s.store.UpsertTablePartRows(ctx, ent.Name, "Строки", id, []map[string]any{{"Клиент": client}}, ent.TableParts[0]); err != nil {
					t.Fatal(err)
				}
				router := chi.NewRouter()
				s.Mount(router)
				req := httptest.NewRequest(http.MethodGet, "/ui/catalog/Заказ/"+id.String(), nil)
				if tc.user != nil {
					req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
				}
				root, err := html.Parse(strings.NewReader(rec.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				var body, grid *html.Node
				var visit func(*html.Node)
				visit = func(n *html.Node) {
					if value, _ := managedHTMLAttr(n, "id"); value == "tp-body-Строки" {
						body = n
					}
					if value, _ := managedHTMLAttr(n, "data-sg-tp"); value == "Строки" {
						grid = n
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						visit(c)
					}
				}
				visit(root)
				if body == nil || grid == nil {
					t.Fatal("missing no_grid or SlickGrid placement")
				}
				if got := refCardNode(body, "button", "data-ob-ref-current") != nil; got != tc.want {
					t.Fatalf("initial card=%t, want %t", got, tc.want)
				}
				if refCardNode(body, "button", "data-ob-ref-picker") == nil {
					t.Fatal("picker disappeared")
				}
				if refCardNode(refCardControl(t, rec.Body.String()), "button", "data-ob-ref-current") == nil {
					t.Fatal("explicit header override changed")
				}
				for _, a := range grid.Attr {
					if strings.Contains(a.Key, "hide-ref-card") {
						t.Fatal("no_grid policy leaked into SlickGrid")
					}
				}
				cols, _ := managedHTMLAttr(grid, "data-sg-cols")
				if strings.Contains(cols, "hideRefCard") || !strings.Contains(cols, "Клиент") {
					t.Fatal("SlickGrid contract changed")
				}
				attrs := map[string]string{}
				for _, a := range body.Attr {
					attrs[a.Key] = a.Val
				}
				payload, err := json.Marshal(map[string]any{"attrs": attrs, "client": client.String(), "readonly": readonly, "want": tc.want})
				if err != nil {
					t.Fatal(err)
				}
				node, err := exec.LookPath("node")
				if err != nil {
					t.Fatal("node required for no_grid public HTML round-trip")
				}
				cmd := exec.Command(node, "static/no_grid_ref_card_probe.js") //nolint:gosec // fixed test fixture and executable resolved from PATH
				cmd.Stdin = bytes.NewReader(payload)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("browser renderer: %v\n%s", err, output)
				}
			})
		}
	}
}
