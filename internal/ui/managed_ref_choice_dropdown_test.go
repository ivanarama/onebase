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

// choice_dropdown: false убирает варианты из выпадающего списка ссылочного
// поля: выбор уходит в форму подбора, а в <select> остаётся плейсхолдер и
// текущее значение. Путь берётся целиком — YAML проекта → project.Load →
// боевой HTTP-маршрут формы → HTML, как в TestManagedRefCardButtonThroughHTTP:
// разрыв между метаданными и рендером прямой вызов шаблона не поймал бы.
func TestManagedRefChoiceDropdownThroughHTTP(t *testing.T) {
	const клиентов = 3

	for _, tc := range []struct {
		name        string
		elSetting   string
		сЗначением  bool
		wantOptions int // включая плейсхолдер «— выбрать —»
	}{
		{name: "ключ отсутствует: список развёрнут", wantOptions: клиентов + 1},
		{name: "choice_dropdown false: только плейсхолдер", elSetting: "    choice_dropdown: false\n", wantOptions: 1},
		{name: "choice_dropdown false: значение видно", elSetting: "    choice_dropdown: false\n", сЗначением: true, wantOptions: 2},
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
			if err := os.WriteFile(filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
`+tc.elSetting), 0o644); err != nil {
				t.Fatalf("запись формы: %v", err)
			}

			proj, err := project.Load(dir)
			if err != nil {
				t.Fatalf("project.Load: %v", err)
			}
			defer proj.Close()

			s, ctx := newSubmitTestServer(t, proj.Entities)
			клиент := uuid.New()
			for i := 0; i < клиентов; i++ {
				id := клиент
				if i > 0 {
					id = uuid.New()
				}
				if err := s.store.Upsert(ctx, "Клиент", id,
					map[string]any{"Наименование": "Клиент " + string(rune('А'+i))}, s.reg.GetEntity("Клиент")); err != nil {
					t.Fatalf("создание клиента: %v", err)
				}
			}

			путь := "/ui/document/" + url.PathEscape("Заказ") + "/new"
			if tc.сЗначением {
				заказ := uuid.New()
				if err := s.store.Upsert(ctx, "Заказ", заказ,
					map[string]any{"Клиент": клиент.String()}, s.reg.GetEntity("Заказ")); err != nil {
					t.Fatalf("создание заказа: %v", err)
				}
				путь = "/ui/document/" + url.PathEscape("Заказ") + "/" + заказ.String()
			}

			router := chi.NewRouter()
			s.Mount(router)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, путь, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("GET формы → %d: %.800s", response.Code, response.Body.String())
			}

			html := response.Body.String()
			начало := strings.Index(html, `id="ref-Клиент"`)
			if начало < 0 {
				t.Fatalf("в разметке нет <select id=\"ref-Клиент\">:\n%.1200s", html)
			}
			конец := strings.Index(html[начало:], "</select>")
			if конец < 0 {
				t.Fatalf("не найден конец <select>")
			}
			селект := html[начало : начало+конец]
			опций := strings.Count(селект, "<option")
			if опций != tc.wantOptions {
				t.Fatalf("опций в списке = %d, ожидалось %d:\n%s", опций, tc.wantOptions, селект)
			}
			// При выключенном списке поле не должно раскрываться вовсе:
			// выбор идёт только кнопкой подбора.
			неРаскрывается := strings.Contains(селект, `data-ob-no-dropdown="1"`) &&
				strings.Contains(селект, "pointer-events:none") &&
				strings.Contains(селект, "appearance:none")
			if неРаскрывается == (tc.elSetting == "") {
				t.Fatalf("список %s раскрывается, ожидалось обратное:\n%s",
					map[bool]string{true: "не", false: ""}[неРаскрывается], селект)
			}
			// Кнопка подбора обязана остаться: без неё поле стало бы невыбираемым.
			if !strings.Contains(html, `data-ob-ref-picker="ref-Клиент"`) {
				t.Fatalf("пропала кнопка подбора у поля:\n%.1200s", html)
			}
		})
	}
}
