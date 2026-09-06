package ui

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedTablePartColumnsUseFormElementTitlesThroughHandler(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noGrid bool
	}{
		{name: "SlickGrid"},
		{name: "no_grid", noGrid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ent := &metadata.Entity{
				Name: "Регион", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
				TableParts: []metadata.TablePart{{Name: "Тарифы", Fields: []metadata.Field{
					{
						Name: "УровеньНапряжения", Type: metadata.FieldTypeString,
						Title: "Заголовок реквизита",
					},
					{
						Name: "Ставка", Type: metadata.FieldTypeNumber,
						Title: "Ставка реквизита",
					},
				}}},
			}
			form := &metadata.FormModule{
				Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
				LayoutKind: metadata.FormLayoutManaged,
				Elements: []*metadata.FormElement{{
					Kind: metadata.FormElementTablePart, Name: "Тарифы", DataPath: "Объект.Тарифы",
					NoGrid: tc.noGrid,
					Children: []*metadata.FormElement{{
						Kind: metadata.FormElementColumn, Name: "КолУровеньНапряжения",
						DataPath: "Объект.Тарифы.УровеньНапряжения",
						TitleMap: map[string]string{"ru": "ТУН"},
					}, {
						Kind: metadata.FormElementColumn, Name: "КолСтавка",
						DataPath: "Объект.Тарифы.Ставка",
					}},
				}},
			}
			ent.Forms = []*metadata.FormModule{form}

			s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			req := reqWithChi(http.MethodGet, "/ui/catalog/регион/new", nil, map[string]string{"entity": "регион"})
			rec := httptest.NewRecorder()
			s.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET формы: code = %d, body: %s", rec.Code, rec.Body.String())
			}

			if tc.noGrid {
				if !strings.Contains(rec.Body.String(), `<th>ТУН</th>`) ||
					!strings.Contains(rec.Body.String(), `<th>Ставка реквизита</th>`) {
					t.Fatalf("no_grid нарушил приоритет заголовков элемента и реквизита: %s", rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), `<th>Заголовок реквизита</th>`) {
					t.Fatal("no_grid оставил заголовок реквизита старше title элемента")
				}
				return
			}

			cols := parseManagedTPColumns(t, rec.Body.String())
			if len(cols) != 2 || cols[0].ID != "УровеньНапряжения" || cols[0].Name != "ТУН" ||
				cols[1].ID != "Ставка" || cols[1].Name != "Ставка реквизита" {
				t.Fatalf("SlickGrid потерял title элемента или идентификатор реквизита: %+v", cols)
			}
		})
	}
}

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

	htmlOut := render("ru")
	cols := parseManagedTPColumns(t, htmlOut)
	byID := make(map[string]managedTPColumnJSON, len(cols))
	for _, col := range cols {
		byID[col.ID] = col
	}
	// SlickGrid: подпись — синоним, идентификатор — имя реквизита.
	if byID["КодКлиента"].Name != "Код клиента" {
		t.Errorf("колонка SlickGrid подписана именем реквизита вместо синонима")
	}
	if byID["Количество"].Name != "Кол-во" {
		t.Errorf("локализованный синоним (titles.ru) не попал в подпись колонки")
	}
	// Без синонима остаётся имя реквизита.
	if byID["Комментарий"].Name != "Комментарий" {
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
	if en := parseManagedTPColumns(t, render("en")); len(en) != 3 || en[1].Name != "Qty" {
		t.Errorf("английский синоним не подставился при Lang=en")
	}
}

func parseManagedTPColumns(t *testing.T, rendered string) []managedTPColumnJSON {
	t.Helper()
	const prefix = `data-sg-cols='`
	start := strings.Index(rendered, prefix)
	if start < 0 {
		t.Fatal("data-sg-cols не найден")
	}
	start += len(prefix)
	end := strings.Index(rendered[start:], `'`)
	if end < 0 {
		t.Fatal("data-sg-cols не закрыт")
	}
	raw := html.UnescapeString(rendered[start : start+end])
	var cols []managedTPColumnJSON
	if err := json.Unmarshal([]byte(raw), &cols); err != nil {
		t.Fatalf("data-sg-cols содержит невалидный JSON %q: %v", raw, err)
	}
	return cols
}

func TestManagedTablePartColumnTitlesProduceValidJSON(t *testing.T) {
	fields := []metadata.Field{{
		Name: "Комментарий", Type: metadata.FieldTypeString,
		Title: `Размер "XL" & client's <выбор>`,
	}}
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-managed-form", map[string]any{
		"Entity": &metadata.Entity{
			Name: "Заказ", Kind: metadata.KindDocument,
			TableParts: []metadata.TablePart{{Name: "Строки", Fields: fields}},
		},
		"Form": &metadata.FormModule{
			Name: "ФормаОбъекта", Kind: "object", EntityName: "Заказ",
			LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{{
				Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			}},
		},
		"IsNew": true, "CanWrite": true, "Lang": "ru",
		"Values": map[string]string{}, "RefOptions": map[string]any{},
		"EnumOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
		"TPRefOptions": map[string]any{}, "TPEnumLabels": map[string]any{},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	cols := parseManagedTPColumns(t, buf.String())
	if len(cols) != 1 || cols[0].Name != fields[0].Title {
		t.Fatalf("синоним испорчен при JSON/HTML-кодировании: %+v", cols)
	}
}
