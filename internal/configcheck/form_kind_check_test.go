package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Неизвестный вид элемента (напр. выдуманный «ПолеИзображения») даёт блокирующую
// ошибку с кодом form.unknown-kind и подсказкой на реальные виды — иначе check
// молча пропускал бы его, а форма падала в рантайме (issue #598).
func TestCheckFormElementKind_UnknownKindErrors(t *testing.T) {
	issues := CheckFormElementKind(projWithElement(&metadata.FormElement{
		Kind:     "ПолеИзображения",
		Name:     "ПолеФото",
		DataPath: "Объект.Фото",
	}))
	if len(issues) != 1 {
		t.Fatalf("ожидалась 1 ошибка, получили %d: %+v", len(issues), issues)
	}
	is := issues[0]
	if is.Code != "form.unknown-kind" {
		t.Errorf("Code = %q, ожидался form.unknown-kind", is.Code)
	}
	if !strings.Contains(is.Message, "ПолеИзображения") {
		t.Errorf("сообщение должно называть неизвестный вид: %q", is.Message)
	}
	if !strings.Contains(is.SuggestedFix, "ПолеВвода") {
		t.Errorf("подсказка должна указывать реальный вид (ПолеВвода): %q", is.SuggestedFix)
	}
	if is.File != "forms/входящееписьмо/объекта.form.yaml" {
		t.Errorf("File = %q, ожидался путь формы в нижнем регистре", is.File)
	}
}

// Реальные виды (в т.ч. поле-картинка через ПолеВвода) и пустой kind ошибок не дают.
func TestCheckFormElementKind_KnownKindsOK(t *testing.T) {
	for _, k := range append(metadata.KnownFormElementTypes(), "") {
		if issues := CheckFormElementKind(projWithElement(&metadata.FormElement{
			Kind: k, Name: "Эл", DataPath: "Объект.Поле",
		})); len(issues) != 0 {
			t.Errorf("kind %q не должен давать ошибок, получили %+v", k, issues)
		}
	}
}
