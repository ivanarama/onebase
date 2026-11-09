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

// Both question continuations coexist in one loaded document form. Exercise
// the rendered configuration and mounted HTTP endpoint, including its event gate.
func TestFormQuestions_OnOpenAndBeforePostHTTP(t *testing.T) {
	for _, answerDeclared := range []bool{true, false} {
		t.Run(map[bool]string{true: "declared-answer", false: "undeclared-answer"}[answerDeclared], func(t *testing.T) {
			dir := t.TempDir()
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
  ПриОткрытии: Открыть
  ПередПроведением: ПроверитьПроведение
`
			if answerDeclared {
				yaml += "  Ответ: ОтветитьНаОткрытие\n"
			}
			module := `
Процедура Открыть()
	ПоказатьВопрос("Открыть?", "ДаНет");
КонецПроцедуры
Процедура ОтветитьНаОткрытие()
	Объект.Наименование = "открытие: " + ВопросОтвет;
КонецПроцедуры
Процедура ПроверитьПроведение(Отказ)
	Если ВопросОтвет = "" Тогда
		ПоказатьВопрос("Провести?", "ДаНет");
		Возврат;
	КонецЕсли;
	Объект.Наименование = "проведение: " + ВопросОтвет;
	Если ВопросОтвет <> "Да" Тогда
		Отказ = Истина;
	КонецЕсли;
КонецПроцедуры
`
			yamlPath := filepath.Join(dir, "object.form.yaml")
			for path, data := range map[string]string{yamlPath: yaml, filepath.Join(dir, "object.form.os"): module} {
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
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
			_, cfg, ok := strings.Cut(page.Body.String(), `<script type="application/json" id="ob-managed-config">`)
			if page.Code != http.StatusOK || !ok {
				t.Fatalf("GET form: %d: %s", page.Code, page.Body.String())
			}
			cfg, _, _ = strings.Cut(cfg, "</script>")
			var config struct {
				BeforePost bool `json:"beforePost"`
			}
			if err := json.Unmarshal([]byte(cfg), &config); err != nil || !config.BeforePost {
				t.Fatalf("posting preflight missing: %v: %s", err, cfg)
			}
			for _, tc := range []struct {
				event, answer, want string
				question, denied    bool
			}{
				{"ПриОткрытии", "", "", true, false},
				{"Ответ", "Нет", "открытие: Нет", false, !answerDeclared},
				{"ПередПроведением", "", "", true, false},
				{"ПередПроведением", "Да", "проведение: Да", false, false},
				{"ПоддельноеСобытие", "Да", "", false, true},
			} {
				body := url.Values{"_kind": {"object"}, "_element": {""}, "_event": {tc.event}, "_question_answer": {tc.answer}, "Наименование": {"ввод"}}
				req := httptest.NewRequest(http.MethodPost, "/ui/document/Проверка/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if rec.Code != http.StatusOK || resp.OK == tc.denied || resp.Cancel {
					t.Fatalf("%s/%s: HTTP %d: %s", tc.event, tc.answer, rec.Code, rec.Body.String())
				}
				if tc.denied {
					if resp.Error == "" || (tc.event == "Ответ" && !strings.Contains(resp.Error, "не объявлен")) {
						t.Fatalf("event gate: %s", rec.Body.String())
					}
					continue
				}
				if (resp.Question != nil) != tc.question || (tc.want != "" && resp.Values["Наименование"] != tc.want) {
					t.Fatalf("wrong continuation for %s/%s: %s", tc.event, tc.answer, rec.Body.String())
				}
			}
		})
	}
}
