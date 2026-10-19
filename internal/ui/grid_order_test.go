package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// Порядок колонок таблицы формы за пользователем (как в 1С): POST
// /ui/_grid-order сохраняет его, следующая отрисовка формы отдаёт его в
// data-sg-order, пустой порядок возвращает стандартный. Путь — тот же, что у
// браузера: обработчик сохранения и обработчик формы обработки.

func gridOrderTestServer(t *testing.T) (*Server, *processor.Processor) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "grid.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "ТЧНайденные", DataPath: "Объект.Найденные",
		}},
	}
	proc := &processor.Processor{
		Name: "ПоискЗаявок", Title: "Поиск заявок",
		TableParts: []metadata.TablePart{{Name: "Найденные", Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Адрес", Type: metadata.FieldTypeString},
			{Name: "Мастер", Type: metadata.FieldTypeString},
		}}},
		Forms: []*metadata.FormModule{form},
	}
	registry := runtime.NewRegistry()
	registry.LoadProcessors([]*processor.Processor{proc})
	return &Server{store: db, reg: registry}, proc
}

func gridOrderUser(login string) *auth.User {
	return &auth.User{Login: login, Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{"ПоискЗаявок": {"run"}},
	}}}}
}

func gridOrderPost(t *testing.T, s *Server, u *auth.User, grid, order string) int {
	t.Helper()
	body := url.Values{"grid": {grid}, "order": {order}}
	req := httptest.NewRequest(http.MethodPost, "/ui/_grid-order", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.gridOrderSave(rec, req)
	return rec.Code
}

func gridOrderRendered(t *testing.T, s *Server, u *auth.User) (layout, order string, resetHidden bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/processor/ПоискЗаявок", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "ПоискЗаявок")
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), u))
	rec := httptest.NewRecorder()
	s.processorForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("форма обработки: status=%d, body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	host := keyboardFindHTML(doc, func(n *html.Node) bool {
		v, ok := keyboardHTMLAttr(n, "data-sg-tp")
		return n.Type == html.ElementNode && ok && v == "Найденные"
	})
	if host == nil {
		t.Fatal("таблица формы не отрисована")
	}
	layout, _ = keyboardHTMLAttr(host, "data-sg-layout")
	order, _ = keyboardHTMLAttr(host, "data-sg-order")
	reset := keyboardFindHTML(doc, func(n *html.Node) bool {
		_, ok := keyboardHTMLAttr(n, "data-ob-grid-order-reset")
		return n.Type == html.ElementNode && ok
	})
	if reset == nil {
		t.Fatal("нет кнопки «Стандартный порядок колонок»")
	}
	style, _ := keyboardHTMLAttr(reset, "style")
	return layout, order, strings.Contains(style, "display:none")
}

func TestGridOrder_SavedPerUserAndRendered(t *testing.T) {
	s, _ := gridOrderTestServer(t)
	anna, oleg := gridOrderUser("anna"), gridOrderUser("oleg")
	const grid = "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные"

	layout, order, hidden := gridOrderRendered(t, s, anna)
	if layout != grid || order != "" || !hidden {
		t.Fatalf("до перестановки: layout=%q order=%q resetHidden=%v", layout, order, hidden)
	}

	if code := gridOrderPost(t, s, anna, grid, `["Мастер","Номер","Адрес"]`); code != http.StatusNoContent {
		t.Fatalf("сохранение порядка: code=%d", code)
	}
	if _, order, hidden = gridOrderRendered(t, s, anna); order != `["Мастер","Номер","Адрес"]` || hidden {
		t.Fatalf("после перестановки: order=%q resetHidden=%v", order, hidden)
	}
	if _, order, _ = gridOrderRendered(t, s, oleg); order != "" {
		t.Fatalf("порядок одного пользователя достался другому: %q", order)
	}

	if code := gridOrderPost(t, s, anna, grid, ""); code != http.StatusNoContent {
		t.Fatalf("сброс порядка: code=%d", code)
	}
	if _, order, hidden = gridOrderRendered(t, s, anna); order != "" || !hidden {
		t.Fatalf("после сброса: order=%q resetHidden=%v", order, hidden)
	}
}

// Ключ проверяется по метаданным и правам: иначе в _settings можно было бы
// писать что угодно.
func TestGridOrder_RejectsUnknownOrForeignGrid(t *testing.T) {
	s, _ := gridOrderTestServer(t)
	anna := gridOrderUser("anna")
	for _, grid := range []string{
		"processor/ПоискЗаявок/ФормаОбъекта/НетТакой",
		"processor/НетОбработки/ФормаОбъекта/ТЧНайденные",
		"processor/ПоискЗаявок/ДругаяФорма/ТЧНайденные",
		"произвольный.ключ",
	} {
		if code := gridOrderPost(t, s, anna, grid, `["Номер"]`); code != http.StatusNotFound {
			t.Errorf("ключ %q: code=%d, ожидался 404", grid, code)
		}
	}
	stranger := &auth.User{Login: "stranger", Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{},
	}}}}
	if code := gridOrderPost(t, s, stranger, "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные", `["Номер"]`); code != http.StatusNotFound {
		t.Errorf("без права запуска обработки: code=%d, ожидался 404", code)
	}
	if code := gridOrderPost(t, s, anna, "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные", `{"a":1}`); code != http.StatusBadRequest {
		t.Errorf("order не массив: code=%d, ожидался 400", code)
	}
}
