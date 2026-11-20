package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Rendered configuration, real modal/controller JavaScript and mounted HTTP
// routes must agree on dialog transport names and the continuation event.
func TestFormQuestionFields_BeforePostHTTPMatrix(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the question HTTP matrix")
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		form := managedObjectForm()
		form.Handlers = map[metadata.FormEventType]string{
			metadata.FormEventOnOpen: "Открыть", metadata.FormEventOnAnswer: "Ответить",
			metadata.FormEventBeforePost: "Проверить",
		}
		for _, name := range []string{"Наименование", "Режим", "_question_fields"} {
			form.Elements = append(form.Elements, fieldEl(name, "Объект."+name))
		}
		form.ProgramAST = mustParse(t, `
Процедура Открыть()
	Спросить();
КонецПроцедуры
Процедура Спросить()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("имя, правка", "Кому", Истина));
	Поля.Добавить(Новый Структура("имя, правка, многострочное", "Комментарий", Истина, Истина));
	ПоказатьВопрос("Продолжить?", "ДаНет", "Проверка", Поля);
КонецПроцедуры
Процедура Ответить()
	Сообщить("Ответ=" + ВопросОтвет + ";" + ДиалогПоля.Кому + ";" + ДиалогПоля.Комментарий);
	Сообщить("Реквизит=" + Объект._question_fields);
КонецПроцедуры
Процедура Проверить(Отказ)
	Если ВопросОтвет = "" Тогда
		Спросить();
		Возврат;
	КонецЕсли;
	Сообщить("ПередПроведением=" + ВопросОтвет + ";" + ДиалогПоля.Кому + ";" + ДиалогПоля.Комментарий);
	Сообщить("Реквизит=" + Объект._question_fields);
	Если Объект.Режим = "cancel" Тогда
		Отказ = Истина;
	КонецЕсли;
	Если Объект.Режим = "error" Тогда
		ВызватьИсключение("Проведение запрещено");
	КонецЕсли;
	Если Объект.Режим = "again" Тогда
		Спросить();
	КонецЕсли;
КонецПроцедуры
`)
		entity := &metadata.Entity{Name: "ДиалогПередПроведением", Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Режим", Type: metadata.FieldTypeString}, {Name: "_question_fields", Type: metadata.FieldTypeString}},
			Forms: []*metadata.FormModule{form}}
		if err := db.Migrate(context.Background(), []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
		srv := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
		srv.entitySvc = srv.newEntityService(nil)
		router := chi.NewRouter()
		srv.Mount(router)
		httpServer := httptest.NewServer(router)
		t.Cleanup(httpServer.Close)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "--test", "--test-name-pattern=question fields mounted HTTP", "static/managed_close_intent_behavior_test.js")
		cmd.Env = append(os.Environ(), "ONEBASE_QUESTION_HTTP_URL="+httpServer.URL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("question HTTP JavaScript (%s): %v\n%s", db.Dialect().Name(), err, output)
		}
		t.Logf("real modal/controller and mounted HTTP (%s): %s", db.Dialect().Name(), output)
	})
}
