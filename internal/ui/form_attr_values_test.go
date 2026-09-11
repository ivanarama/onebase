package ui

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/ivantit66/onebase/internal/runtime"
)

// attrEventServer — сервер с управляемой формой, у которой есть ссылочный и
// строковый реквизиты формы (save:false), плюс справочник для ссылки.
func attrEventServer(t *testing.T, formOS string) (*Server, *metadata.Entity, uuid.UUID) {
	t.Helper()
	s, ent := setupManagedEventsServer(t, formOS, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
		{Kind: metadata.FormElementField, Name: "ПолеСклад", DataPath: "Склад"},
		{Kind: metadata.FormElementField, Name: "ПолеПримечание", DataPath: "Примечание"},
	})
	form := ent.Forms[0]
	form.Attributes = []*metadata.FormAttribute{
		{Name: "Склад", TypeRef: "CatalogRef.Склад", Save: false},
		{Name: "Примечание", TypeRef: "string(100)", Save: false},
	}

	// Справочник Склад: заводим сущность, мигрируем и кладём запись.
	ctx := context.Background()
	sklad := &metadata.Entity{
		Name: "Склад", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	db := s.store
	if err := db.Migrate(ctx, []*metadata.Entity{sklad}); err != nil {
		t.Fatal(err)
	}
	skladID := uuid.New()
	if err := db.Upsert(ctx, sklad.Name, skladID,
		map[string]any{"Наименование": "Основной"}, sklad); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent, sklad}})
	s.reg = reg
	s.interp.LookupProc = reg.GetModuleProc
	return s, ent, skladID
}

// Обработчик видит значение ссылочного реквизита формы и может обратиться к его
// реквизитам (Склад.Наименование) — до правки Объект.Склад давал nil.
func TestFormEvent_RefAttrVisibleToHandler(t *testing.T) {
	s, ent, skladID := attrEventServer(t, `
Процедура ПолеСкладПриИзменении()
	Если Объект.Склад = Неопределено Тогда
		Сообщить("СКЛАД:ПУСТО");
	Иначе
		Сообщить("СКЛАД:" + Объект.Склад.Наименование);
	КонецЕсли;
КонецПроцедуры
`)
	ent.Forms[0].Elements[1].Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "ПолеСкладПриИзменении",
	}

	rec := executeFormEvent(t, s, ent, url.Values{
		"_element":     {"ПолеСклад"},
		"_event":       {"ПриИзменении"},
		"Наименование": {"Тест"},
		"Склад":        {skladID.String()},
	})
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	joined := ""
	for _, m := range resp.Messages {
		joined += m + "|"
	}
	if joined == "" {
		t.Fatalf("обработчик ничего не сообщил: %+v", resp)
	}
	if joined == "СКЛАД:ПУСТО|" {
		t.Fatalf("значение ссылочного реквизита формы не дошло до обработчика: %s", joined)
	}
	if joined != "СКЛАД:Основной|" {
		t.Fatalf("ожидалось «СКЛАД:Основной», получено %q", joined)
	}
}

// Строковый реквизит формы тоже виден обработчику и возвращается в values
// в ОРИГИНАЛЬНОМ регистре — иначе applyValues на клиенте его не применит.
func TestFormEvent_StringAttrRoundTrip(t *testing.T) {
	s, ent, _ := attrEventServer(t, `
Процедура ПолеПримечаниеПриИзменении()
	Сообщить("ПРИМ:" + Объект.Примечание);
	Объект.Примечание = Объект.Примечание + "!";
КонецПроцедуры
`)
	ent.Forms[0].Elements[2].Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "ПолеПримечаниеПриИзменении",
	}

	rec := executeFormEvent(t, s, ent, url.Values{
		"_element":     {"ПолеПримечание"},
		"_event":       {"ПриИзменении"},
		"Наименование": {"Тест"},
		"Примечание":   {"привет"},
	})
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if len(resp.Messages) == 0 || resp.Messages[0] != "ПРИМ:привет" {
		t.Fatalf("реквизит формы не дошёл до обработчика: %+v", resp.Messages)
	}
	got, ok := resp.Values["Примечание"]
	if !ok {
		t.Fatalf("реквизит формы не вернулся в values (ключи: %v)", keysOfAny(resp.Values))
	}
	if got != "привет!" {
		t.Fatalf("мутация обработчика не вернулась: %v", got)
	}
}

// Канонический decimal(15) из импорта 1С и дата с runtime-префиксом проходят
// через настоящий form-event и видны DSL типизированными, а не строками.
func TestFormEvent_ImportedDecimalAndDatePrefixKeepRuntimeTypes(t *testing.T) {
	s, ent, _ := attrEventServer(t, `
Процедура ПолеТипыПриИзменении()
	Сообщить(ТипЗнч(Объект.Сумма) + "/" + ТипЗнч(Объект.Период));
КонецПроцедуры
`)
	importedType := onec_forms.Type1CToOneBase("xs:decimal", 15, 0, "")
	if importedType != "decimal(15)" {
		t.Fatalf("канонический импорт xs:decimal = %q, ожидался decimal(15)", importedType)
	}
	form := ent.Forms[0]
	form.Attributes = append(form.Attributes,
		&metadata.FormAttribute{Name: "Сумма", TypeRef: importedType, Save: false},
		&metadata.FormAttribute{Name: "Период", TypeRef: "dateSuffix", Save: false},
	)
	form.Elements = append(form.Elements, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеТипы", DataPath: "Сумма",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПолеТипыПриИзменении"},
	})

	rec := executeFormEvent(t, s, ent, url.Values{
		"_element":     {"ПолеТипы"},
		"_event":       {"ПриИзменении"},
		"Наименование": {"Тест"},
		"Сумма":        {"12,50"},
		"Период":       {"2026-09-09"},
	})
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if len(resp.Messages) != 1 || resp.Messages[0] != "Число/Дата" {
		t.Fatalf("runtime-типы реквизитов формы = %v, ожидались [Число/Дата]", resp.Messages)
	}
}

// Реквизит формы, одноимённый полю сущности, не должен подменять поле сущности.
func TestFormEvent_AttrNameCollisionKeepsEntityField(t *testing.T) {
	s, ent, _ := attrEventServer(t, `
Процедура ПолеНаимПриИзменении()
	Сообщить("НАИМ:" + Объект.Наименование);
КонецПроцедуры
`)
	ent.Forms[0].Attributes = append(ent.Forms[0].Attributes,
		&metadata.FormAttribute{Name: "Наименование", TypeRef: "CatalogRef.Склад", Save: false})
	ent.Forms[0].Elements[0].Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnChange: "ПолеНаимПриИзменении",
	}

	rec := executeFormEvent(t, s, ent, url.Values{
		"_element":     {"ПолеНаим"},
		"_event":       {"ПриИзменении"},
		"Наименование": {"Ромашка"},
	})
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if len(resp.Messages) == 0 || resp.Messages[0] != "НАИМ:Ромашка" {
		t.Fatalf("поле сущности подменено реквизитом формы: %+v", resp.Messages)
	}
}

func keysOfAny(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTypeFormAttrValue(t *testing.T) {
	if v := typeFormAttrValue("string(10)", ""); v != nil {
		t.Errorf("пустая строка должна давать nil, получено %v", v)
	}
	if v := typeFormAttrValue("bool", "true"); v != true {
		t.Errorf("bool: %v", v)
	}
	if v := typeFormAttrValue("bool", "false"); v != false {
		t.Errorf("bool false: %v", v)
	}
	if v := typeFormAttrValue("string(10)", "текст"); v != "текст" {
		t.Errorf("string: %v", v)
	}
	if v := typeFormAttrValue("date", "2026-02-19"); v == "2026-02-19" {
		t.Errorf("дата не разобрана: %v", v)
	}
	if v := typeFormAttrValue("number(10,2)", "12,50"); v == "12,50" {
		t.Errorf("число не разобрано: %v", v)
	}
}

func TestNormalizeFormAttrKeys(t *testing.T) {
	ent := &metadata.Entity{Name: "К", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
		{Name: "Склад", TypeRef: "CatalogRef.Склад", Save: false},
		{Name: "Наименование", TypeRef: "string(10)", Save: false}, // коллизия — не трогаем
	}}
	values := map[string]any{"склад": "x", "Наименование": "y"}
	got := normalizeFormAttrKeys(values, form, ent)
	if got["Склад"] != "x" {
		t.Errorf("регистр реквизита формы не восстановлен: %v", got)
	}
	if _, stale := got["склад"]; stale {
		t.Errorf("остался нижнерегистровый дубль: %v", got)
	}
	if got["Наименование"] != "y" {
		t.Errorf("поле сущности испорчено: %v", got)
	}
}
