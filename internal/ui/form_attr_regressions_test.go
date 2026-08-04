package ui

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// collisionServer поднимает сервер с РЕАЛЬНОЙ базой и справочником
// «СовсемДругойСправочник» в реестре: без этого GetEntity вернул бы nil,
// mergeFormLocalRefOptions вышла бы раньше подмены ключа, и тест коллизии
// проверял бы пустоту.
func collisionServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	other := &metadata.Entity{
		Name: "СовсемДругойСправочник", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{other}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, other.Name, uuid.New(),
		map[string]any{"Наименование": "ЧужойВариант"}, other); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{other}})
	return &Server{reg: reg, store: db}
}

// Реквизит формы, совпадающий по имени с полем сущности, не должен подменять
// собранные для этого поля варианты: шаблон рисует поле сущности, и подмена
// отдала бы ему опции чужого справочника, а текущее значение выпало бы из
// списка — при следующем сохранении ссылка молча очищается.
func TestMergeFormLocalRefOptions_KeepsEntityFieldOptions(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Статус", Type: metadata.FieldType("reference:СтатусЗаявки"), RefEntity: "СтатусЗаявки"}},
	}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
		// Одноимённый полю сущности реквизит формы, но ссылается на другой справочник.
		{Name: "Статус", TypeRef: "CatalogRef.СовсемДругойСправочник", Save: false},
	}}
	entityRows := []map[string]any{{"id": "st-1", "_label": "Новая"}}
	data := map[string]any{
		"Entity":     entity,
		"RefOptions": map[string][]map[string]any{"Статус": entityRows},
	}

	collisionServer(t).mergeFormLocalRefOptions(context.Background(), form, data)

	got, _ := data["RefOptions"].(map[string][]map[string]any)
	if len(got["Статус"]) != 1 || got["Статус"][0]["_label"] != "Новая" {
		t.Fatalf("опции поля сущности подменены реквизитом формы: %+v", got["Статус"])
	}
}

// Реквизит формы с уникальным именем по-прежнему получает свои опции.
func TestMergeFormLocalRefOptions_KeepsDistinctAttrKey(t *testing.T) {
	entity := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
	form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
		{Name: "Причина", TypeRef: "CatalogRef.ПричинаОтказа", Save: false},
	}}
	data := map[string]any{
		"Entity":     entity,
		"RefOptions": map[string][]map[string]any{"Номер": {{"id": "x"}}},
	}
	s := &Server{reg: runtime.NewRegistry()}
	s.mergeFormLocalRefOptions(context.Background(), form, data)

	got, _ := data["RefOptions"].(map[string][]map[string]any)
	if len(got["Номер"]) != 1 {
		t.Errorf("чужой ключ затёрт: %+v", got)
	}
	// Реестр пуст, поэтому сущность не резолвится и опции не добавляются —
	// важно, что ключ не создан пустым и ничего не сломано.
	if _, exists := got["Причина"]; exists {
		t.Errorf("для нерезолвящейся сущности ключ создаваться не должен: %+v", got)
	}
}

// Элемент с обработчиками, среди которых нет нужного события, не должен глушить
// фолбэк на одноимённую команду: раньше кнопка автопанели оказывалась мёртвой.
func TestResolveHandlerProc_ElementWithoutEventFallsBackToCommand(t *testing.T) {
	form := &metadata.FormModule{
		Commands: []*metadata.FormCommand{{Name: "Классифицировать", Action: "КлассифицироватьНажатие"}},
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementField, Name: "Классифицировать",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ЧтоТоПриИзменении"},
		}},
	}
	if proc := resolveHandlerProc(form, "Классифицировать", "Нажатие"); proc != "КлассифицироватьНажатие" {
		t.Fatalf("ожидалась КлассифицироватьНажатие, получено %q", proc)
	}
	// Собственный обработчик элемента по своему событию по-прежнему в приоритете.
	if proc := resolveHandlerProc(form, "Классифицировать", "ПриИзменении"); proc != "ЧтоТоПриИзменении" {
		t.Fatalf("обработчик элемента должен быть приоритетнее, получено %q", proc)
	}
}

// Фолбэк на команду ограничен событиями, которые автопанель реально шлёт:
// иначе команда выполнялась бы на любом событии, включая мусорное имя.
func TestResolveHandlerProc_CommandFallbackOnlyForClickAndChoice(t *testing.T) {
	form := &metadata.FormModule{
		Commands: []*metadata.FormCommand{{Name: "Провести", Action: "ПровестиКоманда"}},
	}
	for _, evt := range []string{"Нажатие", "Выбор"} {
		if proc := resolveHandlerProc(form, "Провести", evt); proc != "ПровестиКоманда" {
			t.Errorf("событие %s: ожидалась ПровестиКоманда, получено %q", evt, proc)
		}
	}
	for _, evt := range []string{"ПриИзменении", "НачалоВыбора", "ЧтоУгодно"} {
		if proc := resolveHandlerProc(form, "Провести", evt); proc != "" {
			t.Errorf("событие %s не должно резолвиться на команду, получено %q", evt, proc)
		}
	}
}

// Автопанель шлёт имя команды как есть; сравнение регистронезависимое, чтобы
// расхождение регистра в конфигурации не давало молча мёртвую кнопку.
func TestResolveHandlerProc_CommandNameCaseInsensitive(t *testing.T) {
	form := &metadata.FormModule{
		Commands: []*metadata.FormCommand{{Name: "Провести", Action: "ПровестиКоманда"}},
	}
	if proc := resolveHandlerProc(form, "провести", "Нажатие"); proc != "ПровестиКоманда" {
		t.Fatalf("ожидалась ПровестиКоманда при другом регистре, получено %q", proc)
	}
}

// Сквозная проверка: при коллизии имён форма рендерит поле сущности с ЕГО
// вариантами, а не с вариантами одноимённого реквизита формы.
func TestManagedFormRendersEntityOptionsOnNameCollision(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка",
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{
			{Name: "Статус", TypeRef: "CatalogRef.СовсемДругойСправочник", Save: false},
		},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеСтатус", DataPath: "Объект.Статус"},
		},
	}
	entity := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Статус", Type: metadata.FieldType("reference:СтатусЗаявки"), RefEntity: "СтатусЗаявки"}},
		Forms:  []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity": entity, "Form": form, "IsNew": true,
		"Values":        map[string]string{"Статус": "st-1"},
		"RefOptions":    map[string][]map[string]any{"Статус": {{"id": "st-1", "_label": "Новая"}}},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil, "Lang": "ru",
	}
	collisionServer(t).mergeFormLocalRefOptions(context.Background(), form, data)

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "Новая") {
		t.Error("вариант поля сущности не отрисован")
	}
	if !strings.Contains(html, `value="st-1" selected`) {
		t.Error("текущее значение поля сущности потеряно")
	}
	if strings.Contains(html, "СовсемДругойСправочник") {
		t.Error("в разметку просочился справочник одноимённого реквизита формы")
	}
}

// Объявленный скалярный реквизит формы — рабочее поле, а не подозрительное:
// жёлтая подсветка «не найден среди полей сущности» адресована опечатке в
// data_path, и на штатном реквизите она сбивала с толку.
func TestManagedFormRendersDeclaredScalarAttrWithoutWarning(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Обращение",
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "Телефон", TypeRef: "Строка"}},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеТелефон", DataPath: "Телефон"},
			{Kind: metadata.FormElementField, Name: "ПолеОпечатка", DataPath: "Тлефон"},
		},
	}
	entity := &metadata.Entity{
		Name: "Обращение", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity": entity, "Form": form, "IsNew": true,
		"Values":        map[string]string{},
		"RefOptions":    map[string][]map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"User":          nil, "Lang": "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, `name="Телефон" value="" placeholder="Телефон" style="background:#fef9c3"`) {
		t.Error("объявленный реквизит формы подсвечен как ненайденное поле")
	}
	if !strings.Contains(html, `name="Телефон"`) {
		t.Error("поле объявленного реквизита формы не отрисовано")
	}
	// Опечатка в data_path по-прежнему видна.
	if !strings.Contains(html, `name="Тлефон" value="" placeholder="Тлефон" style="background:#fef9c3"`) {
		t.Error("необъявленный data_path потерял предупреждающую подсветку")
	}
}
