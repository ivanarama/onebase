package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/project"
)

// #1684: у ссылки на системную таблицу учётных записей (reference:_users)
// карточки нет — кнопка «Открыть карточку» (🔍) у такого поля не рисуется, а
// у обычной ссылки остаётся. Путь пользователя: YAML проекта → project.Load →
// production GET формы. Вторая половина — адрес, который открыла бы кнопка:
// он отвечает текстовой 404 без ui.js, то есть страницей вне протокола
// закрытия, — именно такую вкладку оболочка теперь закрывает сразу
// (tabs_behavior_test.js, «a page outside the close protocol…»).
func TestRefCardButtonHiddenForSystemUsers(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{
		filepath.Join(dir, "catalogs"),
		filepath.Join(dir, "documents"),
		filepath.Join(dir, "forms", "заказ"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "catalogs", "клиент.yaml"), []byte("name: Клиент\nfields:\n  - name: Наименование\n    type: string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "documents", "заказ.yaml"), []byte(`name: Заказ
fields:
  - name: Клиент
    type: reference:Клиент
  - name: Ответственный
    type: reference:_users
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
  - kind: ПолеВвода
    name: ПолеОтветственный
    data_path: Объект.Ответственный
`), 0o644); err != nil {
		t.Fatal(err)
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	s, _ := newSubmitTestServer(t, proj.Entities)
	router := chi.NewRouter()
	s.Mount(router)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	form := get("/ui/document/" + url.PathEscape("Заказ") + "/new")
	if form.Code != http.StatusOK {
		t.Fatalf("GET формы → %d: %.600s", form.Code, form.Body.String())
	}
	html := form.Body.String()
	for _, field := range []string{"Клиент", "Ответственный"} {
		if !strings.Contains(html, `id="ref-`+field+`"`) {
			t.Fatalf("в форме нет ссылочного поля %s:\n%.1200s", field, html)
		}
	}
	// Кнопка ищет поле в своей строке (data-ob-ref-current="closest", #1759):
	// у копий реквизита id совпадает. Поэтому смотрим строку самого поля.
	fieldRow := func(field string) string {
		start := strings.Index(html, `id="ref-`+field+`"`)
		if start < 0 {
			return ""
		}
		end := strings.Index(html[start:], "managed-control-row")
		if end < 0 {
			end = len(html) - start
		}
		return html[start : start+end]
	}
	if !strings.Contains(fieldRow("Клиент"), `data-ob-ref-current=`) {
		t.Error("у обычной ссылки пропала кнопка «Открыть карточку»")
	}
	if strings.Contains(fieldRow("Ответственный"), `data-ob-ref-current=`) {
		t.Error("у reference:_users нарисована кнопка карточки, которой нет")
	}

	open := get("/ui/_ref-open/_users/" + uuid.NewString())
	if open.Code != http.StatusNotFound {
		t.Fatalf("карточка _users: статус %d, ожидалась 404", open.Code)
	}
	if body := open.Body.String(); strings.Contains(body, "ui.js") || strings.Contains(body, "obAnswersFrameClose") {
		t.Fatalf("404 карточки _users стала страницей с протоколом закрытия — тогда закрытие пойдёт обычным путём, проверьте tabs.go:\n%s", body)
	}
}
