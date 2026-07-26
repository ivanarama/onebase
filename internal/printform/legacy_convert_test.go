package printform

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/sheet"
	"gopkg.in/yaml.v3"
)

// repForm — репрезентативная legacy-форма (счёт-фактура-подобная): title с
// интерполяцией, markdown-шапка, таблица с @row/ссылками/форматами и итогами,
// подвал с money-форматтером и {{Итог.*}}.
func repForm() *PrintForm {
	return &PrintForm{
		Name:     "СчётФактура",
		Document: "Реализация",
		Title:    "Счёт-фактура № {{Номер}} от {{Дата | date}}",
		Header:   "**Продавец**: {{Организация.Наименование}}\n\n**Покупатель**: {{Покупатель.Наименование}}",
		Table: &TableSection{
			Source: "Товары",
			Columns: []Column{
				{Field: "@row", Label: "№", Width: "40px"},
				{Field: "Номенклатура.Наименование", Label: "Наименование"},
				{Field: "Количество", Label: "Кол-во", Align: "right"},
				{Field: "Цена", Label: "Цена", Align: "right", Format: "number:2"},
				{Field: "Сумма", Label: "Стоимость", Align: "right", Format: "number:2"},
			},
			Totals: []TotalSpec{
				{Field: "Сумма", Sum: true, Label: "Итого"},
			},
		},
		Footer: "___\n\n**Всего к оплате**: {{СуммаДокумента | money}} руб.\n\n___\n\nРуководитель: ___________",
	}
}

func areaByName(lt *LayoutTemplate, name string) *LayoutArea {
	return lt.Area(name)
}

func TestConvertLegacy_Title(t *testing.T) {
	lt, err := ConvertLegacy(repForm())
	if err != nil {
		t.Fatalf("ConvertLegacy: %v", err)
	}
	title := areaByName(lt, "Заголовок")
	if title == nil {
		t.Fatal("нет области Заголовок")
		return
	}
	if len(title.Rows) != 1 || len(title.Rows[0].Cells) != 1 {
		t.Fatalf("Заголовок должен быть 1 ячейка, got rows=%d", len(title.Rows))
	}
	c := title.Rows[0].Cells[0]
	if !c.Bold {
		t.Error("заголовок не bold")
	}
	if c.Align != "center" {
		t.Errorf("заголовок align=%q, ожидался center", c.Align)
	}
	if c.Text != "Счёт-фактура № {{Номер}} от {{Дата | date}}" {
		t.Errorf("текст заголовка изменён: %q", c.Text)
	}
	// colspan на всю сетку (5 колонок таблицы).
	if c.ColSpan != 5 {
		t.Errorf("colspan заголовка = %d, ожидался 5 (ширина сетки)", c.ColSpan)
	}
	if c.FontSize <= 10 {
		t.Errorf("fontSize заголовка = %d, ожидался крупнее", c.FontSize)
	}
}

func TestConvertLegacy_Columns(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	if len(lt.Columns) != 5 {
		t.Fatalf("columns = %d, ожидалось 5", len(lt.Columns))
	}
	if lt.Columns[0].Width != "40px" {
		t.Errorf("ширина колонки 0 = %q, ожидалось 40px", lt.Columns[0].Width)
	}
}

func TestConvertLegacy_Header(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	h := areaByName(lt, "Шапка")
	if h == nil {
		t.Fatal("нет области Шапка")
		return
	}
	// **Продавец**... → bold-строка, пустая, **Покупатель**... → bold-строка.
	// Каждая строка — одна ячейка colspan на ширину.
	found := false
	for _, row := range h.Rows {
		if len(row.Cells) != 1 {
			t.Fatalf("строка шапки должна иметь 1 ячейку, got %d", len(row.Cells))
		}
		c := row.Cells[0]
		if c.ColSpan != 5 {
			t.Errorf("ячейка шапки colspan=%d, ожидался 5", c.ColSpan)
		}
		if strings.Contains(c.Text, "Продавец") {
			found = true
			// смешанный inline-markdown (**Продавец**: текст) → маркеры удалены.
			if strings.Contains(c.Text, "**") {
				t.Errorf("маркеры ** не удалены: %q", c.Text)
			}
			if !strings.Contains(c.Text, "{{Организация.Наименование}}") {
				t.Errorf("интерполяция потеряна: %q", c.Text)
			}
		}
	}
	if !found {
		t.Error("строка с Продавцом не найдена в Шапке")
	}
}

func TestConvertLegacy_TableAreas(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())

	thead := areaByName(lt, "ШапкаТаблицы")
	if thead == nil {
		t.Fatal("нет области ШапкаТаблицы")
		return
	}
	if len(thead.Rows) != 1 || len(thead.Rows[0].Cells) != 5 {
		t.Fatalf("ШапкаТаблицы: ожидалось 5 ячеек-меток, got rows=%d", len(thead.Rows))
	}
	for i, c := range thead.Rows[0].Cells {
		if !c.Bold {
			t.Errorf("метка колонки %d не bold", i)
		}
		if c.Border != "all" {
			t.Errorf("метка колонки %d border=%q, ожидался all", i, c.Border)
		}
	}
	if thead.Rows[0].Cells[1].Text != "Наименование" {
		t.Errorf("метка колонки 1 = %q", thead.Rows[0].Cells[1].Text)
	}

	rowArea := areaByName(lt, "Строка")
	if rowArea == nil {
		t.Fatal("нет области Строка")
		return
	}
	if len(rowArea.Rows) != 1 || len(rowArea.Rows[0].Cells) != 5 {
		t.Fatalf("Строка: ожидалось 5 ячеек-параметров, got rows=%d", len(rowArea.Rows))
	}
	for i, c := range rowArea.Rows[0].Cells {
		if c.Parameter == "" {
			t.Errorf("ячейка строки %d без параметра", i)
		}
		if c.Border != "all" {
			t.Errorf("ячейка строки %d border=%q, ожидался all", i, c.Border)
		}
	}
	// align колонки «Количество» (right) переносится в ячейку.
	if rowArea.Rows[0].Cells[2].Align != "right" {
		t.Errorf("ячейка Количество align=%q, ожидался right", rowArea.Rows[0].Cells[2].Align)
	}
}

func TestConvertLegacy_Totals(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	totals := areaByName(lt, "Итоги")
	if totals == nil {
		t.Fatal("нет области Итоги")
		return
	}
	// Итог с меткой («Итого») → интерполируемый текст с Итог.Товары.Сумма,
	// сохраняющий метку (как в legacy «Итого: 650.00»).
	found := false
	for _, row := range totals.Rows {
		for _, c := range row.Cells {
			if strings.Contains(c.Text, "Итог.Товары.Сумма") && strings.Contains(c.Text, "Итого") {
				found = true
			}
		}
	}
	if !found {
		t.Error("в Итогах нет ячейки с меткой и выражением Итог.Товары.Сумма")
	}
}

func TestConvertLegacy_Binding(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	if lt.Binding == nil {
		t.Fatal("binding nil")
	}
	if len(lt.Binding.Repeat) != 1 {
		t.Fatalf("repeat = %d, ожидался 1", len(lt.Binding.Repeat))
	}
	rb := lt.Binding.Repeat[0]
	if !strings.EqualFold(rb.Area, "Строка") {
		t.Errorf("repeat.area = %q, ожидался Строка", rb.Area)
	}
	if rb.Source != "Товары" {
		t.Errorf("repeat.source = %q, ожидался Товары", rb.Source)
	}
	// @row колонка → field "@row".
	if rb.Parameters == nil {
		t.Fatal("repeat.parameters nil")
	}
	// Параметр для ссылочной колонки должен нести выражение Номенклатура.Наименование.
	foundRef := false
	foundFmt := false
	for _, expr := range rb.Parameters {
		if strings.Contains(expr, "Номенклатура.Наименование") {
			foundRef = true
		}
		if strings.Contains(expr, "number:2") {
			foundFmt = true
		}
	}
	if !foundRef {
		t.Error("параметр ссылочной колонки потерян")
	}
	if !foundFmt {
		t.Error("формат number:2 потерян в параметрах")
	}

	// sequence: Заголовок, Шапка, ШапкаТаблицы, Строка, Итоги.
	seq := lt.Binding.Sequence
	if len(seq) == 0 {
		t.Fatal("sequence пуст")
	}
	if !strings.EqualFold(seq[0], "Заголовок") {
		t.Errorf("первая область sequence = %q, ожидался Заголовок", seq[0])
	}
}

// TestConvertLegacy_MoneyMappedToNumber проверяет, что нереализованный money
// замаплен на number:2 (с разделителем тысяч).
func TestConvertLegacy_MoneyMappedToNumber(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	footer := areaByName(lt, "Подвал")
	if footer == nil {
		t.Fatal("нет области Подвал")
		return
	}
	var allText string
	for _, row := range footer.Rows {
		for _, c := range row.Cells {
			allText += c.Text + "\n"
		}
	}
	if strings.Contains(allText, "| money") || strings.Contains(allText, "|money") {
		t.Errorf("money не замаплен: %q", allText)
	}
	if !strings.Contains(allText, "number:2") {
		t.Errorf("money не превратился в number:2: %q", allText)
	}
}

// TestConvertLegacy_FooterHr проверяет, что строка-разделитель --- даёт нижнюю границу.
func TestConvertLegacy_FooterHr(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	footer := areaByName(lt, "Подвал")
	if footer == nil {
		t.Fatal("нет области Подвал")
		return
	}
	foundBorder := false
	for _, row := range footer.Rows {
		for _, c := range row.Cells {
			if c.Borders != nil && c.Borders.Bottom != "" {
				foundBorder = true
			}
		}
	}
	if !foundBorder {
		t.Error("--- не дал нижнюю границу в подвале")
	}
}

// TestConvertLegacy_RendersViaBuildSheet — сквозная проверка: конвертируем форму
// и рендерим декларативным движком против синтетических данных. Проверяем
// смысловые маркеры: заголовок, метки колонок, наименование из ссылки, итог
// (который в legacy был пуст — теперь работает).
func TestConvertLegacy_RendersViaBuildSheet(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	ctx := &RenderContext{
		Document: map[string]any{
			"Номер":       "42",
			"Организация": "ref-org",
			"Покупатель":  "ref-buyer",
		},
		Refs: map[string]map[string]any{
			// Подполя (Организация.Наименование) резолвятся точным ключом поля.
			"ref-org":   {"Наименование": "ООО Продавец", "наименование": "ООО Продавец"},
			"ref-buyer": {"Наименование": "ИП Иванов", "наименование": "ИП Иванов"},
			"ref-g1":    {"Наименование": "Стол", "наименование": "Стол"},
			"ref-g2":    {"Наименование": "Стул", "наименование": "Стул"},
		},
		TableParts: map[string][]map[string]any{
			"Товары": {
				{"Номенклатура": "ref-g1", "Количество": 2.0, "Цена": 100.0, "Сумма": 200.0},
				{"Номенклатура": "ref-g2", "Количество": 3.0, "Цена": 150.0, "Сумма": 450.0},
			},
		},
	}
	doc, err := BuildSheet(lt, ctx)
	if err != nil {
		t.Fatalf("BuildSheet: %v", err)
	}
	html := doc.HTML(sheet.HTMLOptions{})

	wantTexts := []string{
		"Счёт-фактура № 42", // заголовок интерполирован
		"Наименование",      // метка колонки
		"ООО Продавец",      // ссылка в шапке резолвится
		"Стол",              // ссылка в строке резолвится
		"Стул",
		"650.00", // ИТОГ по сумме (200+450) — в legacy footer был пуст
	}
	for _, w := range wantTexts {
		if !strings.Contains(html, w) {
			t.Errorf("в HTML нет ожидаемого текста %q", w)
		}
	}
}

// TestConvertLegacy_MarshalRoundTrip — конвертированный макет сериализуется в
// YAML и парсится обратно без потери областей/binding (нужно для migrate).
func TestConvertLegacy_MarshalRoundTrip(t *testing.T) {
	lt, _ := ConvertLegacy(repForm())
	data, err := yaml.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "areas:") {
		t.Errorf("в YAML нет top-level areas:\n%s", data)
	}
	lt2, err := ParseLayoutBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lt2.Areas) != len(lt.Areas) {
		t.Errorf("areas после round-trip: %d, было %d", len(lt2.Areas), len(lt.Areas))
	}
	if lt2.Binding == nil || len(lt2.Binding.Repeat) != 1 {
		t.Fatalf("binding потерян после round-trip")
	}
	if lt2.Binding.Repeat[0].Source != "Товары" {
		t.Errorf("source после round-trip = %q", lt2.Binding.Repeat[0].Source)
	}
}

// TestConvertLegacy_NilTable: форма без таблицы конвертируется без паники.
func TestConvertLegacy_NilTable(t *testing.T) {
	pf := &PrintForm{Name: "Простая", Document: "Док", Title: "Заголовок"}
	lt, err := ConvertLegacy(pf)
	if err != nil {
		t.Fatalf("ConvertLegacy без таблицы: %v", err)
	}
	if lt.Area("Строка") != nil {
		t.Error("без таблицы не должно быть области Строка")
	}
	if lt.Document != "Док" {
		t.Errorf("document = %q", lt.Document)
	}
	if lt.Name != "Простая" {
		t.Errorf("name = %q", lt.Name)
	}
}
