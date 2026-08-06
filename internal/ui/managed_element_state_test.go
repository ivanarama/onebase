package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Условная доступность элементов управляемой формы (readonly_when / hidden_when).
// Смысл: запрет, который живёт в бизнес-логике, должен быть ВИДЕН на форме, а не
// прилетать исключением при записи — принятая заявка показывает производственные
// реквизиты нередактируемыми, а не «активными до первой попытки сохранить».

func формаСУсловиями(ent *metadata.Entity, els ...*metadata.FormElement) *metadata.FormModule {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": ent.Name},
		Elements:   els,
	}
	ent.Forms = []*metadata.FormModule{form}
	return form
}

func отрисоватьСУсловиями(t *testing.T, ent *metadata.Entity, form *metadata.FormModule, values map[string]string) string {
	t.Helper()
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false,
		"Values": values, "RefOptions": map[string]any{},
		"EnumOptions": map[string][]EnumOption{}, "TPRefOptions": map[string]any{},
		"User": nil, "Lang": "ru",
	}
	s.prepareManagedFormData(context.Background(), data, form)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func заявкаСоСтадией() *metadata.Entity {
	return &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Улица", Type: metadata.FieldTypeString},
		{Name: "СтадияОформления", Type: metadata.FieldTypeString},
	}}
}

func TestУсловныйReadonly_ПоСостояниюЗаписи(t *testing.T) {
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	})

	// Проверяем сам input, а не наличие слова «readonly» на странице: оно есть в
	// служебном CSS формы (правило .form-group input[readonly]).
	вводУлицы := func(html string) string {
		i := strings.Index(html, `name="Улица"`)
		if i < 0 {
			t.Fatalf("поле «Улица» не отрисовано:\n%s", html)
		}
		j := strings.Index(html[i:], ">")
		return html[i : i+j]
	}

	черновик := вводУлицы(отрисоватьСУсловиями(t, ent, form, map[string]string{
		"Улица": "Ленина 1", "СтадияОформления": "НаОформлении"}))
	if strings.Contains(черновик, "readonly") {
		t.Errorf("черновик: поле не должно быть нередактируемым: %s", черновик)
	}

	принята := вводУлицы(отрисоватьСУсловиями(t, ent, form, map[string]string{
		"Улица": "Ленина 1", "СтадияОформления": "Принята"}))
	if !strings.Contains(принята, "readonly") {
		t.Errorf("принятая заявка: поле должно быть нередактируемым: %s", принята)
	}
}

func TestУсловноеСкрытие_ЭлементНеОтрисован(t *testing.T) {
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent,
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнопкаПринять",
			TitleMap:   map[string]string{"ru": "Принять заявку"},
			HiddenWhen: `СтадияОформления = "Принята"`,
			Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnClick: "Принять"},
		})

	черновик := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "НаОформлении"})
	if !strings.Contains(черновик, "Принять заявку") {
		t.Errorf("черновик: кнопка должна быть видна\n%s", черновик)
	}

	принята := отрисоватьСУсловиями(t, ent, form, map[string]string{"СтадияОформления": "Принята"})
	if strings.Contains(принята, "Принять заявку") {
		t.Errorf("принятая заявка: кнопка не должна отрисовываться\n%s", принята)
	}
}

func TestСостоянияЭлементов_СодержатЛожныеУсловия(t *testing.T) {
	// В карте состояний должен присутствовать КАЖДЫЙ элемент с объявленным
	// условием, в том числе с ложным: ответ события формы переносит карты на
	// клиент, и без явного false он не смог бы снять запрет.
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `СтадияОформления = "Принята"`,
	})
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}

	st := s.formElementStates(form, ent, map[string]any{"СтадияОформления": "НаОформлении"})
	if st == nil {
		t.Fatal("состояния не рассчитаны, ожидалась карта с ложным условием")
	}
	if v, есть := st.ReadOnly["ПолеУлица"]; !есть || v {
		t.Errorf("ReadOnly[ПолеУлица] = (%v, есть=%v), ожидалось (false, есть=true)", v, есть)
	}

	st = s.formElementStates(form, ent, map[string]any{"СтадияОформления": "Принята"})
	if !st.ReadOnly["ПолеУлица"] {
		t.Errorf("на принятой заявке ожидалось ReadOnly[ПолеУлица]=true")
	}
}

func TestНеверноеУсловие_НеЗапираетЭлемент(t *testing.T) {
	// Ошибка в условии — ошибка конфигурации. Молча запертое поле объяснить
	// пользователю нечем, поэтому условие игнорируется, а конфигуратор получает
	// предупреждение на форме.
	ent := заявкаСоСтадией()
	form := формаСУсловиями(ent, &metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеУлица",
		DataPath: "Объект.Улица", ReadOnlyWhen: `((`,
	})
	s := &Server{interp: interpreter.New(), reg: runtime.NewRegistry()}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": false,
		"Values": map[string]string{"СтадияОформления": "Принята"},
	}
	s.prepareManagedFormData(context.Background(), data, form)

	ro, _ := data["ElReadOnly"].(map[string]bool)
	if ro["ПолеУлица"] {
		t.Error("нерабочее условие не должно делать поле нередактируемым")
	}
	if data["FormWarnings"] == nil {
		t.Error("ожидалось предупреждение конфигуратору о нерабочем условии")
	}
}
