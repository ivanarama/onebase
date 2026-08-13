package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Колонки табличной части подписываются синонимом реквизита (`title`/`titles`),
// как в автогенерируемой форме. Имя реквизита остаётся идентификатором: по нему
// идёт привязка данных SlickGrid и разбор имён полей tp.<ТЧ>.<i>.<реквизит>.
func TestManagedTablePartColumnsUseFieldTitles(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "КодКлиента", Type: metadata.FieldTypeString, Title: "Код клиента"},
			{Name: "Количество", Type: metadata.FieldTypeNumber,
				Titles: map[string]string{"ru": "Кол-во", "en": "Qty"}},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		}}},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
		}},
	}

	// Та же ТЧ без SlickGrid (no_grid) — второй путь отрисовки заголовков.
	plainForm := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			NoGrid: true,
		}},
	}

	renderForm := func(f *metadata.FormModule, lang string) string {
		var buf bytes.Buffer
		err := tmpl.ExecuteTemplate(&buf, "page-managed-form", map[string]any{
			"Entity": ent, "Form": f, "IsNew": true, "CanWrite": true,
			"Values": map[string]string{}, "RefOptions": map[string]any{},
			"EnumOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
			"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]any{},
			"Lang": lang,
		})
		if err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}
	render := func(lang string) string { return renderForm(form, lang) }

	html := render("ru")
	// SlickGrid: подпись — синоним, идентификатор — имя реквизита.
	if !strings.Contains(html, `{"id":"КодКлиента","name":"Код клиента"`) {
		t.Errorf("колонка SlickGrid подписана именем реквизита вместо синонима")
	}
	if !strings.Contains(html, `{"id":"Количество","name":"Кол-во"`) {
		t.Errorf("локализованный синоним (titles.ru) не попал в подпись колонки")
	}
	// Без синонима остаётся имя реквизита.
	if !strings.Contains(html, `{"id":"Комментарий","name":"Комментарий"`) {
		t.Errorf("реквизит без синонима должен подписываться собственным именем")
	}
	// Простая таблица (no_grid) подписывается так же.
	plain := renderForm(plainForm, "ru")
	if !strings.Contains(plain, `<th>Код клиента</th>`) || !strings.Contains(plain, `<th>Кол-во</th>`) {
		t.Errorf("заголовок простой таблицы ТЧ не использует синоним")
	}
	// Привязка данных в простой таблице остаётся по имени реквизита.
	if !strings.Contains(plain, `КодКлиента|string`) {
		t.Errorf("data-tp-fields должен нести имена реквизитов, а не подписи")
	}

	// Язык интерфейса выбирает перевод синонима.
	if en := render("en"); !strings.Contains(en, `{"id":"Количество","name":"Qty"`) {
		t.Errorf("английский синоним не подставился при Lang=en")
	}
}
