package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Конфигуратор лаунчера работает с базой, которой лаунчер владеет. Для записи
// клиентского подключения все его серверные маршруты обязаны отказывать — и
// отказывать ДО открытия БД и любых операций с файлами.
//
// Блокер круга 2: скрытия кнопки в списке баз было недостаточно. У клиентской
// записи пусты ConfigSource, Path и DB, поэтому cfgAuthMiddleware открывал ей
// временную локальную SQLite и при отсутствии пользователей пропускал запрос, а
// configuratorSaveModule уходил в файловую ветку, где SafeJoin с пустым Path
// направлял запись в текущий рабочий каталог: существующий файл конфигурации
// перезаписывался присланным текстом. Тот же путь открыт из вкладки
// конфигуратора, оставшейся после переключения локальной базы в клиентскую.

// configRouteCase — один серверный маршрут конфигуратора.
type configRouteCase struct {
	name   string
	method string
	target string
	form   url.Values
	call   func(*handler, http.ResponseWriter, *http.Request)
}

func TestConfiguratorRoutesRejectClientBaseWithoutTouchingFiles(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	base, err := NewClientBase("Сервер КЦ", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	// Рабочий каталог процесса — то место, куда уходила запись при пустом Path.
	// Кладём туда файл-приманку и каталог src, как в воспроизведении ревью.
	work := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Mkdir(filepath.Join(work, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	bait := filepath.Join(work, "src", "audit.os")
	const baitText = "// исходный модуль, его нельзя переписать\n"
	if err := os.WriteFile(bait, []byte(baitText), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}

	cases := []configRouteCase{
		{
			name: "сохранение модуля", method: http.MethodPost,
			target: "/bases/" + base.ID + "/configurator/module",
			form: url.Values{
				"entity": {"Audit"}, "module_type": {"object"},
				"source": {"// подделанная перезапись\n"},
			},
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgDBReadMiddleware(h.cfgAuthMiddleware(http.HandlerFunc(h.configuratorSaveModule))).ServeHTTP(w, r)
			},
		},
		{
			name: "страница конфигуратора", method: http.MethodGet,
			target: "/bases/" + base.ID + "/configurator",
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgDBReadMiddleware(h.cfgAuthMiddleware(http.HandlerFunc(h.configuratorPage))).ServeHTTP(w, r)
			},
		},
		{
			name: "вход в конфигуратор", method: http.MethodGet,
			target: "/bases/" + base.ID + "/configurator/login",
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgDBReadMiddleware(http.HandlerFunc(h.cfgLoginPage)).ServeHTTP(w, r)
			},
		},
		{
			name: "экспорт конфигурации", method: http.MethodPost,
			target: "/bases/" + base.ID + "/config/export",
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgDBReadMiddleware(http.HandlerFunc(h.configExport)).ServeHTTP(w, r)
			},
		},
		// У групп резервного копирования внешнего read-middleware нет вовсе:
		// отказ там обязан дать cfgAuthMiddleware, иначе restore и full-import
		// остаются достижимыми, а они деструктивны.
		{
			name: "полный экспорт резервной копии", method: http.MethodPost,
			target: "/bases/" + base.ID + "/configurator/backup/full-export",
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgAuthMiddleware(http.HandlerFunc(h.backupFullExport)).ServeHTTP(w, r)
			},
		},
		{
			name: "полный импорт резервной копии", method: http.MethodPost,
			target: "/bases/" + base.ID + "/configurator/backup/full-import",
			call: func(h *handler, w http.ResponseWriter, r *http.Request) {
				h.cfgAuthMiddleware(http.HandlerFunc(h.backupFullImport)).ServeHTTP(w, r)
			},
		},
	}

	for _, c := range cases {
		var body *strings.Reader
		if c.form != nil {
			body = strings.NewReader(c.form.Encode())
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(c.method, c.target, body)
		if c.form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", base.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		c.call(h, rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("%s: код %d, ожидался 409 (отказ клиентской записи)\nответ: %s",
				c.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "подключение к работающему серверу") {
			t.Errorf("%s: в отказе нет причины: %s", c.name, rec.Body.String())
		}
	}

	// Главное: ни один маршрут не создал и не изменил локальных файлов.
	got, err := os.ReadFile(bait)
	if err != nil {
		t.Fatalf("файл-приманка пропал: %v", err)
	}
	if string(got) != baitText {
		t.Errorf("файл конфигурации перезаписан через маршрут конфигуратора:\n%s", got)
	}
	after, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		names := make([]string, 0, len(after))
		for _, e := range after {
			names = append(names, e.Name())
		}
		t.Errorf("в рабочем каталоге появились файлы: было %d, стало %d (%v)",
			len(before), len(after), names)
	}
}

// Запрет адресный: базу, которой лаунчер владеет, он не задевает.
//
// cfgAuthMiddleware здесь сознательно не вызывается: он сам открывает SQLite и
// держит соединение (из-за чего временный каталог теста не удаляется на
// Windows), а проверяемое поведение — это запрет по виду записи, и он живёт в
// rejectClientBaseConfig плюс в cfgDBReadMiddleware, который БД не открывает.
func TestConfiguratorRoutesStillReachLocalBase(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	local := &Base{
		Name: "Локальная", DBType: "sqlite", DBPath: filepath.Join(dir, "base.db"),
		ConfigSource: "database", Port: 18077,
	}
	if err := store.Add(local); err != nil {
		t.Fatal(err)
	}
	client, err := NewClientBase("Сервер", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(client); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	// Сам предикат отказа: клиентскую запись отвергает, локальную пропускает.
	for _, c := range []struct {
		id       string
		name     string
		rejected bool
	}{
		{client.ID, "клиентская", true},
		{local.ID, "локальная", false},
		{"нет-такой", "несуществующая", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/bases/"+c.id+"/configurator", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", c.id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		if got := h.rejectClientBaseConfig(rec, req); got != c.rejected {
			t.Errorf("%s запись: отказ = %v, ожидалось %v", c.name, got, c.rejected)
		}
	}

	// И через middleware: локальная база доходит до обработчика.
	passed := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		passed = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/bases/"+local.ID+"/configurator", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", local.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.cfgDBReadMiddleware(next).ServeHTTP(rec, req)
	if !passed {
		t.Errorf("локальная база не дошла до обработчика, код %d: %s", rec.Code, rec.Body.String())
	}
}
