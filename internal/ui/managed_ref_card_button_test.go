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
	"github.com/ivantit66/onebase/internal/project"
)

// ref_card_button: false выключает кнопку «Открыть карточку» (🔍) у ссылочных
// полей формы. Тест проходит весь пользовательский путь: YAML проекта →
// project.Load → production HTTP-маршрут формы → итоговый HTML. Так он ловит
// разрыв связи RefCardButton → renderEntityForm, который прямой вызов шаблона
// не замечает.
func TestManagedRefCardButtonThroughHTTP(t *testing.T) {
	for _, tc := range []struct {
		name        string
		formSetting string
		wantButton  bool
	}{
		{name: "ключ отсутствует", wantButton: true},
		{name: "явный false", formSetting: "  ref_card_button: false\n", wantButton: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, path := range []string{
				filepath.Join(dir, "catalogs"),
				filepath.Join(dir, "documents"),
				filepath.Join(dir, "forms", "заказ"),
			} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("создание каталога %s: %v", path, err)
				}
			}

			if err := os.WriteFile(filepath.Join(dir, "catalogs", "клиент.yaml"), []byte(`name: Клиент
title: Клиент
fields:
  - name: Наименование
    type: string
`), 0o644); err != nil {
				t.Fatalf("запись справочника: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "documents", "заказ.yaml"), []byte(`name: Заказ
title: Заказ
fields:
  - name: Клиент
    type: reference:Клиент
`), 0o644); err != nil {
				t.Fatalf("запись документа: %v", err)
			}
			formYAML := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
` + tc.formSetting + `elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
`
			if err := os.WriteFile(filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), []byte(formYAML), 0o644); err != nil {
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
			req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Заказ")+"/new", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("GET формы → %d: %.800s", response.Code, response.Body.String())
			}

			html := response.Body.String()
			hasButton := strings.Contains(html, `data-ob-ref-current="ref-Клиент"`)
			if hasButton != tc.wantButton {
				t.Fatalf("наличие кнопки «Открыть карточку» = %v, ожидалось %v:\n%.1200s", hasButton, tc.wantButton, html)
			}
		})
	}
}
