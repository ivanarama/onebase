package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestCheckFormBackground_InvalidColorWarns(t *testing.T) {
	warns := CheckFormBackground(projWithElement(&metadata.FormElement{
		Kind:       metadata.FormElementGroupBox,
		Name:       "ГруппаДействия",
		Background: "зеленый-в-клеточку",
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	if warns[0].Code != "form.background-color" {
		t.Errorf("Code = %q, ожидался form.background-color", warns[0].Code)
	}
	if !strings.Contains(warns[0].SuggestedFix, "hex") {
		t.Errorf("подсказка не называет допустимые форматы: %q", warns[0].SuggestedFix)
	}
}

func TestCheckFormBackground_NonGroupKindWarns(t *testing.T) {
	warns := CheckFormBackground(projWithElement(&metadata.FormElement{
		Kind:       metadata.FormElementField,
		Name:       "ПолеНаименование",
		DataPath:   "Объект.Наименование",
		Background: "#e8f5e9",
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	if warns[0].Code != "form.background-kind" {
		t.Errorf("Code = %q, ожидался form.background-kind", warns[0].Code)
	}
}

func TestCheckFormBackground_ValidColorSilent(t *testing.T) {
	warns := CheckFormBackground(projWithElement(&metadata.FormElement{
		Kind:       metadata.FormElementGroupBox,
		Name:       "ГруппаДействия",
		Background: "#e8f5e9",
	}))
	if len(warns) != 0 {
		t.Fatalf("валидный фон не должен предупреждать: %+v", warns)
	}
}

func TestCheckFormBackground_MalformedRGBWarns(t *testing.T) {
	for _, color := range []string{
		"rgb(,)",
		"rgba(1)",
		"rgb(100%,0,50%)",
		"rgb(1 %,0,0)",
		"rgb(1.,0,0)",
	} {
		t.Run(color, func(t *testing.T) {
			warns := CheckFormBackground(projWithElement(&metadata.FormElement{
				Kind:       metadata.FormElementGroupBox,
				Name:       "ГруппаДействия",
				Background: color,
			}))
			if len(warns) != 1 || warns[0].Code != "form.background-color" {
				t.Fatalf("background %q должен дать form.background-color: %+v", color, warns)
			}
		})
	}
}

func TestCheckFormBackground_CSSColor4RGBSilent(t *testing.T) {
	for _, color := range []string{
		"rgb(255 0 0)",
		"rgb(0,0,0,.5)",
		"rgba(0,0,0)",
		"rgb(256,0,0)",
		"rgba(0,0,0,1.01)",
	} {
		t.Run(color, func(t *testing.T) {
			warns := CheckFormBackground(projWithElement(&metadata.FormElement{
				Kind:       metadata.FormElementGroupBox,
				Name:       "ГруппаДействия",
				Background: color,
			}))
			if len(warns) != 0 {
				t.Fatalf("валидный background %q не должен предупреждать: %+v", color, warns)
			}
		})
	}
}

// Схема --lint обязана знать тот же ключ, который реально загружает
// FormElement. Иначе корректная форма одновременно работает в runtime и
// получает ложное metadata.unvalidated-key на обязательном build-гейте.
func TestLintYAML_FormBackgroundKnown(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ГруппаФормы
    name: Действия
    background: "rgba(255, 255, 255, .8)"
    children: []
`)
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code == "metadata.unvalidated-key" {
			t.Fatalf("background формы должен быть известен YAML-линту: %+v", issue)
		}
	}
}
