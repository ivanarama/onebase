package ui

// Персональный вид списка (#1485): явный выбор «Список/Плитка» сохраняется
// по пользователю и сущности, открытие без параметра восстанавливает его,
// приоритет — у явного выбора. Всё через публичный HTTP-обработчик списка.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func listView1485(t *testing.T, s *Server, user *auth.User, query string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/ui/catalog/клиент"+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	ctx := context.Background()
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список не открылся: %d", rec.Code)
	}
	return rec.Body.String()
}

func user1485(login string) *auth.User { return &auth.User{Login: login, IsAdmin: true} }

func uuidMust(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}

func savedView1485(t *testing.T, s *Server, user, entity string) string {
	t.Helper()
	v, _ := s.store.GetListViewUserSettings(context.Background(), entity, user)
	return v
}

func TestListViewPersistence1485(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	u1, u2 := user1485("u1"), user1485("u2")
	// tile-grid рендерится только при наличии строк
	if err := s.store.Upsert(context.Background(), ent.Name, uuidMust("11111111-1111-1111-1111-111111111111"),
		map[string]any{"Наименование": "Иван"}, ent); err != nil {
		t.Fatalf("запись строки: %v", err)
	}

	// Явный выбор «Плитка» сохраняется.
	body := listView1485(t, s, u1, "?view=tiles")
	if !strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("явный ?view=tiles не применился к странице")
	}
	if savedView1485(t, s, "u1", "Клиент") != "tiles" {
		t.Fatal("выбор плитки не сохранён для пользователя")
	}
	// Повторное открытие без параметра восстанавливает плитку.
	if body = listView1485(t, s, u1, ""); !strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("открытие без параметра не восстановило плитку")
	}
	// Другой пользователь — свой (по умолчанию «Список»).
	if body = listView1485(t, s, u2, ""); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("чужой пользователь получил вид u1")
	}
	// Явный «Список» сохраняется и вытесняет плитку.
	if body = listView1485(t, s, u1, "?view=list"); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("явный ?view=list не применился")
	}
	if savedView1485(t, s, "u1", "Клиент") != "list" {
		t.Fatal("выбор списка не сохранён")
	}
	if body = listView1485(t, s, u1, ""); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("после явного «Список» плитка вернулась")
	}
	// Невалидное значение параметра игнорируется, но не портит сохранённое.
	if body = listView1485(t, s, u1, "?view=банан"); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("невалидный view не должен применяться")
	}
	if savedView1485(t, s, "u1", "Клиент") != "list" {
		t.Fatal("невалидный view не должен перезаписывать сохранённый")
	}
}

func TestListViewPersistence1485_NoUserNoPersistence(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	if err := s.store.Upsert(context.Background(), ent.Name, uuidMust("22222222-2222-2222-2222-222222222222"),
		map[string]any{"Наименование": "Пётр"}, ent); err != nil {
		t.Fatalf("запись строки: %v", err)
	}
	// Без пользователя явный выбор работает на странице...
	body := listView1485(t, s, nil, "?view=tiles")
	if !strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("явный ?view=tiles не применился без авторизации")
	}
	// ...но ничего не сохраняется: следующее открытие — снова список.
	if body = listView1485(t, s, nil, ""); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("без авторизации не должно быть персистентности")
	}
}

// Регрессия иерархического вида: нормализация вида съедала ?view=tree, и кнопка
// «Дерево» открывала плоский список. Дерево обязано открываться и при этом НЕ
// запоминаться в настройки — его выбирают заново.
func TestListViewPersistence1485_TreeViewOpensAndIsNotSaved(t *testing.T) {
	ent := &metadata.Entity{
		Name:         "Проект",
		Kind:         metadata.KindCatalog,
		Hierarchical: true,
		Fields:       []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	if err := s.store.Upsert(context.Background(), ent.Name, uuidMust("33333333-3333-3333-3333-333333333333"),
		map[string]any{"Наименование": "Проект №1"}, ent); err != nil {
		t.Fatalf("запись строки: %v", err)
	}

	req := httptest.NewRequest("GET", "/ui/catalog/проект?view=tree", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "проект")
	ctx := auth.ContextWithUser(context.Background(), user1485("u1"))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список не открылся: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "data-tree-id") {
		t.Fatal("явный ?view=tree не открыл дерево: нормализация съела tree")
	}
	if savedView1485(t, s, "u1", "Проект") != "" {
		t.Fatal("дерево не должно сохраняться в настройки вида")
	}
}

// Переключатель вида обязан возвращать из плитки. Кнопка «Список» раньше
// убирала параметр view, а открытие без параметра восстанавливает
// сохранённый вид — то есть ту же плитку: выбрав её однажды, вернуться к списку
// через интерфейс было нельзя. Тест идёт по ссылке самой кнопки, как пользователь,
// а не по сконструированному руками ?view=list.
func TestListViewPersistence1485_ListButtonLeavesTiles(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	if err := s.store.Upsert(context.Background(), ent.Name, uuidMust("44444444-4444-4444-4444-444444444444"),
		map[string]any{"Наименование": "Иван"}, ent); err != nil {
		t.Fatalf("запись строки: %v", err)
	}
	u := user1485("u1")

	body := listView1485(t, s, u, "?view=tiles")
	if !strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("явный ?view=tiles не применился к странице")
	}
	href := viewButtonHref1485(t, body, "Список")
	body = listView1485(t, s, u, href)
	if strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatalf("кнопка «Список» (%s) оставила плитку", href)
	}
	if got := savedView1485(t, s, "u1", "Клиент"); got != "list" {
		t.Fatalf("после кнопки «Список» сохранён вид %q, ожидался list", got)
	}
	// Следующее открытие без параметра — снова список.
	if body = listView1485(t, s, u, ""); strings.Contains(body, "class=\"tile-grid\"") {
		t.Fatal("после возврата к списку повторное открытие снова дало плитку")
	}
}

// viewButtonHref1485 достаёт адрес кнопки переключателя вида по её подписи.
func viewButtonHref1485(t *testing.T, body, title string) string {
	t.Helper()
	i := strings.Index(body, `title="`+title+`">`)
	if i < 0 {
		t.Fatalf("кнопки «%s» нет на странице", title)
	}
	start := strings.LastIndex(body[:i], `href="`)
	if start < 0 {
		t.Fatalf("у кнопки «%s» нет href", title)
	}
	start += len(`href="`)
	end := strings.Index(body[start:], `"`)
	href := strings.ReplaceAll(body[start:start+end], "&amp;", "&")
	if href == "?" {
		return ""
	}
	return href
}
