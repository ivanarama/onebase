package ui

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ссылка, присвоенная обработчиком формы, обязана приехать клиенту вместе с
// опцией для <select> (issue #615).
//
// Клиентский applyValues делает inp.value = val; для <select> без такого
// <option> браузер молча ставит selectedIndex = -1, поле пустеет, и следующая
// запись затирает ссылку в базе. Список же строится первой страницей пикера
// (refPickerDefaultLimit = 50), поэтому в справочнике покрупнее промах — норма.
//
// Фикстура: 60 складов, обработчик подставляет шестидесятый — он гарантированно
// вне первой страницы.

const refOptionsTargetName = "Склад-060"

func setupRefOptionEventServer(t *testing.T) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	warehouse := &metadata.Entity{
		Name:   "Склад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	order := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: "reference:Склад", RefEntity: "Склад"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{warehouse, order}); err != nil {
		t.Fatal(err)
	}

	var target uuid.UUID
	for i := 1; i <= 60; i++ {
		id := uuid.New()
		name := fmt.Sprintf("Склад-%03d", i)
		if err := db.Upsert(ctx, warehouse.Name, id, map[string]any{"Наименование": name}, warehouse); err != nil {
			t.Fatal(err)
		}
		if name == refOptionsTargetName {
			target = id
		}
	}
	if target == uuid.Nil {
		t.Fatal("целевой склад не создан")
	}

	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
		Elements: []*metadata.FormElement{{
			Kind:     metadata.FormElementField,
			Name:     "ПолеНаименование",
			DataPath: "Объект.Наименование",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnChange: "ПодставитьСклад",
			},
		}},
		ProgramAST: mustParse(t, `
Процедура ПодставитьСклад()
	Объект.Склад = Справочники.Склад.НайтиПоНаименованию("`+refOptionsTargetName+`");
КонецПроцедуры
`),
	}
	order.Forms = []*metadata.FormModule{form}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{warehouse, order}})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)
	return s, order, target
}

func TestFormEvent_ПрисвоеннаяОбработчикомСсылкаЕдетСоСвоейОпцией(t *testing.T) {
	s, order, target := setupRefOptionEventServer(t)

	body := url.Values{}
	body.Set("_element", "ПолеНаименование")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("Наименование", "Заказ 1")

	rec := executeFormEvent(t, s, order, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}

	// Сначала убеждаемся, что обработчик вообще отработал: иначе тест был бы
	// зелёным и на сломанном коде — проверял бы отсутствие опции у отсутствующей
	// ссылки.
	got := refValueString(resp.Values["Склад"])
	if got != target.String() {
		t.Fatalf("обработчик не подставил склад: values[Склад]=%v, ждали %s", resp.Values["Склад"], target)
	}

	rows := resp.RefOptions["Склад"]
	if len(rows) == 0 {
		t.Fatalf("refOptions[Склад] пуст: <select> не получит <option> для %s, и присвоенная ссылка потеряется", target)
	}
	if id := refValueString(rows[0]["id"]); id != target.String() {
		t.Fatalf("refOptions[Склад][0].id=%q, ждали %s", id, target)
	}
	if label, _ := rows[0]["_label"].(string); label != refOptionsTargetName {
		t.Errorf("подпись опции = %q, ждали %q", label, refOptionsTargetName)
	}
}

// Значение, попавшее в первую страницу пикера, опции в ответе не требует: у
// <select> она уже есть. Проверяем, что лишних чтений и лишнего JSON нет.
func TestFormEvent_ЗначениеБезСсылкиОпцийНеПорождает(t *testing.T) {
	s, order, _ := setupRefOptionEventServer(t)

	body := url.Values{}
	body.Set("_element", "ПолеНаименование")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("Наименование", "Заказ 1")

	rec := executeFormEvent(t, s, order, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if _, ok := resp.RefOptions["Наименование"]; ok {
		t.Errorf("для строкового реквизита опций быть не должно: %v", resp.RefOptions)
	}
}
