package ui

import (
	"bytes"
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
	dom "golang.org/x/net/html"
	"net/http"
	"strings"
	"testing"
)

func TestTabularDrilldownColorHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "ColorMaster", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		if e := db.Migrate(ctx, []*metadata.Entity{target}); e != nil {
			t.Fatal(e)
		}
		if e := db.Upsert(ctx, target.Name, uuid.New(), map[string]any{"Name": "own-master"}, target); e != nil {
			t.Fatal(e)
		}
		for index, color := range []string{"#FF0000", "#FF9900", "#A3A300", "#008000", ""} {
			name := strings.TrimPrefix(color, "#")
			if name == "" {
				name = "default"
			}
			t.Run(name, func(t *testing.T) {
				rep := &reportpkg.Report{Name: fmt.Sprintf("OwnColor%d", index), ResultProc: "Build"}
				src := fmt.Sprintf(`Функция Build() Д=Новый ТабличныйДокумент;Д.Ячейка(1,1).Текст="MASTER";Д.Ячейка(1,1).ЦветТекста="%s";Д.Ячейка(1,1).Расшифровка=Справочники.ColorMaster.НайтиПоРеквизиту("Name","own-master");Возврат Д;КонецФункции`, color)
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}, Reports: []*reportpkg.Report{rep}, Programs: map[string]*ast.Program{rep.Name: mustParse(t, src)}})
				i := interpreter.New()
				i.LookupProc = reg.GetModuleProc
				s := &Server{reg: reg, store: db, interp: i, messages: NewMessageStore(), lockMgr: runtime.NewLockManager(), ops: newOperationLimiter()}
				s.entitySvc = s.newEntityService(nil)
				t.Cleanup(s.Close)
				actor := &auth.User{ID: uuid.NewString(), Login: "own-color", Roles: []*auth.Role{{Name: "ColorReader", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{target.Name: {"read"}}}}}}
				run := tabularHTTP(s, actor, "own-color-session", http.MethodPost, "/ui/report/"+rep.Name, nil)
				if run.Code != 303 {
					t.Fatalf("reportHTTP%d:%s", run.Code, run.Body.String())
				}
				path := run.Header().Get("Location")
				view := tabularHTTP(s, actor, "own-color-session", http.MethodGet, path, nil)
				if view.Code != 200 {
					t.Fatal("viewer unavailable")
				}
				root, e := dom.Parse(strings.NewReader(view.Body.String()))
				if e != nil {
					t.Fatal(e)
				}
				var link *dom.Node
				var walk func(*dom.Node)
				attr := func(n *dom.Node, key string) string {
					for _, a := range n.Attr {
						if a.Key == key {
							return a.Val
						}
					}
					return ""
				}
				walk = func(n *dom.Node) {
					if n.Type == dom.ElementNode && n.Data == "a" && attr(n, "data-sheet-drilldown") == "1" {
						link = n
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
				}
				walk(root)
				if link == nil {
					t.Fatal("no native reference link")
				}
				if (attr(link, "style") == "color:inherit") != (color != "") {
					t.Fatalf("link style=%q color=%q", attr(link, "style"), color)
				}
				href := attr(link, "href")
				if !strings.HasPrefix(href, path+"/cell?row=0&col=0") {
					t.Fatal("changed protected route")
				}
				follow := tabularHTTP(s, actor, "own-color-session", http.MethodGet, href, nil)
				if follow.Code != 303 {
					t.Fatalf("native protected linkHTTP%d", follow.Code)
				}
				x := tabularHTTP(s, actor, "own-color-session", http.MethodGet, path+"/xlsx", nil)
				f, e := excelize.OpenReader(bytes.NewReader(x.Body.Bytes()))
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if color != "" {
					sid, e := f.GetCellStyle(f.GetSheetList()[0], "A1")
					if e != nil {
						t.Fatal(e)
					}
					style, e := f.GetStyle(sid)
					if e != nil || style.Font == nil || !strings.EqualFold(style.Font.Color, strings.TrimPrefix(color, "#")) {
						t.Fatal("XLSX color changed")
					}
				}
				pdf := tabularHTTP(s, actor, "own-color-session", http.MethodGet, path+"/pdf", nil)
				if pdf.Code != 200 || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF-")) {
					t.Fatal("same snapshot PDF unavailable")
				}
				if len(s.tabularResults.items) != 1 {
					t.Fatal("exports reran module")
				}
			})
		}
	})
}
