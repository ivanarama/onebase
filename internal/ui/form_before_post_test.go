package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ПередПроведением — событие уровня формы, которое браузер шлёт перед
// стандартными «Провести» и «Провести и закрыть». Контракт Отказ — как у
// ПередЗакрытием; вопрос возвращается тем же событием с ВопросОтвет.

const beforePostModule = `
Процедура ПередПроведениемФормы(Отказ)
	Если ВопросОтвет = "" Тогда
		Если Объект.Наименование = "спросить" Тогда
			ПоказатьВопрос("Есть свободный остаток. Выдать?", "ДаНетОтмена", "Внимание");
			Возврат;
		КонецЕсли;
		Если Объект.Наименование = "нельзя" Тогда
			Сообщить("Проведение отменено");
			Отказ = Истина;
		КонецЕсли;
		Возврат;
	КонецЕсли;
	Объект.Наименование = "ответ: " + ВопросОтвет;
	Если ВопросОтвет = "Отмена" Тогда
		Отказ = Истина;
	КонецЕсли;
КонецПроцедуры
`

func beforePostServer(t *testing.T, posting bool) (*Server, *metadata.Entity) {
	t.Helper()
	srv, ent := setupManagedEventsServer(t, beforePostModule,
		map[metadata.FormEventType]string{metadata.FormEventBeforePost: "ПередПроведениемФормы"}, nil)
	ent.Posting = posting
	return srv, ent
}

func beforePostBody(name, answer string) url.Values {
	body := url.Values{}
	body.Set("_element", "")
	body.Set("_event", string(metadata.FormEventBeforePost))
	body.Set("Наименование", name)
	if answer != "" {
		body.Set("_question_answer", answer)
	}
	return body
}

func TestBeforePost_БезОтказаПроведениеРазрешено(t *testing.T) {
	srv, ent := beforePostServer(t, true)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("можно", "")).Body.Bytes())
	if !resp.OK || resp.Cancel || resp.Question != nil || resp.Error != "" {
		t.Fatalf("без отказа и вопроса проведение должно быть разрешено: %+v", resp)
	}
}

func TestBeforePost_ОтказОтменяетПроведение(t *testing.T) {
	srv, ent := beforePostServer(t, true)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("нельзя", "")).Body.Bytes())
	if !resp.OK || !resp.Cancel {
		t.Fatalf("Отказ = Истина должен вернуть cancel: %+v", resp)
	}
	if !strings.Contains(strings.Join(resp.Messages, "\n"), "Проведение отменено") {
		t.Fatalf("сообщение обработчика потеряно: %v", resp.Messages)
	}
}

func TestBeforePost_ВопросИОтветТемЖеСобытием(t *testing.T) {
	srv, ent := beforePostServer(t, true)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("спросить", "")).Body.Bytes())
	if !resp.OK || resp.Cancel || resp.Question == nil {
		t.Fatalf("фаза 1 должна вернуть вопрос без отказа: %+v", resp)
	}
	if strings.Join(resp.Question.Variants, "|") != "Да|Нет|Отмена" || resp.Question.Title != "Внимание" {
		t.Fatalf("вопрос: %+v", resp.Question)
	}

	resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("спросить", "Да")).Body.Bytes())
	if !resp.OK || resp.Cancel || resp.Question != nil {
		t.Fatalf("ответ «Да» — проведение разрешено: %+v", resp)
	}
	if got := resp.Values["Наименование"]; got != "ответ: Да" {
		t.Fatalf("изменения обработчика должны вернуться в форму перед проведением: %v", got)
	}

	resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("спросить", "Отмена")).Body.Bytes())
	if !resp.OK || !resp.Cancel {
		t.Fatalf("ответ «Отмена» с Отказ — проведение отменено: %+v", resp)
	}
}

func TestBeforePost_НепроводимыйОбъектОтклоняется(t *testing.T) {
	srv, ent := beforePostServer(t, false)
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("можно", "")).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "не проводится") {
		t.Fatalf("у непроводимого объекта событие отклоняется: %+v", resp)
	}
}

func TestBeforePost_ПараметрТолькоОтказ(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПередПроведениемФормы(Что)
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforePost: "ПередПроведениемФормы"}, nil)
	ent.Posting = true
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, beforePostBody("можно", "")).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "Отказ") {
		t.Fatalf("параметр не Отказ — ошибка конфигурации: %+v", resp)
	}
}

// Событие шлёт сам браузер, с элемента его не прислать: на элементе такого
// события нет, и маршрутизатор отвечает отказом.
func TestBeforePost_ТолькоУровеньФормы(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, beforePostModule, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementButton, Name: "Команда",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforePost: "ПередПроведениемФормы"}},
	})
	ent.Posting = true
	body := beforePostBody("нельзя", "")
	body.Set("_element", "Команда")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.OK || resp.Cancel {
		t.Fatalf("элемент не отправляет ПередПроведением: %+v", resp)
	}
}

// Браузер узнаёт о ПередПроведением из конфигурации формы: признак есть только
// у проводимого объекта с объявленным обработчиком, иначе «Провести» работает
// по-старому, без лишнего запроса.
func TestBeforePost_ПризнакВКонфигурацииФормы(t *testing.T) {
	for _, tc := range []struct {
		name     string
		posting  bool
		handlers map[metadata.FormEventType]string
		want     bool
	}{
		{"проводимый с обработчиком", true, map[metadata.FormEventType]string{metadata.FormEventBeforePost: "ПередПроведениемФормы"}, true},
		{"проводимый без обработчика", true, nil, false},
		{"непроводимый с обработчиком", false, map[metadata.FormEventType]string{metadata.FormEventBeforePost: "ПередПроведениемФормы"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ent := setupManagedEventsServer(t, beforePostModule, tc.handlers, nil)
			ent.Posting = tc.posting
			page := httptest.NewRecorder()
			srv.form(page, reqWithChi("GET", "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name, "kind": "catalog"}))
			if page.Code != 200 {
				t.Fatalf("GET form: %d: %s", page.Code, page.Body.String())
			}
			_, rest, ok := strings.Cut(page.Body.String(), `<script type="application/json" id="ob-managed-config">`)
			if !ok {
				t.Fatal("нет конфигурации формы")
			}
			configJSON, _, _ := strings.Cut(rest, "</script>")
			var config map[string]any
			if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
				t.Fatal(err)
			}
			if got, _ := config["beforePost"].(bool); got != tc.want {
				t.Fatalf("beforePost=%v, want %v", config["beforePost"], tc.want)
			}
		})
	}
}

// Use the real loader and mounted HTTP routes: an unavailable declared handler
// must never turn the browser's posting preflight into successful permission.
func TestBeforePost_UnavailableDeclaredHandlerHTTP(t *testing.T) {
	for _, module := range []struct{ name, source string }{
		{"missing-module", ""},
		{"missing-procedure", "Процедура Другая()\nКонецПроцедуры"},
	} {
		t.Run(module.name, func(t *testing.T) {
			dir := t.TempDir()
			yamlPath := filepath.Join(dir, "object.form.yaml")
			yaml := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Проверка
  layout: managed
elements:
  - kind: ПолеВвода
    name: Наименование
    data_path: Объект.Наименование
events:
  ПередПроведением: ПроверитьПроведение
  ПриОткрытии: ПроверитьОткрытие
`
			if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if module.source != "" {
				if err := os.WriteFile(filepath.Join(dir, "object.form.os"), []byte(module.source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			form, err := loader.NewManagedFormLoader().LoadFormFile(yamlPath, "Проверка")
			if err != nil {
				t.Fatal(err)
			}
			entity := &metadata.Entity{Name: "Проверка", Kind: metadata.KindDocument, Posting: true,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{form}}
			db, err := storage.ConnectSQLite(context.Background(), filepath.Join(dir, "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := db.Migrate(context.Background(), []*metadata.Entity{entity}); err != nil {
				t.Fatal(err)
			}
			reg := runtime.NewRegistry()
			reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
			srv := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
			srv.entitySvc = srv.newEntityService(nil)
			router := chi.NewRouter()
			srv.Mount(router)
			page := httptest.NewRecorder()
			router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/ui/document/Проверка/new", nil))
			if page.Code != http.StatusOK {
				t.Fatalf("GET form: %d: %s", page.Code, page.Body.String())
			}
			_, config, ok := strings.Cut(page.Body.String(), `<script type="application/json" id="ob-managed-config">`)
			if !ok {
				t.Fatal("managed config missing")
			}
			config, _, _ = strings.Cut(config, "</script>")
			var cfg struct {
				BeforePost bool `json:"beforePost"`
			}
			if err := json.Unmarshal([]byte(config), &cfg); err != nil {
				t.Fatal(err)
			}
			if !cfg.BeforePost {
				t.Fatal("declared posting preflight was disabled")
			}

			for _, event := range []metadata.FormEventType{metadata.FormEventBeforePost, metadata.FormEventOnOpen} {
				body := beforePostBody("тест", "")
				body.Set("_kind", "object")
				body.Set("_event", string(event))
				req := httptest.NewRequest(http.MethodPost, "/ui/document/Проверка/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("event HTTP %d: %s", rec.Code, rec.Body.String())
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if event == metadata.FormEventBeforePost {
					if resp.OK || !strings.Contains(resp.Error, "ПроверитьПроведение") || !strings.Contains(resp.Error, ".form.os") {
						t.Fatalf("unavailable declared handler must deny posting: %s", rec.Body.String())
					}
				} else if !resp.OK || resp.Error != "" {
					t.Fatalf("ordinary event no-op changed: %s", rec.Body.String())
				}
			}
		})
	}
}
