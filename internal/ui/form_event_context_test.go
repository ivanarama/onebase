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
			{Name: "Активно", Type: metadata.FieldTypeBool},
		},
		// ТЧ с колонкой, одноимённой полю шапки, — на ней проверяется, что
		// readonly-колонка ТЧ не делает поле шапки «всегда перечитываемым».
		TableParts: []metadata.TablePart{{
			Name:   "Строки",
			Fields: []metadata.Field{{Name: "Канал", Type: metadata.FieldTypeString}},
		}},
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
		map[string]any{"Наименование": "ОБР-000002", "Канал": "Телефон", "Активно": true}, ent); err != nil {
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

	srv := &Server{store: db, reg: reg, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	srv.entitySvc = srv.newEntityService(nil)
	return formCtxFixture{srv: srv, entity: ent, docID: docID, naprID: naprID}
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

// Объект.Записать() в обработчике формы создаёт ещё не сохранённую запись —
// иначе команда на экране «Создать» упиралась в пустую Ссылка и требовала от
// оператора сначала нажать «Записать». Клиент получает id записи в savedId,
// чтобы следующее действие не создало второй документ.
func TestFormEvent_ОбъектЗаписатьСоздаётНовуюЗапись(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Если НЕ ЗначениеЗаполнено(Объект.Ссылка) Тогда
		Объект.Записать();
	КонецЕсли;
	Об = Объект.Ссылка.ПолучитьОбъект();
	Сообщить("записано: " + Об.Наименование);
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("Наименование", "ОБР-НОВОЕ") // _id не передан — форма «Создать»

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || resp.Messages[0] != "записано: ОБР-НОВОЕ" {
		t.Fatalf("messages=%v, ожидалось [записано: ОБР-НОВОЕ]", resp.Messages)
	}
	if resp.SavedID == "" {
		t.Fatal("savedId пуст: клиент не узнает id и следующее действие создаст дубль")
	}
	// Запись реально появилась в базе — под тем же id, что уехал клиенту.
	id, err := uuid.Parse(resp.SavedID)
	if err != nil {
		t.Fatalf("savedId не uuid: %q", resp.SavedID)
	}
	row, err := f.srv.store.GetByID(context.Background(), f.entity.Name, id, f.entity)
	if err != nil || row == nil {
		t.Fatalf("записи нет в базе: row=%v err=%v", row, err)
	}
	if row["Наименование"] != "ОБР-НОВОЕ" {
		t.Errorf("Наименование = %v, ожидалось ОБР-НОВОЕ", row["Наименование"])
	}
}

// У существующей записи savedId не возвращается: клиенту нечего менять, а
// непустое значение сбило бы адрес открытой формы.
func TestFormEvent_SavedIDТолькоДляНовойЗаписи(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Объект.Записать();
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.SavedID != "" {
		t.Errorf("savedId=%q для существующей записи — должен быть пуст", resp.SavedID)
	}
}

// Поле, которое обработчик изменил ЧЕРЕЗ БАЗУ (типовой путь: команда зовёт общий
// модуль, модуль читает объект, меняет и записывает), должно вернуться в форму
// актуальным. Раньше нередактируемое поле-результат не приходило из POST и
// восстанавливалось из базы ДО обработчика — форма показывала результат
// ПРЕДЫДУЩЕГО действия, отставая на шаг.
func TestFormEvent_ПолеИзменённоеЧерезБазуВозвращаетсяАктуальным(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Об = Объект.Ссылка.ПолучитьОбъект();
	Об.Канал = "Изменён модулем";
	Об.Записать();
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")
	// «Канал» не отправлен — как нередактируемое (disabled) поле на форме.

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if got := resp.Values["Канал"]; got != "Изменён модулем" {
		t.Fatalf("Канал=%v, ожидалось «Изменён модулем» (поле отстаёт на шаг)", got)
	}
}

// Значение, присвоенное САМИМ обработчиком, перечитыванием из базы не затирается:
// оно может быть ещё не записано и обязано доехать до формы как есть.
func TestFormEvent_ПрисвоенноеОбработчикомНеЗатирается(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Объект.Канал = "Черновик, не записан";
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if got := resp.Values["Канал"]; got != "Черновик, не записан" {
		t.Fatalf("Канал=%v, присвоение обработчика затёрто значением из базы", got)
	}
}

// Нередактируемое поле приходит в POST, если отрисовано как <input readonly>
// (в отличие от disabled-списка), но пользователь его не вводит. Его всё равно
// надо перечитывать: номер, присвоенный нумератором при записи, иначе появлялся
// на форме только после закрытия и повторного открытия.
func TestFormEvent_ReadonlyПолеОбновляетсяДажеПридяИзФормы(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Об = Объект.Ссылка.ПолучитьОбъект();
	Об.Канал = "Присвоено при записи";
	Об.Записать();
КонецПроцедуры
`, nil)
	// «Канал» на форме — нередактируемое поле.
	form := f.entity.Forms[0]
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеКанал",
		DataPath: "Объект.Канал", ReadOnly: true,
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")
	body.Set("Канал", "Телефон") // readonly-инпут отправляет старое значение

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if got := resp.Values["Канал"]; got != "Присвоено при записи" {
		t.Fatalf("Канал=%v, ожидалось «Присвоено при записи» (readonly не обновилось)", got)
	}
}

// Снятый пользователем флажок обязан остаться снятым. Для РЕДАКТИРУЕМОГО
// элемента «Флажок» отсутствие ключа в POST означает «снято», а не «не
// передавалось» (контракт частичной записи), поэтому перечитывать такое поле
// из базы нельзя: иначе любое событие формы — нажатие команды, ПриИзменении
// соседа — возвращало бы галку взведённой, applyValues ставил бы её обратно
// в DOM, а следующая запись писала бы в базу старое «истина».
func TestFormEvent_СнятыйФлажокОстаётсяСнятым(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Сообщить("ок");
КонецПроцедуры
`, nil)
	form := f.entity.Forms[0]
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementCheckbox, Name: "ПолеАктивно",
		DataPath: "Объект.Активно",
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")
	// «Активно» не отправлено — пользователь снял галку. В базе там истина.

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if got := resp.Values["Активно"]; got == true || got == "true" {
		t.Fatalf("Активно=%#v — снятая галка вернулась взведённой", got)
	}
}

// ReadOnly-колонка табличной части не должна делать одноимённое поле шапки
// «всегда перечитываемым»: имена сегментов data_path совпадают, но к полю шапки
// путь ТЧ отношения не имеет. Иначе введённое пользователем значение шапки
// молча откатывается к тому, что лежит в базе.
func TestFormEvent_ReadonlyКолонкаТЧНеЗатираетПолеШапки(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Сообщить("ок");
КонецПроцедуры
`, nil)
	form := f.entity.Forms[0]
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "КолонкаКанал",
		DataPath: "Объект.Строки.Канал", ReadOnly: true,
	})

	body := url.Values{}
	body.Set("_id", f.docID.String())
	body.Set("Наименование", "ОБР-000002")
	body.Set("Канал", "Почта") // в базе «Телефон», пользователь ввёл «Почта»

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if got := resp.Values["Канал"]; got != "Почта" {
		t.Fatalf("Канал=%v, введённое пользователем значение шапки затёрто базой", got)
	}
}

// Перечитывание не должно отключаться для формы «Создать», на которой обработчик
// сам записал объект: номер от нумератора и прочее, присвоенное при записи,
// обязаны приехать на экран сразу, без переоткрытия формы. Гейт «есть ли запись
// в базе» смотрит и на сырой _id, и на факт записи внутри обработчика.
func TestFormEvent_НаСозданииПолеПослеЗаписиПриходитСразу(t *testing.T) {
	f := setupFormCtxServer(t, `
Процедура Тест()
	Объект.Записать();
	Об = Объект.Ссылка.ПолучитьОбъект();
	Об.Канал = "Присвоено при записи";
	Об.Записать();
КонецПроцедуры
`, nil)

	body := url.Values{}
	body.Set("Наименование", "ОБР-НОВОЕ") // _id не передан — форма «Создать»

	resp := f.fire(t, body)
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if resp.SavedID == "" {
		t.Fatal("savedId пуст: обработчик записал новую запись, id обязан уехать клиенту")
	}
	if got := resp.Values["Канал"]; got != "Присвоено при записи" {
		t.Fatalf("Канал=%v, ожидалось «Присвоено при записи» (поле после записи не приехало)", got)
	}
}
