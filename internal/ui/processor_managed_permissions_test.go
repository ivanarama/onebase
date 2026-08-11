package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Управляемая форма обработки использует виртуальную catalog Entity только как
// описание полей. Право редактировать параметры и ТЧ следует из processor/run,
// а не из несуществующего catalog/<processor>/write.
func TestProcessorManagedForm_TablePartUsesProcessorRunPermission(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name:       "ФормаОбработки",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind:     metadata.FormElementTablePart,
			Name:     "СтрокиФормы",
			DataPath: "Объект.Строки",
		}},
	}
	proc := &processor.Processor{
		Name:  "Импорт",
		Title: "Импорт",
		TableParts: []metadata.TablePart{{
			Name:   "Строки",
			Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}},
		}},
		Forms: []*metadata.FormModule{form},
	}
	registry := runtime.NewRegistry()
	registry.LoadProcessors([]*processor.Processor{proc})
	s := &Server{store: db, reg: registry}

	user := &auth.User{Login: "operator", Roles: []*auth.Role{{
		Name: "Оператор обработок",
		Permissions: auth.Permission{
			Processors: map[string][]string{proc.Name: {"run"}},
			Catalogs:   map[string][]string{},
		},
	}}}
	if !user.Has("processor", proc.Name, "run") || user.Has("catalog", proc.Name, "write") {
		t.Fatal("предусловие теста нарушено: нужно processor/run без catalog/write")
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/processor/Импорт", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", proc.Name)
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), user))
	rec := httptest.NewRecorder()
	s.processorForm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET managed processor: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	host := keyboardFindHTML(doc, func(node *html.Node) bool {
		name, ok := keyboardHTMLAttr(node, "data-sg-tp")
		return node.Type == html.ElementNode && node.Data == "div" && ok && name == "Строки"
	})
	if host == nil {
		t.Fatal("HTTP-рендер обработки потерял SlickGrid табличной части")
	}
	if _, readOnly := keyboardHTMLAttr(host, "data-sg-ro"); readOnly {
		t.Fatal("processor/run ошибочно отрендерен readonly из-за catalog/write")
	}
	if keys, ok := keyboardHTMLAttr(host, "aria-keyshortcuts"); !ok || keys == "" {
		t.Fatal("редактируемый processor grid не объявляет доступные клавиши")
	}
	add := keyboardFindHTML(doc, func(node *html.Node) bool {
		name, ok := keyboardHTMLAttr(node, "data-ob-grid-add")
		return node.Type == html.ElementNode && node.Data == "button" && ok && name == "Строки"
	})
	if add == nil {
		t.Fatal("processor/run не получил кнопку добавления строки")
	}
}

func TestCatalogManagedForm_RemainsReadOnlyWithoutCatalogWrite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	form := &metadata.FormModule{
		Name: "ФормаЭлемента", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "СтрокиФормы", DataPath: "Объект.Строки",
		}},
	}
	ent := &metadata.Entity{
		Name: "Настройка", Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form},
		TableParts: []metadata.TablePart{{
			Name: "Строки", Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
	s := &Server{store: db, reg: registry}
	user := &auth.User{Login: "reader", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{ent.Name: {"read"}},
	}}}}
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/Настройка/id", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	s.renderEntityForm(rec, req, "object", map[string]any{
		"Entity": ent, "IsNew": false, "Values": map[string]string{},
		"RefOptions": map[string]any{}, "EnumOptions": map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]map[string]map[string]string{},
		"TPEnumOrder": map[string]map[string][]string{}, "TPRefMeta": map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Строки": {}},
	})

	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	host := keyboardFindHTML(doc, func(node *html.Node) bool {
		name, ok := keyboardHTMLAttr(node, "data-sg-tp")
		return node.Type == html.ElementNode && ok && name == "Строки"
	})
	if host == nil {
		t.Fatal("catalog managed form lost table-part grid")
	}
	if marker, _ := keyboardHTMLAttr(host, "data-sg-ro"); marker != "1" {
		t.Fatalf("catalog/read without write rendered grid marker=%q, want readonly", marker)
	}
	if _, advertised := keyboardHTMLAttr(host, "aria-keyshortcuts"); advertised {
		t.Fatal("readonly catalog grid advertises unavailable structural shortcuts")
	}
	if strings.Contains(rec.Body.String(), `data-ob-grid-add="Строки"`) {
		t.Fatal("readonly catalog grid exposes add-row action")
	}
}
