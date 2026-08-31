package xlsximport

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
)

// Бланк для тестов собирается excelize прямо здесь: бинарный фикстур в репозитории
// нечитаем в ревью и меняется только целиком, а построение в коде показывает, что
// именно проверяется.

type blankOpts struct {
	// WithNames — разметить области именами Excel вместо автоматики.
	WithNames bool
	// WithPrintTitles — задать «сквозные строки» листа.
	WithPrintTitles bool
	// NoTableTags — не размечать строку тегами табличной части.
	NoTableTags bool
}

// buildBlank рисует накладную: заголовок, шапка, шапка таблицы, строка ТЧ, итог,
// подпись. Раскладка повторяет типовой бланк — на нём и проверяется импорт.
func buildBlank(t *testing.T, opts blankOpts) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sh := f.GetSheetName(0)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("подготовка бланка: %v", err)
		}
	}

	title, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 14},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	must(err)
	boxed, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Border: []excelize.Border{
			{Type: "left", Style: 1}, {Type: "top", Style: 1},
			{Type: "right", Style: 1}, {Type: "bottom", Style: 1},
		},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9E1F2"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	must(err)
	cellBox, err := f.NewStyle(&excelize.Style{
		Border: []excelize.Border{
			{Type: "left", Style: 1}, {Type: "top", Style: 1},
			{Type: "right", Style: 1}, {Type: "bottom", Style: 1},
		},
	})
	must(err)

	// 1 — заголовок на всю ширину.
	must(f.SetCellValue(sh, "A1", "Накладная № {{Номер}} от {{Дата | date}}"))
	must(f.MergeCell(sh, "A1", "D1"))
	must(f.SetCellStyle(sh, "A1", "D1", title))
	must(f.SetRowHeight(sh, 1, 30))

	// 2 — шапка документа.
	must(f.SetCellValue(sh, "A2", "Покупатель: {{Контрагент.Наименование}}"))
	must(f.MergeCell(sh, "A2", "D2"))

	// 3 — шапка таблицы.
	for cell, text := range map[string]string{"A3": "№", "B3": "Товар", "C3": "Кол-во", "D3": "Цена"} {
		must(f.SetCellValue(sh, cell, text))
	}
	must(f.SetCellStyle(sh, "A3", "D3", boxed))

	// 4 — строка табличной части.
	row := map[string]string{
		"A4": "{{@row}}",
		"B4": "{{Товары.Номенклатура}}",
		"C4": "{{Товары.Количество}}",
		"D4": "{{Товары.Цена | number:2}}",
	}
	if opts.NoTableTags {
		row["B4"] = "{{Номенклатура}}"
		row["C4"] = "{{Количество}}"
		row["D4"] = "{{Цена}}"
	}
	for cell, text := range row {
		must(f.SetCellValue(sh, cell, text))
	}
	must(f.SetCellStyle(sh, "A4", "D4", cellBox))

	// 5 — итог.
	must(f.SetCellValue(sh, "C5", "Итого:"))
	must(f.SetCellValue(sh, "D5", "{{Итог.Товары.Сумма | number:2}}"))

	// 6 — подпись.
	must(f.SetCellValue(sh, "A6", "Отпустил ________________"))
	must(f.MergeCell(sh, "A6", "D6"))

	must(f.SetColWidth(sh, "A", "A", 5))
	must(f.SetColWidth(sh, "B", "B", 40))
	must(f.SetPageLayout(sh, &excelize.PageLayoutOptions{
		Orientation: ptr("landscape"),
		Size:        ptr(11), // A5
	}))
	must(f.SetPageMargins(sh, &excelize.PageLayoutMarginsOptions{
		Left: ptrF(0.5), Right: ptrF(0.5), Top: ptrF(1.0), Bottom: ptrF(1.0),
	}))

	if opts.WithNames {
		must(f.SetDefinedName(&excelize.DefinedName{Name: "Шапка", RefersTo: sh + "!$A$1:$D$2"}))
		must(f.SetDefinedName(&excelize.DefinedName{Name: "Таблица", RefersTo: sh + "!$A$3:$D$3"}))
		must(f.SetDefinedName(&excelize.DefinedName{Name: "ПозицияТовара", RefersTo: sh + "!$A$4:$D$4"}))
	}
	if opts.WithPrintTitles {
		must(f.SetDefinedName(&excelize.DefinedName{Name: "_xlnm.Print_Titles", RefersTo: sh + "!$1:$3"}))
	}

	buf, err := f.WriteToBuffer()
	must(err)
	return buf.Bytes()
}

func ptr[T any](v T) *T       { return &v }
func ptrF(v float64) *float64 { return &v }

func importBlank(t *testing.T, opts blankOpts, tps ...string) *Result {
	t.Helper()
	res, err := ImportBytes(buildBlank(t, opts), Options{TableParts: tps})
	if err != nil {
		t.Fatalf("ImportBytes: %v", err)
	}
	return res
}

// areaByName ищет область макета по имени.
func areaByName(lt *printform.LayoutTemplate, name string) *printform.LayoutArea {
	for _, a := range lt.Areas {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func TestImportBlank_Structure(t *testing.T) {
	lt := importBlank(t, blankOpts{}, "Товары").Layout

	if got := len(lt.Columns); got != 4 {
		t.Fatalf("колонок: got %d, want 4", got)
	}
	if lt.Columns[0].Width == "" || lt.Columns[0].Width == lt.Columns[1].Width {
		t.Errorf("ширины колонок не перенесены: %+v", lt.Columns)
	}

	want := []string{"Шапка", "ШапкаТаблицы", "Строка", "Подвал"}
	var got []string
	for _, a := range lt.Areas {
		got = append(got, a.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("области: got %v, want %v", got, want)
	}
	if strings.Join(lt.Binding.Sequence, ",") != strings.Join(want, ",") {
		t.Errorf("sequence: got %v, want %v", lt.Binding.Sequence, want)
	}
}

func TestImportBlank_RepeatFromTags(t *testing.T) {
	lt := importBlank(t, blankOpts{}, "Товары").Layout

	if len(lt.Binding.Repeat) != 1 {
		t.Fatalf("repeat: got %+v, want одну привязку", lt.Binding.Repeat)
	}
	rb := lt.Binding.Repeat[0]
	if rb.Area != "Строка" || rb.Source != "Товары" {
		t.Errorf("repeat: got %+v, want {Строка Товары}", rb)
	}
	if lt.Binding.RepeatHeader != "ШапкаТаблицы" {
		t.Errorf("repeat_header: got %q, want ШапкаТаблицы", lt.Binding.RepeatHeader)
	}

	// Внутри repeat-области приставка ТЧ снимается: выражением колонки является
	// её голое имя (иначе binding.go искал бы поле «Товары» в строке).
	row := areaByName(lt, "Строка")
	if row == nil || len(row.Rows) != 1 {
		t.Fatalf("область Строка: %+v", row)
	}
	var texts []string
	for _, c := range row.Rows[0].Cells {
		texts = append(texts, c.Text)
	}
	want := []string{"{{@row}}", "{{Номенклатура}}", "{{Количество}}", "{{Цена | number:2}}"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("теги строки ТЧ: got %v, want %v", texts, want)
	}

	// Итог.<ТЧ>.<Поле> — конструкция языка выражений, а не колонка: приставку
	// «Итог» за имя табличной части принимать нельзя.
	footer := areaByName(lt, "Подвал")
	if footer == nil || !strings.Contains(cellsText(footer), "{{Итог.Товары.Сумма | number:2}}") {
		t.Errorf("итог испорчен: %q", cellsText(footer))
	}
}

func cellsText(a *printform.LayoutArea) string {
	var sb strings.Builder
	for _, r := range a.Rows {
		for _, c := range r.Cells {
			sb.WriteString(c.Text)
			sb.WriteString(" ")
		}
	}
	return sb.String()
}

// Без списка табличных частей разворот не делается: {{Товары.Номенклатура}} и
// {{Склад.Наименование}} по написанию неразличимы, и угадывать нельзя.
func TestImport_NoTablePartsKeepsFlatLayout(t *testing.T) {
	res := importBlank(t, blankOpts{})
	if len(res.Layout.Binding.Repeat) != 0 {
		t.Errorf("repeat без списка ТЧ: got %+v, want пусто", res.Layout.Binding.Repeat)
	}
	if !hasWarning(res, "Табличные части документа не заданы") {
		t.Errorf("нет предупреждения о ненайденных ТЧ: %v", res.Warnings)
	}
	// Теги остались как были — их никто не переписывал.
	if !strings.Contains(cellsText(res.Layout.Areas[0]), "{{Номер}}") {
		t.Errorf("шапка испорчена: %q", cellsText(res.Layout.Areas[0]))
	}
}

// Табличная часть известна, но строка не помечена — импорт обязан сказать об
// этом, иначе пользователь получит бланк с одной строкой и не поймёт почему.
func TestImport_TablePartWithoutTaggedRowWarns(t *testing.T) {
	res := importBlank(t, blankOpts{NoTableTags: true}, "Товары")
	if len(res.Layout.Binding.Repeat) != 0 {
		t.Errorf("repeat: got %+v, want пусто", res.Layout.Binding.Repeat)
	}
	if !hasWarning(res, "Строк табличной части не найдено") {
		t.Errorf("нет предупреждения: %v", res.Warnings)
	}
}

func TestImportBlank_Styles(t *testing.T) {
	lt := importBlank(t, blankOpts{}, "Товары").Layout

	head := areaByName(lt, "Шапка")
	if head == nil {
		t.Fatal("нет области Шапка")
	}
	title := head.Rows[0].Cells[0]
	if !title.Bold || title.Align != "center" || title.FontSize != 14 {
		t.Errorf("оформление заголовка: %+v", title)
	}
	if title.ColSpan != 4 {
		t.Errorf("объединение заголовка: colspan=%d, want 4", title.ColSpan)
	}
	if title.RowSpan != 0 {
		t.Errorf("rowspan=1 не должен попадать в YAML: %d", title.RowSpan)
	}
	if head.Rows[0].Height == "" {
		t.Errorf("высота строки не перенесена")
	}

	th := areaByName(lt, "ШапкаТаблицы")
	if th == nil {
		t.Fatal("нет области ШапкаТаблицы")
	}
	cell := th.Rows[0].Cells[0]
	if cell.Borders == nil || cell.Borders.Left != "thin" || cell.Borders.Bottom != "thin" {
		t.Errorf("границы шапки таблицы: %+v", cell.Borders)
	}
	if cell.BackColor != "#D9E1F2" {
		t.Errorf("заливка: got %q, want #D9E1F2", cell.BackColor)
	}
	if cell.VAlign != "middle" {
		t.Errorf("valign: got %q, want middle (в CSS середина — middle, не center)", cell.VAlign)
	}
}

func TestImportBlank_Page(t *testing.T) {
	lt := importBlank(t, blankOpts{}, "Товары").Layout
	if lt.Page == nil {
		t.Fatal("параметры страницы не перенесены")
	}
	if lt.Page.Orientation != "landscape" || lt.Page.Format != "A5" {
		t.Errorf("страница: got %+v, want landscape/A5", *lt.Page)
	}
	if got := lt.Page.MarginsMM.Left; got < 12.6 || got > 12.8 {
		t.Errorf("левое поле: got %v мм, want ≈12.7 (0.5 дюйма)", got)
	}
	if got := lt.Page.MarginsMM.Top; got < 25.3 || got > 25.5 {
		t.Errorf("верхнее поле: got %v мм, want ≈25.4 (1 дюйм)", got)
	}
}

func TestImportWideScaledSheetAndIgnoresNamedCells(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	sh := f.GetSheetName(0)
	if err := f.SetCellValue(sh, "A1", "{{Номер}}"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue(sh, "CJ1", "край"); err != nil { // CJ = 88-я колонка
		t.Fatal(err)
	}
	if err := f.SetColWidth(sh, "A", "CJ", 10); err != nil {
		t.Fatal(err)
	}
	if err := f.SetPageLayout(sh, &excelize.PageLayoutOptions{AdjustTo: ptr(uint(60))}); err != nil {
		t.Fatal(err)
	}
	for name, ref := range map[string]string{
		"ПРОДАВЕЦ_НАИМ": "$A$1",
		"ПРОДАВЕЦ_ИНН":  "$B$1",
		"ПОКУПАТЕЛЬ":    "$C$1",
		"TABLE":         "$A$1:$F$1",
	} {
		if err := f.SetDefinedName(&excelize.DefinedName{Name: name, RefersTo: sh + "!" + ref}); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportBytes(buf.Bytes(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.Layout.Columns); got != 88 {
		t.Fatalf("columns = %d, want 88", got)
	}
	if got := res.Layout.Columns[0].Width; got != "45px" {
		t.Errorf("scaled width = %q, want 45px", got)
	}
	for _, area := range res.Layout.Areas {
		if area.Name == "ПРОДАВЕЦ_НАИМ" || area.Name == "TABLE" {
			t.Errorf("workbook field %q became a layout area", area.Name)
		}
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "пользовательские именованные") {
		t.Errorf("missing named-cell warning: %v", res.Warnings)
	}
}

// Имена Excel — явная разметка, она перебивает автоматику Шапка/Строка/Подвал.
func TestImport_DefinedNamesOverrideAutoAreas(t *testing.T) {
	lt := importBlank(t, blankOpts{WithNames: true}, "Товары").Layout

	for _, name := range []string{"Шапка", "Таблица", "ПозицияТовара"} {
		if areaByName(lt, name) == nil {
			t.Errorf("нет области %q; есть: %v", name, lt.Binding.Sequence)
		}
	}
	if len(lt.Binding.Repeat) != 1 || lt.Binding.Repeat[0].Area != "ПозицияТовара" {
		t.Errorf("repeat по именованной области: got %+v", lt.Binding.Repeat)
	}
	// Разметку задал человек — эвристику «шапка таблицы» не применяем.
	if lt.Binding.RepeatHeader != "" {
		t.Errorf("repeat_header при явных именах: got %q, want пусто", lt.Binding.RepeatHeader)
	}
}

// «Сквозные строки» Excel — это ровно binding.repeat_header, гадать не нужно.
func TestImport_PrintTitlesBecomeRepeatHeader(t *testing.T) {
	lt := importBlank(t, blankOpts{WithNames: true, WithPrintTitles: true}, "Товары").Layout
	if lt.Binding.RepeatHeader == "" {
		t.Fatalf("сквозные строки не стали repeat_header: %+v", lt.Binding)
	}
	if area := areaByName(lt, lt.Binding.RepeatHeader); area == nil {
		t.Errorf("repeat_header ссылается на несуществующую область %q", lt.Binding.RepeatHeader)
	}
}

func TestImport_Errors(t *testing.T) {
	if _, err := ImportBytes(nil, Options{}); !errors.Is(err, ErrParse) {
		t.Errorf("пустой ввод: got %v, want ErrParse", err)
	}
	if _, err := ImportBytes([]byte("это не xlsx"), Options{}); !errors.Is(err, ErrParse) {
		t.Errorf("мусор: got %v, want ErrParse", err)
	}
	if _, err := ImportBytes(bytes.Repeat([]byte{0}, MaxFileSize+1), Options{}); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("большой файл: got %v, want ErrFileTooLarge", err)
	}
	if _, err := ImportBytes(buildBlank(t, blankOpts{}), Options{Sheet: "Нетути"}); !errors.Is(err, ErrSheetNotFound) {
		t.Errorf("чужой лист: got %v, want ErrSheetNotFound", err)
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	empty, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("пустая книга: %v", err)
	}
	if _, err := ImportBytes(empty.Bytes(), Options{}); !errors.Is(err, ErrEmptySheet) {
		t.Errorf("пустой лист: got %v, want ErrEmptySheet", err)
	}
}

func hasWarning(res *Result, substr string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
