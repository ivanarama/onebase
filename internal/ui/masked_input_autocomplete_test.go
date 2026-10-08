package ui

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Поле с маской ввода — почти всегда телефон. Автозаполнение Яндекс Браузера
// на autocomplete="off" (#595) не смотрит и подсказывает сохранённые номера из
// своего профиля; на поле "one-time-code" (по стандарту — код подтверждения)
// эти подсказки в Яндекс Браузере пропадают, проверено вручную. Поле без маски
// остаётся "off". Тест проверяет выдаваемый атрибут в разметке формы, отданной
// обычным GET (и у поля объекта, и у реквизита формы), а не поведение браузеров.
func TestManagedForm_MaskedInputSuppressesBrowserAutofill(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПолеНаименование", DataPath: "Объект.Наименование", InputMask: "(000)000-00-00"},
		{Kind: metadata.FormElementField, Name: "ПолеТелефон", DataPath: "Телефон", InputMask: "(000)000-00-00"},
		{Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Комментарий"},
	})
	ent.Forms[0].Attributes = append(ent.Forms[0].Attributes,
		&metadata.FormAttribute{Name: "Телефон", TypeRef: "string"},
		&metadata.FormAttribute{Name: "Комментарий", TypeRef: "string"})
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id,
		map[string]any{"Наименование": "(495)111-22-33"}, ent); err != nil {
		t.Fatal(err)
	}
	body := renderObjectFormGET(t, srv, ent, id)

	for name, want := range map[string]string{
		"Наименование": "one-time-code", // поле объекта с маской
		"Телефон":      "one-time-code", // реквизит формы с маской
		"Комментарий":  "off",           // без маски — как прежде (#595)
	} {
		re := regexp.MustCompile(`<input type="text" autocomplete="([^"]*)" name="` + regexp.QuoteMeta(name) + `"`)
		m := re.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("нет текстового поля %s в разметке формы", name)
		}
		if m[1] != want {
			t.Errorf("%s: autocomplete=%q, ожидалось %q", name, m[1], want)
		}
	}
}
