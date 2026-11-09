package configcheck

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestCheckFormTextColor(t *testing.T) {
	cases := []struct {
		name string
		el   *metadata.FormElement
		want []string
	}{
		{"поле importance error", &metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеОшибка", DataPath: "Объект.Наименование", ReadOnly: true, Importance: "error"}, nil},
		{"надпись с цветом", &metadata.FormElement{Kind: metadata.FormElementLabel, Name: "Надпись", TextColor: "#b91c1c"}, nil},
		{"неверный цвет", &metadata.FormElement{Kind: metadata.FormElementField, Name: "Поле", DataPath: "Объект.Наименование", TextColor: "url(x)"}, []string{"form.text-color"}},
		{"неизвестная importance", &metadata.FormElement{Kind: metadata.FormElementLabel, Name: "Надпись", Importance: "warning"}, []string{"form.importance"}},
		{"не тот вид элемента", &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Группа", Importance: "error"}, []string{"form.text-color-kind"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			warns := CheckFormTextColor(projWithElement(c.el))
			if len(warns) != len(c.want) {
				t.Fatalf("предупреждения %+v, ждали коды %v", warns, c.want)
			}
			for i, code := range c.want {
				if warns[i].Code != code {
					t.Errorf("Code = %q, ждали %q", warns[i].Code, code)
				}
			}
		})
	}
}
