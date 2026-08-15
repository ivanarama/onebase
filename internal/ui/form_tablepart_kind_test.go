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
	documentsDir := filepath.Join(dir, "documents")
	formsDir := filepath.Join(dir, "forms", "заказ")
	for _, path := range []string{documentsDir, formsDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("создание каталога %s: %v", path, err)
		}
	}

	if err := os.WriteFile(filepath.Join(documentsDir, "заказ.yaml"), []byte(`name: Заказ
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
`), 0o644); err != nil {
		t.Fatalf("запись метаданных документа: %v", err)
	}
	if err := os.WriteFile(filepath.Join(formsDir, "объекта.form.yaml"), []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТаблицаСтроки
    table_part: Строки
`), 0o644); err != nil {
		t.Fatalf("запись формы: %v", err)
	}

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
