package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func managedRequiredFieldEntity(field metadata.Field, elementRequired bool) *metadata.Entity {
	element := &metadata.FormElement{
		Kind:      metadata.FormElementField,
		Name:      "ПолеПроверки",
		DataPath:  "Объект." + field.Name,
		Required:  elementRequired,
		TitleMap:  map[string]string{"ru": "Проверяемое поле"},
		Multiline: field.Type == metadata.FieldTypeRichText,
	}
	return &metadata.Entity{
		Name:   "ПроверкаОбязательнойФормы",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{field},
		Forms:  []*metadata.FormModule{managedObjectForm(element)},
	}
}

func submitManagedRequiredCreate(t *testing.T, entity *metadata.Entity, entities []*metadata.Entity, body url.Values) (*Server, context.Context, *httptest.ResponseRecorder) {
	t.Helper()
	server, ctx := newSubmitTestServer(t, append([]*metadata.Entity{entity}, entities...))
	request := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new", body,
		map[string]string{"entity": entity.Name})
	response := httptest.NewRecorder()
	server.submit(response, request)
	return server, ctx, response
}

func TestSubmit_ManagedRequired_FieldTypesThroughPublicHandler(t *testing.T) {
	target := &metadata.Entity{
		Name: "ЦельОбязательнойСсылки", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	tests := []struct {
		name       string
		field      metadata.Field
		value      string
		wantStatus int
	}{
		{name: "string spaces are empty", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeString}, value: " \t ", wantStatus: http.StatusBadRequest},
		{name: "empty date", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeDate}, value: "", wantStatus: http.StatusBadRequest},
		{name: "empty number", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeNumber}, value: "", wantStatus: http.StatusBadRequest},
		{name: "visually empty richtext", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeRichText}, value: "<p><br></p>", wantStatus: http.StatusBadRequest},
		{name: "empty reference", field: metadata.Field{Name: "Значение", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name}, value: "", wantStatus: http.StatusBadRequest},
		{name: "nil reference", field: metadata.Field{Name: "Значение", Type: metadata.FieldType("reference:" + target.Name), RefEntity: target.Name}, value: uuid.Nil.String(), wantStatus: http.StatusBadRequest},
		{name: "ordinary value", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeString}, value: "заполнено", wantStatus: http.StatusSeeOther},
		{name: "richtext text is filled", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeRichText}, value: "<p>заполнено</p>", wantStatus: http.StatusSeeOther},
		{name: "numeric zero is filled", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeNumber}, value: "0", wantStatus: http.StatusSeeOther},
		{name: "false is filled", field: metadata.Field{Name: "Значение", Type: metadata.FieldTypeBool}, value: "false", wantStatus: http.StatusSeeOther},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entity := managedRequiredFieldEntity(test.field, true)
			server, ctx, response := submitManagedRequiredCreate(t, entity, []*metadata.Entity{target}, url.Values{
				test.field.Name: {test.value},
			})
			if response.Code != test.wantStatus {
				t.Fatalf("submit status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
			if err != nil {
				t.Fatal(err)
			}
			wantRows := 0
			if test.wantStatus == http.StatusSeeOther {
				wantRows = 1
			}
			if len(rows) != wantRows {
				t.Fatalf("persisted rows = %d, want %d: %#v", len(rows), wantRows, rows)
			}
			if test.wantStatus == http.StatusBadRequest {
				body := response.Body.String()
				if !strings.Contains(body, "Заполните обязательные поля:") || !strings.Contains(body, "Проверяемое поле") {
					t.Fatalf("required refusal is not shown in the managed form: %s", body)
				}
			}
		})
	}
}

func TestRequiredManagedRichTextBlank(t *testing.T) {
	const imageOnly = `<p><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="></p>`
	tests := []struct {
		name  string
		value string
		blank bool
	}{
		{name: "empty", value: "", blank: true},
		{name: "quill empty", value: "<p><br></p>", blank: true},
		{name: "markup whitespace", value: "<div> &nbsp; </div>", blank: true},
		{name: "text", value: "<p>текст</p>"},
		{name: "image only", value: imageOnly},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requiredManagedRichTextBlank(test.value); got != test.blank {
				t.Fatalf("requiredManagedRichTextBlank(%q) = %v, want %v", test.value, got, test.blank)
			}
		})
	}
}

func TestSubmit_ManagedRequired_MetadataFieldRemainsFinalStateInvariant(t *testing.T) {
	field := metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString, Required: true}
	entity := managedRequiredFieldEntity(field, false)
	server, ctx, response := submitManagedRequiredCreate(t, entity, nil, url.Values{field.Name: {""}})

	if response.Code != http.StatusOK {
		t.Fatalf("submit status = %d, want final-state validation response 200; body=%s", response.Code, response.Body.String())
	}
	rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("metadata-required empty row persisted: %#v", rows)
	}
}

func TestSubmit_ManagedRequired_MetadataFieldCanBeFilledByBeforeWriteHook(t *testing.T) {
	server, entity := setupManagedEventsServer(t, `
Процедура ПередЗаписьюФормы()
	Объект.Наименование = "заполнено обработчиком";
КонецПроцедуры
`, map[metadata.FormEventType]string{
		metadata.FormEventBeforeWrite: "ПередЗаписьюФормы",
	}, []*metadata.FormElement{
		fieldEl("ПолеНаименование", "Объект.Наименование"),
	})
	entity.Fields[0].Required = true

	request := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new",
		url.Values{"Наименование": {""}}, map[string]string{"entity": entity.Name})
	response := httptest.NewRecorder()
	server.submit(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("hook-filled metadata-required field status=%d, want 303; body=%s", response.Code, response.Body.String())
	}

	rows, err := server.store.List(context.Background(), entity.Name, entity, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || fmt.Sprint(rows[0]["Наименование"]) != "заполнено обработчиком" {
		t.Fatalf("before-write value did not satisfy final-state invariant: %#v", rows)
	}
}

func TestSubmit_ManagedRequired_MetadataAutoNumberWaitsForGeneration(t *testing.T) {
	entity := &metadata.Entity{
		Name: "АвтонумеруемыйСправочник", Kind: metadata.KindCatalog,
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6},
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString, Required: true},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Forms: []*metadata.FormModule{managedObjectForm(
			fieldEl("ПолеКод", "Объект."+metadata.StandardCodeField),
			fieldEl("ПолеНаименование", "Объект.Наименование"),
		)},
	}
	server, ctx, response := submitManagedRequiredCreate(t, entity, nil, url.Values{
		metadata.StandardCodeField: {""},
		"Наименование":             {"Запись"},
	})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("auto-numbered required field status=%d, want 303; body=%s", response.Code, response.Body.String())
	}
	rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.HasPrefix(fmt.Sprint(rows[0][metadata.StandardCodeField]), "К-") {
		t.Fatalf("required Code was not generated before final-state validation: %#v", rows)
	}
}

func TestSubmit_ManagedRequired_MetadataCheckboxPersistsFalse(t *testing.T) {
	checkbox := &metadata.FormElement{
		Kind: metadata.FormElementCheckbox, Name: "ПолеАктивен", DataPath: "Объект.Активен",
	}
	entity := &metadata.Entity{
		Name: "ОбязательныйФлажок", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Активен", Type: metadata.FieldTypeBool, Required: true}},
		Forms:  []*metadata.FormModule{managedObjectForm(checkbox)},
	}
	server, ctx, response := submitManagedRequiredCreate(t, entity, nil, url.Values{
		"_ob_present_Активен": {"1"},
	})

	if response.Code != http.StatusSeeOther {
		t.Fatalf("unchecked required bool status=%d, want 303; body=%s", response.Code, response.Body.String())
	}
	rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("unchecked checkbox row count = %d, want 1: %#v", len(rows), rows)
	}
	value, given := maskCIKeyValue(rows[0], "Активен")
	if !given || value == nil || asBool(value) {
		t.Fatalf("unchecked checkbox was not persisted as filled false: %#v", rows)
	}
}

func TestSubmitEdit_ManagedRequired_ExplicitClearDoesNotMutateRecord(t *testing.T) {
	field := metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString}
	entity := managedRequiredFieldEntity(field, true)
	server, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := server.store.Upsert(ctx, entity.Name, id, map[string]any{field.Name: "исходное"}, entity); err != nil {
		t.Fatal(err)
	}

	request := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/"+id.String(),
		url.Values{field.Name: {""}, "_version": {"1"}}, map[string]string{"entity": entity.Name, "id": id.String()})
	response := httptest.NewRecorder()
	server.submitEdit(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("submitEdit status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `name="_version" value="1"`) || !strings.Contains(body, id.String()) {
		t.Fatalf("required error lost edit identity or version: %s", body)
	}
	record, err := server.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(record[field.Name]); got != "исходное" {
		t.Fatalf("rejected edit mutated record: got %q, want %q", got, "исходное")
	}
}

func TestSubmitEdit_ManagedRequired_InheritedReadOnlyDoesNotBlockPartialEdit(t *testing.T) {
	required := &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ТолькоЧтение", DataPath: "Объект.Служебное", Required: true,
	}
	group := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "СлужебнаяГруппа", ReadOnly: true,
		Children: []*metadata.FormElement{required},
	}
	entity := &metadata.Entity{
		Name: "ОбязательноеТолькоЧтение", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Служебное", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		Forms: []*metadata.FormModule{managedObjectForm(group, fieldEl("ПолеКомментарий", "Объект.Комментарий"))},
	}
	server, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := server.store.Upsert(ctx, entity.Name, id, map[string]any{
		"Служебное": nil, "Комментарий": "до",
	}, entity); err != nil {
		t.Fatal(err)
	}

	request := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/"+id.String(),
		url.Values{"Комментарий": {"после"}}, map[string]string{"entity": entity.Name, "id": id.String()})
	response := httptest.NewRecorder()
	server.submitEdit(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("readonly required field blocked edit: status=%d body=%s", response.Code, response.Body.String())
	}
	record, err := server.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(record["Комментарий"]); got != "после" {
		t.Fatalf("partial edit did not persist: %q", got)
	}
}

func TestSubmit_ManagedRequired_FormAttributeUsesPostedValue(t *testing.T) {
	form := managedObjectForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеФормы", DataPath: "Форма.Комментарий", Required: true,
		},
		fieldEl("ПолеЧерновик", "Форма.Черновик"),
	)
	form.Attributes = []*metadata.FormAttribute{
		{Name: "Комментарий", TypeRef: "string"},
		{Name: "Черновик", TypeRef: "string"},
	}
	entity := &metadata.Entity{
		Name: "ОбязательныйРеквизитФормы", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}

	server, ctx, response := submitManagedRequiredCreate(t, entity, nil, url.Values{
		"Наименование": {"запись"}, "Комментарий": {"   "}, "Черновик": {"не потерять"},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty form attribute status=%d, want 400; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `name="Черновик" value="не потерять"`) {
		t.Fatalf("required error lost another form attribute: %s", response.Body.String())
	}
	rows, err := server.store.List(ctx, entity.Name, entity, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty required form attribute allowed persistence: %#v", rows)
	}
}

func TestSubmit_ManagedRequired_RemainsUIOnlyInvariant(t *testing.T) {
	field := metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString}
	entity := managedRequiredFieldEntity(field, true)
	server, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	result, err := server.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: entity, ID: id, IsNew: true, Fields: map[string]any{field.Name: ""},
	})
	if err != nil || result.DSLError != "" {
		t.Fatalf("form-local required leaked into entityservice: result=%+v err=%v", result, err)
	}
	if _, err := server.store.GetByID(ctx, entity.Name, id, entity); err != nil {
		t.Fatalf("direct service write was unexpectedly rejected: %v", err)
	}
}
