package ui

import (
	"strings"
	"testing"
)

// #595: строковые поля формы получают autocomplete="off". Без него нативный
// autofill браузера, привязанный к атрибуту name="Наименование" (одинаковому у
// всех справочников), сваливал ранее введённые значения всех сущностей в одну
// корзину. Это не задуманная фича «недавние значения» — её в коде нет.
func TestObjectForm_StringInputSuppressesAutofill(t *testing.T) {
	srv, ent, id := setupDeleteActionServer(t) // запись с текстовым полем Наименование
	body := renderObjectFormGET(t, srv, ent, id)

	if !strings.Contains(body, `<input type="text" autocomplete="off"`) {
		snippet := body
		if len(snippet) > 400 {
			snippet = snippet[:400]
		}
		t.Fatalf("на форме нет текстового инпута с autocomplete=off — браузерный autofill не подавлен:\n%s", snippet)
	}
}
