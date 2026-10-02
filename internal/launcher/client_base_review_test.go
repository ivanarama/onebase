package launcher

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Замечания круга 1 по клиентским записям. Каждый тест закрывает один дефект:
// их общая черта — лаунчер применял к записи, которой не владеет, правила
// владения (остановка при удалении, кнопка «Остановить»), либо обходил правила,
// которые обязан соблюдать (защита параметров запуска у работающего процесса).

func clientReviewHandler(t *testing.T) (*handler, *Store) {
	t.Helper()
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "ibases.yaml")}
	return &handler{store: store, runner: NewRunner()}, store
}

func postTo(t *testing.T, h func(http.ResponseWriter, *http.Request), target, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	if id != "" {
		rctx.URLParams.Add("id", id)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// Удаление клиентской записи не должно упираться в запрет остановки чужого
// процесса: иначе убрать строку из списка нельзя вовсе, даже когда сервер
// недоступен.
func TestDeleteClientBaseRemovesRecord(t *testing.T) {
	h, store := clientReviewHandler(t)
	client, err := NewClientBase("Сервер", "https://srv:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(client); err != nil {
		t.Fatal(err)
	}

	rec := postTo(t, h.delete, "/bases/"+client.ID+"/delete", client.ID, nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("удаление вернуло %d %s, ожидался редирект", rec.Code, rec.Body.String())
	}
	if _, err := store.Get(client.ID); err == nil {
		t.Error("клиентская запись осталась в реестре после удаления")
	}
}

// Переключение вида записи — изменение параметров запуска. У работающей базы его
// нельзя разрешать: иначе прежний сервер продолжает работать, а лаунчер уже
// считает запись чужой и проходит мимо неё при остановке.
func TestUpdateRefusesKindSwitchWhileBaseOccupied(t *testing.T) {
	h, store := clientReviewHandler(t)
	local := &Base{Name: "Локальная", DB: "postgres://localhost/x", ConfigSource: "database"}
	if err := store.Add(local); err != nil {
		t.Fatal(err)
	}
	// Занимаем порт записи: именно так RuntimeStatus видит «процесс на месте».
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(local.Port))
	if err != nil {
		t.Skipf("не удалось занять порт %d: %v", local.Port, err)
	}
	defer func() { _ = ln.Close() }()

	rec := postTo(t, h.update, "/bases/"+local.ID, local.ID, url.Values{
		"name":          {"Локальная"},
		"base_kind":     {baseKindClient},
		"server_url":    {"https://srv:8443"},
		"config_source": {"database"},
	})
	if rec.Code == http.StatusFound {
		t.Fatal("смена вида у занятой базы прошла — защита параметров запуска обойдена")
	}
	stored, err := store.Get(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Client() {
		t.Error("запись стала клиентской, пока её порт занят")
	}
	if stored.Port == 0 {
		t.Error("порт работающей базы обнулён")
	}
	// Форма должна вернуться с выбранным клиентским видом, иначе пользователь
	// теряет введённый адрес.
	if !strings.Contains(rec.Body.String(), `value="client" selected`) {
		t.Error("форма сбросила выбранный вид записи")
	}
}

// Смена вида считается изменением параметров запуска — на этом держится проверка
// выше, поэтому фиксируем правило отдельно.
func TestRuntimeConfigChangedCountsServerURL(t *testing.T) {
	local := &Base{Name: "База", DB: "postgres://localhost/x", ConfigSource: "database", Port: 8080}
	client := &Base{Name: "База", ServerURL: "https://srv:8443"}
	if !runtimeConfigChanged(local, client) {
		t.Error("переключение в клиентскую запись не считается сменой параметров запуска")
	}
	if !runtimeConfigChanged(client, local) {
		t.Error("возврат из клиентской записи не считается сменой параметров запуска")
	}

	// Решающая пара: записи различаются ТОЛЬКО адресом сервера. Без учёта
	// ServerURL функция вернула бы false, и смена адреса у работающей базы прошла
	// бы мимо проверки «сначала остановите базу» — остальные кейсы этого не
	// доказывают, они различаются ещё и DSN.
	moved := &Base{Name: "База", ServerURL: "https://other:8443"}
	if !runtimeConfigChanged(client, moved) {
		t.Error("смена адреса сервера не считается сменой параметров запуска")
	}

	same := &Base{Name: "База", ServerURL: "https://srv:8443"}
	if runtimeConfigChanged(client, same) {
		t.Error("одинаковые клиентские записи признаны изменёнными")
	}
}

// Ошибка адреса при создании не должна сбрасывать выбранный вид: иначе поле
// адреса скрывается и исправить введённое нельзя.
func TestCreateKeepsClientKindOnAddressError(t *testing.T) {
	h, _ := clientReviewHandler(t)
	rec := postTo(t, h.create, "/bases", "", url.Values{
		"name":       {"Сервер"},
		"base_kind":  {baseKindClient},
		"server_url": {""},
	})
	body := rec.Body.String()
	if !strings.Contains(body, `value="client" selected`) {
		t.Error("вид записи сброшен на локальный после ошибки адреса")
	}
	if !strings.Contains(body, `id="server-row"`) || strings.Contains(body, `id="server-row" style="display:none`) {
		t.Error("поле адреса скрыто — исправить введённое нельзя")
	}
}
