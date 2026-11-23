package ui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Пользователь с двумя вкладками браузера — две оболочки, две подписки на
// /ui/events. Команда формы самому себе (ui.открытьФорму) из обработчика
// формы обязана прийти только в оболочку, откуда пришло событие: раньше форма
// открывалась копией и во второй вкладке, с повторным ПриОткрытии, а
// ui.закрытьФорму в фоновой вкладке копию не закрывала (заявка колл-центра,
// :8443, 22.10). Тост по-прежнему идёт во все оболочки. Путь: YAML + .form.os →
// зарегистрированные маршруты /ui/events и form-event.
func TestFormCommandToSelfGoesOnlyToSourceShell(t *testing.T) {
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "shell.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заказ\nelements:\n" +
		"  - kind: Кнопка\n    name: КнОткрыть\n    events:\n      Нажатие: Открыть\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	module := "Процедура Открыть()\n" +
		"  ОтправитьУведомление(\"anna\", \"ui.открытьФорму\", Новый Структура(\"вид,сущность,id\", \"catalog\", \"Заказ\", \"x\"));\n" +
		"  ОтправитьУведомление(\"anna\", \"ui.оповещение\", \"всем вкладкам\");\n" +
		"КонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заказ")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
	if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: []*metadata.Entity{ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := New(reg, db, interp, nil, Config{}, nil)
	user := &auth.User{ID: "u-anna", Login: "anna", IsAdmin: true}
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
			})
		})
		srv.Mount(r)
	})
	server := httptest.NewServer(router)
	defer func() {
		srv.CloseEventStreams()
		server.Close()
		srv.Close()
	}()

	subscribe := func(shell string) *http.Response {
		t.Helper()
		// Тайм-аут клиента обрывает чтение потока: без него тест, не дождавшись
		// кадра, висел бы до общего тайм-аута пакета.
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(server.URL + "/ui/events?shell=" + shell)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	shellA, shellB := subscribe("shell-a"), subscribe("shell-b")
	defer func() { _ = shellA.Body.Close(); _ = shellB.Body.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.hub.SubscriberCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("обе оболочки не подписались за 2с")
		}
		time.Sleep(5 * time.Millisecond)
	}

	form := url.Values{"_element": {"КнОткрыть"}, "_event": {"Нажатие"}, "Наименование": {"заказ"}}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/ui/catalog/заказ/form-event", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-OneBase-Shell", "shell-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&event)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || decodeErr != nil || !event.OK {
		t.Fatalf("form-event: %d ok=%v %q %v", resp.StatusCode, event.OK, event.Error, decodeErr)
	}

	// Один буферизованный читатель на поток: кадры приходят пачкой, и новый
	// bufio.Reader на каждое чтение терял бы уже прочитанное.
	readerA, readerB := bufio.NewReader(shellA.Body), bufio.NewReader(shellB.Body)
	frameName := func(stream *bufio.Reader) string {
		t.Helper()
		var frame struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(readDataFrame(t, stream)), &frame); err != nil {
			t.Fatal(err)
		}
		return frame.Name
	}
	if got := frameName(readerA); got != "ui.открытьФорму" {
		t.Fatalf("оболочка-источник: первым пришло %q, ожидалась команда ui.открытьФорму", got)
	}
	if got := frameName(readerA); got != "ui.оповещение" {
		t.Fatalf("оболочка-источник: вторым пришло %q, ожидался тост", got)
	}
	// Во второй вкладке команды нет: первым в ней идёт тост.
	if got := frameName(readerB); got != "ui.оповещение" {
		t.Fatalf("вторая оболочка: первым пришло %q — команда формы ушла не только в источник", got)
	}
}
