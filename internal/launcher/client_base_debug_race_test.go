package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// Замечания круга 3. Оба про одно: отказ для клиентской записи обязан стоять
// там, где локальная база ещё не открыта, и опираться на вид записи, актуальный
// ПОД той же блокировкой, под которой база открывается.

// countFiles — сколько файлов в каталоге, рекурсивно. Локальная база, созданная
// по ошибке, видна как новый файл.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// Прямые входы отладчика и одноразового кода зарегистрированы ВНЕ
// cfgDBRead/cfgAuth middleware, поэтому общий отказ их не покрывал:
// cfgAdminAuthorized открывал getAuthDB и создавал клиентской записи локальный
// onebase_<id>.db по её пустым полям.
func TestDebugAndOneTimeCodeRejectClientBaseWithoutCreatingDatabase(t *testing.T) {
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

	// Путь, который legacy-ветка выдумывает по пустым полям записи. Считать файлы
	// во всём os.TempDir() нельзя: каталог общий для машины, и на CI в нём есть
	// подкаталоги без доступа (/tmp/snap-private-tmp на ubuntu-runner) — обход
	// падал бы на permission denied. Проверяем конкретный файл.
	legacyDB := filepath.Join(os.TempDir(), "onebase_"+base.ID+".db")
	if err := os.Remove(legacyDB); err != nil && !os.IsNotExist(err) {
		t.Fatalf("подготовка: %v", err)
	}
	regBefore := countFiles(t, dir)

	cases := []struct {
		name   string
		method string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"debug/status", http.MethodGet, "/bases/" + base.ID + "/debug/status", h.debugProxy},
		{"one-time-code", http.MethodPost, "/bases/" + base.ID + "/one-time-code", h.oneTimeCodeProxy},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.target, strings.NewReader(""))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", base.ID)
		rctx.URLParams.Add("action", "status")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		c.call(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("%s: код %d, ожидался 409\nответ: %s", c.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "подключение к работающему серверу") {
			t.Errorf("%s: в отказе нет причины: %s", c.name, rec.Body.String())
		}
	}

	// Главное: локальная база не создана ни в temp, ни рядом с реестром.
	if _, err := os.Stat(legacyDB); err == nil {
		t.Errorf("создана локальная база %s", legacyDB)
		_ = os.Remove(legacyDB)
	}
	if got := countFiles(t, dir); got != regBefore {
		t.Errorf("в каталоге реестра появились файлы: было %d, стало %d", regBefore, got)
	}
}

// Гонка local → client. Проверка вида стояла ДО взятия блокировки, поэтому
// запись успевала стать клиентской между проверкой и lease: ожидавший запрос
// проходил как локальный и открывал локальную базу уже клиентской записи.
//
// Сценарий ревью воспроизводится так: держим exclusive lease (как это делает
// сохранение записи), параллельно пускаем вход в конфигуратор — он ждёт read
// lease. Пока он ждёт, запись становится клиентской. После освобождения вход
// обязан получить отказ, а не 200.
func TestConfiguratorRefusesBaseThatBecameClientWhileWaitingForLease(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	local := &Base{
		Name: "База", DBType: "sqlite", DBPath: filepath.Join(dir, "base.db"),
		ConfigSource: "database", Port: 18066,
	}
	if err := store.Add(local); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	// Exclusive lease удерживает запись, как это делает сохранение формы базы.
	release := acquireCfgDBExclusive(local.ID)

	reached := make(chan int, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/bases/"+local.ID+"/configurator/login", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", local.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		// next намеренно пустой: нас интересует решение middleware, а не страница
		// входа, которая открыла бы базу и задержала файл в temp.
		h.cfgDBReadMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		reached <- rec.Code
	}()

	// Пауза задаёт ПОРЯДОК, а не ждёт результата: горутина должна успеть пройти
	// то место, где проверка вида стояла раньше, и упереться в read lease. Без
	// этого Update успевал первым, ранняя проверка видела уже клиентскую запись и
	// отказывала — окно не воспроизводилось, и тест проходил даже на
	// неисправленном коде.
	time.Sleep(150 * time.Millisecond)

	// Пока вход ждёт lease, запись становится клиентской — ровно то окно, которое
	// ранняя проверка не закрывала.
	client := &Base{ID: local.ID, Name: local.Name, ServerURL: "https://srv:8443", Created: local.Created}
	if err := store.Update(client); err != nil {
		t.Fatal(err)
	}
	release()

	wg.Wait()
	code := <-reached
	if code != http.StatusConflict {
		t.Fatalf("после смены вида под блокировкой вход вернул %d, ожидался 409", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "base.db")); err == nil {
		t.Error("создана локальная база записи, ставшей клиентской")
	}
}

// Та же гонка для прямого входа отладчика: он приходит мимо middleware, поэтому
// перечитывает запись сам.
func TestDebugProxyRefusesBaseThatBecameClient(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	local := &Base{
		Name: "База", DBType: "sqlite", DBPath: filepath.Join(dir, "base.db"),
		ConfigSource: "database", Port: 18067,
	}
	if err := store.Add(local); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	// Обработчик читает запись сам; подменяем её до вызова — так же, как это
	// произошло бы между чтением и открытием базы.
	stale := *local
	client := &Base{ID: local.ID, Name: local.Name, ServerURL: "https://srv:8443", Created: local.Created}
	if err := store.Update(client); err != nil {
		t.Fatal(err)
	}

	ok, err := h.cfgAdminAuthorized(httptest.NewRequest(http.MethodGet, "/bases/"+local.ID+"/debug/status", nil), &stale)
	if ok {
		t.Error("устаревшая локальная запись получила доступ к конфигуратору")
	}
	if err == nil || !strings.Contains(err.Error(), "client connection") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "base.db")); statErr == nil {
		t.Error("создана локальная база по устаревшей записи")
	}
}
