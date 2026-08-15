package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/project"
)

// Элемент ТЧ, описанный только ключом `table_part:`, обязан пройти весь путь
// пользовательской конфигурации: YAML проекта → project.Load → HTTP-форма → HTML.
// До #830 загрузчик оставлял data_path пустым, поэтому метаданные загружались,
// но HTTP-форма молча не отрисовывала таблицу.
func TestManagedForm_TablePartKeyRenders(t *testing.T) {
	dir := t.TempDir()
	writeFixture := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("создание каталога %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("запись %s: %v", path, err)
		}
	}

	writeFixture("documents/заказ.yaml", `name: Заказ
title: Заказ
fields:
  - name: Номер
    type: string
tableparts:
  - name: Строки
    fields:
      - name: Номенклатура
        type: string
      - name: Количество
        type: number
`)
	writeFixture("forms/заказ/объекта.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТаблицаСтроки
    table_part: Строки
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	s, _ := newSubmitTestServer(t, proj.Entities)
	router := chi.NewRouter()
	s.Mount(router)
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)

	endpoint := httpServer.URL + "/ui/document/" + url.PathEscape("Заказ") + "/new"
	resp, err := http.Get(endpoint) //nolint:gosec,noctx // адрес тестового httptest-сервера
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("чтение HTTP-ответа: %v", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET формы → %d: %.600s", resp.StatusCode, body)
	}

	html := string(body)
	for _, want := range []string{`data-sg-tp="Строки"`, "Номенклатура", "Количество"} {
		if !strings.Contains(html, want) {
			t.Fatalf("в HTTP-форме нет %q — table_part не дошёл до рендера:\n%.1200s", want, html)
		}
	}
}
