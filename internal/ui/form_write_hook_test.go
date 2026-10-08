package ui

// Направление 3 (Фаза B): серверные события записи формы
// ПередЗаписью/ПриЗаписи/ПослеЗаписи. Раньше они объявлялись, но молча не
// вызывались в save-пути. Здесь проверяем: ПередЗаписью может отменить запись
// (исключение) и мутировать реквизиты так, что мутация доходит до Save
// (сведение регистра ключей), а ПослеЗаписи исполняется после записи.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// ПередЗаписью мутирует реквизит через Объект.Поле = … (Object.Set пишет ключ в
// нижнем регистре). Для существующего объекта реквизиты приходят в оригинальном
// регистре — без сведения у поля было бы два ключа и Save прочитал бы старое.
// runPreSaveFormHooks обязан свести их: победить должна мутация хука.
func TestRunPreSaveFormHooks_MutationReachesSave(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПередЗаписьюФормы()
	Объект.Наименование = "НОВОЕ";
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventBeforeWrite: "ПередЗаписьюФормы",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})

	// Существующий объект: ключи в оригинальном регистре (как из formToFields).
	obj := &runtime.Object{
		ID:            uuid.New(),
		Type:          ent.Name,
		Kind:          ent.Kind,
		Fields:        map[string]any{"Наименование": "СТАРОЕ"},
		TablePartRows: map[string][]map[string]any{},
	}

	var msgs []string
	if err := srv.runPreSaveFormHooks(context.Background(), ent, obj, &msgs); err != nil {
		t.Fatalf("runPreSaveFormHooks вернул ошибку: %v", err)
	}

	if got := obj.Fields["Наименование"]; got != "НОВОЕ" {
		t.Errorf("Fields[Наименование] = %v, ждали НОВОЕ (мутация хука не дошла)", got)
	}
	if _, dup := obj.Fields["наименование"]; dup {
		t.Errorf("остался дубль-ключ в нижнем регистре: %v", obj.Fields)
	}
	if len(obj.Fields) != 1 {
		t.Errorf("ожидался один ключ поля, получено %v", obj.Fields)
	}
}

// ПередЗаписью с ВызватьИсключение отменяет запись: runPreSaveFormHooks
// возвращает ошибку, а вызывающий код перерисует форму и не вызовет Save.
func TestRunPreSaveFormHooks_Abort(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПередЗаписьюФормы()
	ВызватьИсключение("Укажите заголовок");
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventBeforeWrite: "ПередЗаписьюФормы",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})

	obj := &runtime.Object{ID: uuid.New(), Type: ent.Name, Kind: ent.Kind,
		Fields: map[string]any{"Наименование": ""}, TablePartRows: map[string][]map[string]any{}}

	var msgs []string
	err := srv.runPreSaveFormHooks(context.Background(), ent, obj, &msgs)
	if err == nil {
		t.Fatal("ожидалась ошибка (ПередЗаписью бросил исключение), получили nil")
	}
	if !strings.Contains(err.Error(), "заголовок") {
		t.Errorf("ошибка = %q, ждали текст исключения", err.Error())
	}
}

// Если форма не объявляет события записи — runPreSaveFormHooks no-op: Объект не
// трогается (поведение save как раньше, без накладных расходов).
func TestRunPreSaveFormHooks_NoHookNoop(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil,
		[]*metadata.FormElement{
			{Kind: metadata.FormElementButton, Name: "КнопкаПусто"},
		})

	obj := &runtime.Object{ID: uuid.New(), Type: ent.Name, Kind: ent.Kind,
		Fields: map[string]any{"Наименование": "X"}, TablePartRows: map[string][]map[string]any{}}

	var msgs []string
	if err := srv.runPreSaveFormHooks(context.Background(), ent, obj, &msgs); err != nil {
		t.Fatalf("no-op ждали nil, получили %v", err)
	}
	if obj.Fields["Наименование"] != "X" || len(obj.Fields) != 1 {
		t.Errorf("Объект изменился без хуков: %v", obj.Fields)
	}
}

func TestRunPreSaveFormHooks_BlocksRecursiveSaveOfSameObject(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПередЗаписьюФормы()
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventBeforeWrite: "ПередЗаписьюФормы",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	obj := &runtime.Object{
		ID:            uuid.New(),
		Type:          ent.Name,
		Kind:          ent.Kind,
		Fields:        map[string]any{"Наименование": "recursive"},
		TablePartRows: map[string][]map[string]any{},
	}

	var messages []string
	err := srv.runPreSaveFormHooks(context.Background(), ent, obj, &messages)
	if err == nil || !strings.Contains(err.Error(), "внутри обработчика записи формы") {
		t.Fatalf("recursive Object.Write was not rejected explicitly: %v", err)
	}
}

// #915 moves BeforeWrite/OnWrite into entityservice.Save's transaction so the
// handlers see the assigned number and rejection rolls the counter back. That
// transaction must still obey #914's entity.save wall-clock limit: otherwise a
// sleeping form handler holds SQLite's only connection after the entity hook
// deadline work deliberately closed that gap for OnWrite/OnPost.
func TestRunPreSaveFormHooks_UsesEntitySaveDeadlineInsideTransaction(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПередЗаписьюФормы()
	Приостановить(2);
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventBeforeWrite: "ПередЗаписьюФормы",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	srv.cfg.Limits.RequestTimeoutSec = 1

	id := uuid.New()
	fields := map[string]any{"Наименование": "slow"}
	started := time.Now()
	_, err := srv.entityService().Save(context.Background(), entityservice.SaveRequest{
		Entity: ent,
		ID:     id,
		IsNew:  true,
		Fields: fields,
		Preflight: func(txCtx context.Context, obj *runtime.Object) error {
			var messages []string
			return srv.runPreSaveFormHooks(txCtx, ent, obj, &messages)
		},
	})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatalf("sleeping form write hook succeeded after %v", elapsed)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("form write hook outlived entity.save deadline: %v", elapsed)
	}
	if _, getErr := srv.store.GetByID(context.Background(), ent.Name, id, ent); !storage.IsNotFound(getErr) {
		t.Fatalf("row survived timed-out preflight: %v", getErr)
	}
}

// ПослеЗаписи исполняется после успешной записи с перезагруженным из БД
// Объектом (с актуальными значениями реквизитов).
func TestRunAfterWriteFormHook(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПослеЗаписиФормы()
	Сообщить("записано: " + Объект.Наименование);
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventAfterWrite: "ПослеЗаписиФормы",
	}, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})

	id := insertContragent(t, srv, ent, "КОНТРАГЕНТ-1")

	var msgs []string
	srv.runAfterWriteFormHook(context.Background(), ent, id, &msgs)

	if len(msgs) != 1 || !strings.Contains(msgs[0], "КОНТРАГЕНТ-1") {
		t.Errorf("messages = %v, ждали сообщение с наименованием записанного объекта", msgs)
	}
}

// #1826: use the mounted HTTP save routes so a populated reference in an
// isolated hook cannot hide a missing invocation in the user-facing save path.
func TestAfterWriteFormSelfRefThroughHTTPSave(t *testing.T) {
	for _, test := range []struct {
		kind    metadata.Kind
		field   string
		refType string
	}{
		{metadata.KindCatalog, "Наименование", "СправочникСсылка"},
		{metadata.KindDocument, "Номер", "ДокументСсылка"},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			form := managedObjectForm(fieldEl("Value", "Объект."+test.field))
			form.Handlers = map[metadata.FormEventType]string{
				metadata.FormEventAfterWrite: "ПослеЗаписиФормы",
			}
			form.ProgramAST = mustParse(t, fmt.Sprintf(`
Процедура ПослеЗаписиФормы()
	Сообщить("ru-type=" + Строка(ТипЗнч(Объект.Ссылка)));
	Сообщить("ru-id=" + Строка(Объект.Ссылка.УникальныйИдентификатор));
	Записанный = Объект.Ссылка.ПолучитьОбъект();
	Сообщить("ru-value=" + Записанный.%s);
	Сообщить("en-type=" + Строка(ТипЗнч(Объект.Reference)));
	Сообщить("en-id=" + Строка(Объект.Reference.УникальныйИдентификатор));
	Записанный = Объект.Reference.GetObject();
	Сообщить("en-value=" + Записанный.%s);
	Сообщить("current=" + Объект.%s);
КонецПроцедуры
`, test.field, test.field, test.field))
			entity := &metadata.Entity{
				Name: "ЗаписьСоСсылкой", Kind: test.kind,
				Fields: []metadata.Field{{Name: test.field, Type: metadata.FieldTypeString}},
				Forms:  []*metadata.FormModule{form},
			}
			if test.kind == metadata.KindDocument {
				entity.Numerator = &metadata.Numerator{Prefix: "Д-", Length: 6}
			}
			server, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
			router := chi.NewRouter()
			server.Mount(router)
			baseURL := "/ui/" + string(test.kind) + "/" + entity.Name + "/"
			var id uuid.UUID
			for _, operation := range []string{"create", "update"} {
				t.Run(operation, func(t *testing.T) {
					target, value := baseURL+"new", "создано"
					if operation == "update" {
						target, value = baseURL+id.String(), "изменено"
					} else if test.kind == metadata.KindDocument {
						value = "" // The hook must see the assigned document number.
					}
					server.messages.Clear("")
					request := httptest.NewRequest(http.MethodPost, target,
						strings.NewReader(url.Values{test.field: {value}}.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					if response.Code != http.StatusSeeOther {
						t.Fatalf("save status=%d, body=%s", response.Code, response.Body.String())
					}
					rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1 {
						t.Fatalf("expected one persisted row, got %#v", rows)
					}
					savedID := uuid.MustParse(fmt.Sprint(rows[0]["id"]))
					if operation == "update" && savedID != id {
						t.Fatalf("update changed identity: %s -> %s", id, savedID)
					}
					id = savedID
					savedValue := fmt.Sprint(rows[0][test.field])
					if savedValue == "" || (value != "" && savedValue != value) {
						t.Fatalf("persisted value=%q, submitted=%q", savedValue, value)
					}
					want := []string{
						"ru-type=" + test.refType + "." + entity.Name,
						"ru-id=" + id.String(), "ru-value=" + savedValue,
						"en-type=" + test.refType + "." + entity.Name,
						"en-id=" + id.String(), "en-value=" + savedValue,
						"current=" + savedValue,
					}
					messages := server.messages.List("")
					if len(messages) != len(want) {
						t.Fatalf("AfterWrite messages=%v, want %v", messages, want)
					}
					for i, message := range messages {
						if message.Text != want[i] {
							t.Errorf("AfterWrite message[%d]=%q, want %q", i, message.Text, want[i])
						}
					}
				})
			}
		})
	}
}
