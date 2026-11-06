package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Строка состояния внизу страницы: оператор работает с базой на весь экран, а
// состояние звонка и свой статус видит в постоянной строке. Содержимое задаёт
// функция конфигурации, кнопки — объявление и обработчик конфигурации.
const statusBarModuleSrc = `
Функция Строка() Экспорт
	Кнопки = Новый Массив;
	Кнопки.Добавить("НеБеспокоить");
	Возврат Новый Структура("Слева,Справа,Кнопки", "Клиент: разговор 02:13", "На линии", Кнопки);
КонецФункции

Функция Нажатие(Кнопка) Экспорт
	Возврат "нажата " + Кнопка;
КонецФункции
`

func newStatusBarTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	prog, err := parser.New(lexer.New(statusBarModuleSrc, "telephony.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.LoadModules(map[string]*ast.Program{"Телефония": prog})
	bar := &metadata.StatusBar{
		Roles: []string{"Оператор"}, Source: "Телефония.Строка", Action: "Телефония.Нажатие", Interval: 2,
		Buttons: []metadata.StatusBarButton{{Name: "НаЛинии", Title: "На линии"}, {Name: "НеБеспокоить", Title: "Не беспокоить"}},
	}
	if err := bar.Validate(); err != nil {
		t.Fatal(err)
	}
	registry.LoadHomePage(&metadata.HomePage{StatusBar: bar})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	srv := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	srv.entitySvc = srv.newEntityService(nil)
	return srv
}

func statusBarUser(role string) *auth.User {
	return &auth.User{Login: "op", Roles: []*auth.Role{{Name: role}}}
}

func statusBarRequest(t *testing.T, s *Server, method string, u *auth.User, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if method == http.MethodPost {
		r = httptest.NewRequest(method, "/ui/_status-bar", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, "/ui/_status-bar", nil)
	}
	r = r.WithContext(auth.ContextWithUser(r.Context(), u))
	w := httptest.NewRecorder()
	if method == http.MethodPost {
		s.statusBarPress(w, r)
	} else {
		s.statusBarGet(w, r)
	}
	return w
}

func decodeStatusBar(t *testing.T, w *httptest.ResponseRecorder) statusBarState {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var st statusBarState
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("ответ не JSON: %v %s", err, w.Body.String())
	}
	return st
}

func TestStatusBar_ContentButtonsAndRoles(t *testing.T) {
	s := newStatusBarTestServer(t)
	operator := statusBarUser("Оператор")

	st := decodeStatusBar(t, statusBarRequest(t, s, http.MethodGet, operator, nil))
	if !st.Visible || st.Left != "Клиент: разговор 02:13" || st.Right != "На линии" || st.Interval != 2 {
		t.Fatalf("содержимое строки: %+v", st)
	}
	// Функция вернула Кнопки — показана только перечисленная, с подписью.
	if len(st.Buttons) != 1 || st.Buttons[0].Name != "НеБеспокоить" || st.Buttons[0].Title != "Не беспокоить" {
		t.Fatalf("кнопки: %+v", st.Buttons)
	}

	pressed := decodeStatusBar(t, statusBarRequest(t, s, http.MethodPost, operator, url.Values{"button": {"НеБеспокоить"}}))
	if pressed.Message != "нажата НеБеспокоить" || !pressed.Visible {
		t.Fatalf("нажатие: %+v", pressed)
	}
	// Объявленная, но скрытая функцией кнопка не нажимается в обход строки.
	if w := statusBarRequest(t, s, http.MethodPost, operator, url.Values{"button": {"НаЛинии"}}); w.Code != http.StatusConflict {
		t.Fatalf("скрытая кнопка: %d %s", w.Code, w.Body.String())
	}
	if w := statusBarRequest(t, s, http.MethodPost, operator, url.Values{"button": {"Нет"}}); w.Code != http.StatusNotFound {
		t.Fatalf("неизвестная кнопка: %d", w.Code)
	}

	// Пользователю без роли строки нет: ни содержимого, ни кнопок.
	other := statusBarUser("Кладовщик")
	if st := decodeStatusBar(t, statusBarRequest(t, s, http.MethodGet, other, nil)); st.Visible || st.Left != "" {
		t.Fatalf("чужая роль видит строку: %+v", st)
	}
	if w := statusBarRequest(t, s, http.MethodPost, other, url.Values{"button": {"НеБеспокоить"}}); w.Code != http.StatusNotFound {
		t.Fatalf("чужая роль нажала кнопку: %d", w.Code)
	}
}

// Признак строки попадает в общий head страницы только тому, кому строка
// положена; без него ui.js строку не рисует.
func TestStatusBar_PageMetaFollowsRole(t *testing.T) {
	s := newStatusBarTestServer(t)
	get := func(u *auth.User) string {
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), u))
		data := map[string]any{}
		s.addStatusBarData(r, data)
		return string(statusBarMetaHTML(data))
	}
	if got := get(statusBarUser("Оператор")); got != `<meta name="ob-status-bar" content="2">` {
		t.Fatalf("признак строки оператору: %q", got)
	}
	if got := get(statusBarUser("Кладовщик")); got != "" {
		t.Fatalf("признак строки чужой роли: %q", got)
	}
	// Данные страницы не картой — шаблон не падает и строки нет.
	if got := statusBarMetaHTML(struct{}{}); got != "" {
		t.Fatalf("не карта: %q", got)
	}
}

func TestStatusBar_ValidateDeclaration(t *testing.T) {
	for _, bad := range []*metadata.StatusBar{
		{Source: "БезМодуля"},
		{Source: "М.Ф", Interval: 61},
		{Source: "М.Ф", Buttons: []metadata.StatusBarButton{{Name: "А"}}},
		{Source: "М.Ф", Action: "М.Д", Buttons: []metadata.StatusBarButton{{Name: "А"}, {Name: "а"}}},
	} {
		if bad.Validate() == nil {
			t.Fatalf("объявление должно быть отклонено: %+v", bad)
		}
	}
}

// Настоящая страница: общий рендер кладёт признак строки в head.
func TestStatusBar_IndexPageCarriesMeta(t *testing.T) {
	s := newStatusBarTestServer(t)
	for role, want := range map[string]bool{"Оператор": true, "Кладовщик": false} {
		r := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), statusBarUser(role)))
		w := httptest.NewRecorder()
		s.index(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: GET /ui/: %d %s", role, w.Code, w.Body.String())
		}
		if got := strings.Contains(w.Body.String(), `<meta name="ob-status-bar" content="2">`); got != want {
			t.Fatalf("%s: признак строки в странице = %v, ожидалось %v", role, got, want)
		}
	}
}
