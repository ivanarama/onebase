package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Открываем карточку через публичный маршрут: у ValueTable заголовок должен
// оставаться внутри блока заданной высоты, а у настоящей ТЧ — сохраняться.
func TestManagedTableTitle_PublicForm(t *testing.T) {
	for _, kind := range []string{"ValueTable", "TablePartGrid", "TablePartDOM"} {
		valueTable := kind == "ValueTable"
		for _, readonly := range []bool{false, true} {
			for _, height := range []int{0, 240} {
				t.Run(fmt.Sprintf("%s/readonly=%v/height=%d", kind, readonly, height), func(t *testing.T) {
					const title = "История подписок"
					el := &metadata.FormElement{
						Kind:     metadata.FormElementTablePart,
						Name:     "ТаблицаИстории",
						DataPath: "Объект.История",
						TitleMap: map[string]string{"ru": title},
						ReadOnly: readonly,
						Height:   height,
						NoGrid:   kind == "TablePartDOM",
					}
					ent := layoutTestEntity(el)
					if valueTable {
						el.DataPath = "Форма.История"
						ent.Forms[0].Attributes = []*metadata.FormAttribute{{
							Name: "История", TypeRef: "ValueTable",
							Columns: []*metadata.FormAttributeColumn{{Name: "Товар", TypeRef: "string"}},
						}}
					} else {
						ent.TableParts = []metadata.TablePart{{
							Name:   "История",
							Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
						}}
					}
					s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
					router := chi.NewRouter()
					s.Mount(router)
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/Клиент/new", nil))
					if rec.Code != http.StatusOK {
						t.Fatalf("open form: status=%d body=%s", rec.Code, rec.Body.String())
					}
					doc, err := html.Parse(strings.NewReader(rec.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					var headings []*html.Node
					var table, layout *html.Node
					var walk func(*html.Node)
					walk = func(n *html.Node) {
						if n.Type == html.ElementNode {
							if n.Data == "h3" && n.FirstChild != nil && n.FirstChild.Data == title {
								headings = append(headings, n)
							}
							for _, a := range n.Attr {
								if (a.Key == "data-vt" || a.Key == "data-tp" || a.Key == "data-sg-tp") && a.Val == "История" {
									table = n
								}
								if a.Key == "class" && strings.Contains(a.Val, "managed-vt-layout") {
									layout = n
								}
							}
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							walk(c)
						}
					}
					walk(doc)
					if len(headings) != 1 {
						t.Fatalf("table headings = %d, want 1", len(headings))
					}
					if table == nil {
						t.Fatal("table missing from form")
					}
					if valueTable && height > 0 {
						if layout == nil || headings[0].Parent != layout || table.Parent != layout {
							t.Fatal("ValueTable heading and table must share the height container")
						}
					}
				})
			}
		}
	}
}
