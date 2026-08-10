package ui

// kind: ПолеКода — многострочный редактор с подсветкой. Проверяем разметку:
// JS-монтирование опирается на классы .code-field / .code-editor и на то, что
// контейнер редактора идёт сразу за textarea (previousElementSibling).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func renderCodeElement(t *testing.T, el *metadata.FormElement) string {
	t.Helper()
	ent := &metadata.Entity{
		Name: "ПравилоОбмена",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "КодПравила", Type: metadata.FieldTypeString},
		},
	}
	ctx := map[string]any{
		"Entity":      ent,
		"Values":      map[string]string{"КодПравила": "Возврат 1;"},
		"RefOptions":  map[string]any{},
		"EnumOptions": map[string]any{},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "managed-element", map[string]any{"El": el, "Ctx": ctx}); err != nil {
		t.Fatalf("execute managed-element: %v", err)
	}
	return buf.String()
}

func TestManagedCodeField_RendersTextareaAndHolder(t *testing.T) {
	out := renderCodeElement(t, &metadata.FormElement{
		Kind:     metadata.FormElementCodeField,
		Name:     "КодПравила",
		DataPath: "Объект.КодПравила",
		Language: "bsl",
	})

	if !strings.Contains(out, `class="code-field"`) {
		t.Errorf("нет textarea.code-field — без JS поле стало бы нередактируемым:\n%s", out)
	}
	if !strings.Contains(out, `class="code-editor"`) {
		t.Errorf("нет контейнера .code-editor:\n%s", out)
	}
	if !strings.Contains(out, `data-code-language="bsl"`) {
		t.Errorf("язык подсветки не проброшен в разметку:\n%s", out)
	}
	if !strings.Contains(out, "Возврат 1;") {
		t.Errorf("значение поля не попало в textarea:\n%s", out)
	}
	ta := strings.Index(out, `class="code-field"`)
	ed := strings.Index(out, `class="code-editor"`)
	if ta < 0 || ed < 0 || ed < ta {
		t.Errorf("контейнер редактора должен идти ПОСЛЕ textarea — иначе JS его не найдёт:\n%s", out)
	}
}

// Без language подсветка не выключается, а становится plaintext: пустой язык
// Monaco не принимает.
func TestManagedCodeField_DefaultsToPlaintext(t *testing.T) {
	out := renderCodeElement(t, &metadata.FormElement{
		Kind:     metadata.FormElementCodeField,
		Name:     "КодПравила",
		DataPath: "Объект.КодПравила",
	})
	if !strings.Contains(out, `data-code-language="plaintext"`) {
		t.Errorf("ожидался plaintext по умолчанию:\n%s", out)
	}
}

// У нередактируемого поля редактора быть не должно: он перекрыл бы textarea и
// дал бы править то, что править нельзя.
func TestManagedCodeField_ReadOnlyHasNoEditor(t *testing.T) {
	out := renderCodeElement(t, &metadata.FormElement{
		Kind:     metadata.FormElementCodeField,
		Name:     "КодПравила",
		DataPath: "Объект.КодПравила",
		ReadOnly: true,
	})
	if strings.Contains(out, `class="code-editor"`) {
		t.Errorf("у readonly-поля не должно быть контейнера редактора:\n%s", out)
	}
	if !strings.Contains(out, "readonly") {
		t.Errorf("textarea должна быть readonly:\n%s", out)
	}
}
