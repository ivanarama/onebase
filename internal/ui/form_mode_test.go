package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// newServerForFormMode конструирует минимальный *Server для тестов режима
// открытия форм (мирроринг TestAgentSettings_ForbiddenForNonAdmin: реестр +
// SQLite, но с миграцией и виджет-кэшем, чтобы прошёл полный рендер index).
func newServerForFormMode(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	return &Server{
		store:       db,
		reg:         runtime.NewRegistry(),
		messages:    NewMessageStore(),
		widgetCache: widget.NewCache(60 * time.Second),
	}
}

func TestIndex_RedirectsToTabsWhenTabsMode(t *testing.T) {
	s := newServerForFormMode(t)
	ctx := context.Background()
	if err := s.store.SaveFormOpenMode(ctx, storage.FormModeTabs); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	// ASCII-имя подсистемы: http.Redirect percent-кодирует не-ASCII байты в
	// Location (стандартная библиотека), поэтому для точного сравнения строки
	// берём ASCII; кириллица сохраняется тем же образом, только в %xx-форме.
	req := httptest.NewRequest(http.MethodGet, "/ui?subsystem=Sales", nil)
	s.index(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получено %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/app?home=1&subsystem=Sales" {
		t.Errorf("Location = %q, ожидалось /ui/app?home=1&subsystem=Sales", loc)
	}
}

func TestIndex_NoRedirectInPagesMode(t *testing.T) {
	s := newServerForFormMode(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	s.index(rec, req)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("в режиме pages редиректа быть не должно, получено %d", rec.Code)
	}
}

func TestSetFormMode_PersistsAndRedirects(t *testing.T) {
	s := newServerForFormMode(t)
	rec := httptest.NewRecorder()
	// Персональный режим хранится по логину, поэтому привязываем пользователя к
	// запросу (анонимная сессия персонального режима не имеет — см. спеку).
	req := httptest.NewRequest(http.MethodPost, "/ui/form-mode", strings.NewReader("mode=tabs"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{Login: "ivan"}))
	s.setFormMode(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получено %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/app" {
		t.Errorf("Location = %q, ожидалось /ui/app", loc)
	}
	// Персональный режим действительно сохранён — последующее разрешение даёт tabs.
	if got := s.store.EffectiveFormOpenMode(req.Context(), "ivan"); got != storage.FormModeTabs {
		t.Errorf("после POST персональный режим не сохранён: %q", got)
	}
}

// TestSetFormMode_AnonymousWritesGlobal: у анонимной сессии (no-auth база,
// login == "") персонального режима нет, поэтому переключатель меняет
// ГЛОБАЛЬНЫЙ режим — иначе кнопка/радио были бы мёртвыми (issue #129/#130,
// фикс блокера ревью). POST mode=tabs → глобальный tabs → редирект в /ui/app.
func TestSetFormMode_AnonymousWritesGlobal(t *testing.T) {
	s := newServerForFormMode(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/form-mode", strings.NewReader("mode=tabs"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.setFormMode(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получено %d", rec.Code)
	}
	// Глобальный режим действительно переключился на tabs.
	if got := s.store.GetFormOpenMode(req.Context()); got != storage.FormModeTabs {
		t.Errorf("аноним не переключил глобальный режим: %q", got)
	}
	// Эффективный режим анонима теперь tabs → редирект в оболочку вкладок.
	if loc := rec.Header().Get("Location"); loc != "/ui/app" {
		t.Errorf("Location = %q, ожидалось /ui/app", loc)
	}
	// Возврат: POST mode=pages → глобальный снова pages → редирект на /ui
	// (доказывает, что в no-auth базе переключатель работает в обе стороны).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/ui/form-mode", strings.NewReader("mode=pages"))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.setFormMode(rec2, req2)
	if got := s.store.GetFormOpenMode(req2.Context()); got != storage.FormModePages {
		t.Errorf("аноним не вернул глобальный режим в pages: %q", got)
	}
	if loc := rec2.Header().Get("Location"); loc != "/ui" {
		t.Errorf("Location(возврат) = %q, ожидалось /ui", loc)
	}
}

func renderNavWithMode(t *testing.T, mode string) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{"Cfg": Config{}, "Lang": "ru", "IsAdmin": false, "FormOpenMode": mode}
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("execute nav: %v", err)
	}
	return buf.String()
}

func TestNav_ShowsTabsToggle(t *testing.T) {
	out := renderNavWithMode(t, storage.FormModePages)
	if !strings.Contains(out, "/ui/form-mode") {
		t.Error("в топбаре нет формы переключателя режима (/ui/form-mode)")
	}
}

// TestParams_HasFormModeRadio проверяет, что в «Параметрах» (меню Система → nav)
// есть радиогруппа режима открытия форм со всеми тремя значениями: персональный
// pages/tabs и сброс к глобальному default.
func TestParams_HasFormModeRadio(t *testing.T) {
	out := renderNavWithMode(t, storage.FormModeTabs)
	for _, want := range []string{`value="pages"`, `value="tabs"`, `value="default"`} {
		if !strings.Contains(out, want) {
			t.Errorf("в «Параметрах» нет радио %s", want)
		}
	}
}

// Закреплённый администратором режим: личный ключ не действует, оба
// переключателя (кнопка в шапке и блок в «Системе») не показываются, а прямой
// POST /ui/form-mode отвечает 403 и ничего не пишет. Повод — оператор в личном
// режиме «страниц», где любой переход уводит форму звонка («Закрыть приложение?»).
func TestFormMode_LockedByAdmin(t *testing.T) {
	s := newServerForFormMode(t)
	ctx := context.Background()
	asIvan := func(req *http.Request) *http.Request {
		return req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{Login: "ivan"}))
	}
	// Глобально pages, у ivan личный tabs: без закрепления /ui уводит его в оболочку.
	if err := s.store.SaveUserFormOpenMode(ctx, "ivan", storage.FormModeTabs); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.index(rec, asIvan(httptest.NewRequest(http.MethodGet, "/ui", nil)))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("без закрепления личный tabs обязан вести в оболочку: %d", rec.Code)
	}

	if err := s.store.SaveFormOpenModeLocked(ctx, true); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.index(rec, asIvan(httptest.NewRequest(http.MethodGet, "/ui", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("при закреплении действует глобальный pages, ждали 200, получено %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "/ui/form-mode") || strings.Contains(body, `class="sys-mode-block"`) {
		t.Error("при закреплении переключатели режима должны быть скрыты")
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/form-mode", strings.NewReader("mode=pages"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.setFormMode(rec, asIvan(req))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /ui/form-mode при закреплении: %d, ждали 403", rec.Code)
	}
	if got := s.store.GetUserFormOpenMode(ctx, "ivan"); got != storage.FormModeTabs {
		t.Errorf("отказанный POST не должен менять личный режим, получено %q", got)
	}
	// Анонимная сессия меняет глобальный режим — и её путь закрыт так же.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/ui/form-mode", strings.NewReader("mode=tabs"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.setFormMode(rec, req)
	if rec.Code != http.StatusForbidden || s.store.GetFormOpenMode(ctx) != storage.FormModePages {
		t.Errorf("анонимный POST при закреплении: код %d, глобальный %q", rec.Code, s.store.GetFormOpenMode(ctx))
	}
}
