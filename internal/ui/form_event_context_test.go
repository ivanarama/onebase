package ui

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Контекст обработчика управляемой формы: «Ссылка» самой записи и реквизиты
// формы голым именем. До этих проверок типовой обработчик вида
// `Модуль.Действие(Объект.Ссылка)` падал на «ПолучитьОбъект вызван у
// Неопределено», а `Если ПустаяСтрока(Телефон)` всегда видел пустоту.

// formCtxFixture — справочник «Обращение» с реквизитом-ссылкой на «Направление»
// в реквизитах формы (save:false) и модулем формы из параметра.
type formCtxFixture struct {
	srv    *Server
	entity *metadata.Entity
	docID  uuid.UUID
	naprID uuid.UUID
}

func setupFormCtxServer(t *testing.T, formOS string, attrs []*metadata.FormAttribute) formCtxFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	napr := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
	}
	ent := &metadata.Entity{
		Name: "Обращение", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Канал", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{napr, ent}); err != nil {
		t.Fatal(err)
	}
	naprID := uuid.New()
	if err := db.Upsert(ctx, napr.Name, naprID,
		map[string]any{"Наименование": "Ремонт", "Код": "REM"}, napr); err != nil {
		t.Fatal(err)
	}
	docID := uuid.New()
	if err := db.Upsert(ctx, ent.Name, docID,
		map[string]any{"Наименование": "ОБР-000002", "Канал": "Телефон"}, ent); err != nil {
		t.Fatal(err)
	}

	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: attrs,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementButton, Name: "КнопкаТест",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Тест"},
		}},
		ProgramAST: mustParse(t, formOS),
	}
	ent.Forms = []*metadata.FormModule{form}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{napr, ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc

	return formCtxFixture{
		srv: &Server{store: db, reg: reg, interp: interp,
			lockMgr: runtime.NewLockManager(), messages: NewMessageStore()},
		entity: ent, docID: docID, naprID: naprID,
	}
}

func (f formCtxFixture) fire(t *testing.T, body url.Values) formEventResponse {
	t.Helper()
	body.Set("_element", "КнопкаТест")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	rec := executeFormEvent(t, f.srv, f.entity, body)
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

// Объект.Ссылка в обработчике формы — рабочая ссылка на саму запись: её можно
// передать в общий модуль и получить объект, как в ПриЗаписи/ОбработкеПроведения.
func TestFormEvent_ОбъектСсылкаУказываетНаЗапись(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Об = Объект.Ссылка.ПолучитьОбъект();
	Сообщить("наим=" + Об.Наименование);
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "наим=ОБР-000002" {
		t.Fatalf("messages=%v, ожидалось [наим=ОБР-000002]", resp.Messages)
	}
	// Псевдо-реквизит — контекст обработчика, в значения формы он не едет.
	for _, k := range []string{"ссылка", "reference", "Ссылка"} {
		if _, exists := resp.Values[k]; exists {
			t.Errorf("ключ %q не должен попадать в values: %+v", k, resp.Values)
		}
	}
}

// У новой (ещё не записанной) записи ссылки нет — обработчик должен увидеть
// Неопределено, а не ссылку на несуществующий uuid.
func TestFormEvent_УНовойЗаписиСсылкиНет(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Если ЗначениеЗаполнено(Объект.Ссылка) Тогда
		Сообщить("ссылка есть");
	Иначе
		Сообщить("ссылки нет");
	КонецЕсли;
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("Наименование", "черновик")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "ссылки нет" {
		t.Fatalf("messages=%v, ожидалось [ссылки нет]", resp.Messages)
	}
}

// Реквизит формы читается голым именем — так его пишут в модуле управляемой
// формы. Ссылочный при этом остаётся ссылкой: доступны .Код/.Наименование.
func TestFormEvent_РеквизитФормыДоступенГолымИменем(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Если НЕ ЗначениеЗаполнено(НаправлениеВыбор) Тогда
		ВызватьИсключение("направление не выбрано");
	КонецЕсли;
	Сообщить("код=" + НаправлениеВыбор.Код + " тел=" + Телефон);
КонецПроцедуры
`, []*metadata.FormAttribute{
		{Name: "НаправлениеВыбор", TypeRef: "CatalogRef.Направление"},
		{Name: "Телефон", TypeRef: "Строка"},
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("НаправлениеВыбор", f.naprID.String())
	body.Set("Телефон", "89001112233")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "код=REM тел=89001112233" {
		t.Fatalf("messages=%v, ожидалось [код=REM тел=89001112233]", resp.Messages)
	}
}

// Тот же реквизит по-прежнему виден и как Объект.<Реквизит> — путь, на котором
// написаны существующие конфигурации, ломать нельзя.
func TestFormEvent_РеквизитФормыДоступенЧерезОбъект(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Сообщить("код=" + Объект.НаправлениеВыбор.Код);
КонецПроцедуры
`, []*metadata.FormAttribute{
		{Name: "НаправлениеВыбор", TypeRef: "CatalogRef.Направление"},
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("НаправлениеВыбор", f.naprID.String())

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "код=REM" {
		t.Fatalf("messages=%v, ожидалось [код=REM]", resp.Messages)
	}
}

// Имя реквизита формы не должно перебивать встроенный объект доступа: иначе
// «Справочники» на форме убили бы весь DSL внутри процедуры.
func TestFormEvent_РеквизитНеЗатираетВстроенныеИмена(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Реф = Справочники.Направление.НайтиПоРеквизиту("Код", "REM");
	Если ЗначениеЗаполнено(Реф) Тогда
		Сообщить("найдено");
	КонецЕсли;
КонецПроцедуры
`, []*metadata.FormAttribute{
		{Name: "Справочники", TypeRef: "Строка"},
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Справочники", "мусор")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "найдено" {
		t.Fatalf("messages=%v, ожидалось [найдено]", resp.Messages)
	}
}

// Основной реквизит (main:true) — это сам объект; голым именем он не публикуется,
// иначе «Объект» подменился бы значением поля и обработчик потерял бы запись.
func TestFormEvent_ОсновнойРеквизитНеПодменяетОбъект(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Сообщить("канал=" + Объект.Канал);
КонецПроцедуры
`, []*metadata.FormAttribute{
		{Name: "Объект", TypeRef: "CatalogRef.Обращение", MainAttribute: true},
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Канал", "Телефон")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "канал=Телефон" {
		t.Fatalf("messages=%v, ожидалось [канал=Телефон]", resp.Messages)
	}
}
