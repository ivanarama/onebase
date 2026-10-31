package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Follow the actual mounted list and its removable-filter anchor on both
// databases. Captured ordering and the two independent filters must coexist.
func TestListSortFormFilterIntegration(t *testing.T) {
	for _, opening := range []string{"default", "restored"} {
		for _, mode := range []string{"feed", "pages"} {
			t.Run(opening+"/"+mode, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					entity := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
						Fields: []metadata.Field{
							{Name: "Наименование", Type: metadata.FieldTypeString},
							{Name: "Автор", Type: metadata.FieldTypeString},
						},
					}
					entity.Forms = []*metadata.FormModule{{
						Name: "ФормаСписка", Kind: "list", EntityName: entity.Name,
						LayoutKind: metadata.FormLayoutManaged,
						Elements:   []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Имя", DataPath: "Наименование"}},
						ListFilter: []metadata.FormListCondition{{Query: listQueryMine, Removable: true, Label: map[string]string{"ru": "Только мои"}}},
					}}
					if err := db.Migrate(t.Context(), []*metadata.Entity{entity}); err != nil {
						t.Fatal(err)
					}
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
					s := &Server{store: db, reg: reg, messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
					router := chi.NewRouter()
					s.Mount(router)
					user := listQueryUser("u-op")
					names := []string{"Моя Alpha", "Моя Omega", "Чужая Alpha", "Чужая Omega"}
					for i, name := range names {
						author := "u-op"
						if i >= 2 {
							author = "u-other"
						}
						if err := db.Upsert(t.Context(), entity.Name, uuid.New(), map[string]any{"Наименование": name, "Автор": author}, entity); err != nil {
							t.Fatal(err)
						}
					}
					get := func(q url.Values) string {
						t.Helper()
						req := httptest.NewRequest(http.MethodGet, "/ui/catalog/заявка?"+q.Encode(), nil)
						req = req.WithContext(auth.ContextWithUser(req.Context(), user))
						w := httptest.NewRecorder()
						router.ServeHTTP(w, req)
						if w.Code != http.StatusOK {
							t.Fatalf("mounted list: HTTP %d: %s", w.Code, w.Body.String())
						}
						return w.Body.String()
					}
					assertRows := func(body string, want ...string) {
						t.Helper()
						doc, err := html.Parse(strings.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						var got []string
						var walk func(*html.Node)
						walk = func(n *html.Node) {
							if n.Type == html.TextNode {
								for _, name := range names {
									if strings.TrimSpace(n.Data) == name {
										got = append(got, name)
									}
								}
							}
							for c := n.FirstChild; c != nil; c = c.NextSibling {
								walk(c)
							}
						}
						walk(doc)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("list rows %v, want %v", got, want)
						}
					}
					preference := func(wantField, wantDir string) {
						t.Helper()
						field, dir, err := db.GetListSortUserSettings(t.Context(), entity.Name, user.Login)
						if err != nil || field != wantField || dir != wantDir {
							t.Fatalf("preference %q/%q, want %q/%q: %v", field, dir, wantField, wantDir, err)
						}
						var count int
						if err := db.QueryRow(t.Context(), `SELECT COUNT(*) FROM _settings WHERE key LIKE 'listsort.%'`).Scan(&count); err != nil {
							t.Fatal(err)
						}
						wantCount := 0
						if wantField != "" {
							wantCount = 1
						}
						if count != wantCount {
							t.Fatalf("unexpected preference write: %d rows, want %d", count, wantCount)
						}
					}
					choose := func(dir string) { t.Helper(); get(url.Values{"sort": {"Наименование"}, "dir": {dir}}) }
					preference("", "")
					mine, others := names[:2], names[2:]
					wantField, wantDir := "", ""
					if opening == "restored" {
						choose("desc")
						wantField, wantDir = "Наименование", "desc"
						mine = []string{names[1], names[0]}
						others = []string{names[3], names[2]}
					}
					initial := get(url.Values{"lm": {mode}, "limit": {"10"}, "q": {"a"}, "subsystem": {"sales"}, "view": {"list"}})
					assertRows(initial, mine...)
					off, pressed := parsedListFormFilterToggle(t, initial)
					if pressed != "true" || off.Get("lf_off") != "0" {
						t.Fatalf("initial toggle: pressed=%q query=%v", pressed, off)
					}
					preference(wantField, wantDir)
					disabled := get(off)
					all := append(append([]string(nil), mine...), others...)
					if opening == "restored" {
						all = append(append([]string(nil), others...), mine...)
					}
					assertRows(disabled, all...)
					preference(wantField, wantDir)
					off.Set("f.Наименование", "Чужая")
					panel := get(off)
					assertRows(panel, others...)
					enable, pressed := parsedListFormFilterToggle(t, panel)
					if pressed != "false" || enable.Has("lf_off") || enable.Get("f.Наименование") != "Чужая" {
						t.Fatalf("reenable lost filter state: pressed=%q query=%v", pressed, enable)
					}
					for key, value := range map[string]string{"lm": mode, "limit": "10", "q": "a", "subsystem": "sales", "view": "list"} {
						if enable.Get(key) != value {
							t.Fatalf("reenable lost %s: %v", key, enable)
						}
					}
					if opening == "default" {
						if len(enable["sort_default"]) != 1 || enable.Get("sort_default") != "1" || enable.Has("sort") || enable.Has("sort_snapshot") {
							t.Fatalf("reenable lost default order: %v", enable)
						}
					} else if enable.Get("sort_snapshot") != "1" || enable.Get("sort") != "Наименование" || enable.Get("dir") != "desc" || enable.Has("sort_default") {
						t.Fatalf("reenable lost captured order: %v", enable)
					}
					preference(wantField, wantDir)
					// The same rendered link must still combine both filters after a
					// newer choice in a second tab, without overwriting that choice.
					newDir := "desc"
					if opening == "restored" {
						newDir = "asc"
					}
					choose(newDir)
					assertRows(get(enable))
					preference("Наименование", newDir)
					// Removing only the panel filter exposes the captured old order,
					// while the reenabled form filter still excludes all foreign rows.
					enable.Del("f.Наименование")
					assertRows(get(enable), mine...)
					preference("Наименование", newDir)
					freshMine := names[:2]
					if newDir == "desc" {
						freshMine = []string{names[1], names[0]}
					}
					assertRows(get(url.Values{"lm": {mode}}), freshMine...)
					preference("Наименование", newDir)
					t.Logf("mounted filter/default-snapshot contract verified: %s/%s", opening, mode)
				})
			})
		}
	}
}
