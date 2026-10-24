package xlsxtemplate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

func TestRenderBytesPreservesWorkbookAndRepeatsRows(t *testing.T) {
	f := excelize.NewFile()
	sheet := "Sheet1"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetCellStr(sheet, "A1", "{{Контрагент.Наименование}}"))
	must(f.SetCellStr(sheet, "B1", "ИНН {{ИНН}}"))
	must(f.SetCellStr(sheet, "C1", "{{Сумма}}"))
	must(f.SetCellStr(sheet, "A3", "{{Товары.Наименование}}"))
	must(f.SetCellStr(sheet, "B3", "{{Товары.Количество}}"))
	must(f.SetCellStr(sheet, "D3", "строка {{@row}}"))
	must(f.MergeCell(sheet, "D3", "E3"))
	must(f.SetCellStr(sheet, "A4", "Итого: {{Итог.Товары.Количество | number:0}}"))
	must(f.SetColWidth(sheet, "A", "A", 24))
	style, err := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFFF00"}, Pattern: 1}})
	must(err)
	must(f.SetCellStyle(sheet, "A3", "E3", style))
	must(f.SetDefinedName(&excelize.DefinedName{Name: "OB_Строка", RefersTo: "Sheet1!$A$3:$E$3"}))

	var input bytes.Buffer
	must(f.Write(&input))
	must(f.Close())

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		EntityName: "Контрагент",
		Document: map[string]any{
			"Наименование": "ООО Ромашка",
			"ИНН":          "7701000000",
			"Сумма":        1250.5,
		},
		TableParts: map[string][]map[string]any{
			"Товары": {
				{"Наименование": "Бумага", "Количество": 2},
				{"Наименование": "Ручка", "Количество": 3},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			t.Errorf("Close XLSX: %v", err)
		}
	}()
	assertCell(t, out, sheet, "A1", "ООО Ромашка")
	assertCell(t, out, sheet, "B1", "ИНН 7701000000")
	assertCell(t, out, sheet, "C1", "1250.5")
	assertCell(t, out, sheet, "A3", "Бумага")
	assertCell(t, out, sheet, "B3", "2")
	assertCell(t, out, sheet, "D3", "строка 1")
	assertCell(t, out, sheet, "A4", "Ручка")
	assertCell(t, out, sheet, "B4", "3")
	assertCell(t, out, sheet, "D4", "строка 2")
	assertCell(t, out, sheet, "A5", "Итого: 5")
	if typ, err := out.GetCellType(sheet, "C1"); err != nil || (typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset) {
		t.Errorf("C1 type = %v, %v; want number", typ, err)
	}
	if got, _ := out.GetCellStyle(sheet, "A4"); got != style {
		t.Errorf("duplicated style = %d, want %d", got, style)
	}
	merges, err := out.GetMergeCells(sheet)
	if err != nil || len(merges) != 2 || merges[1].GetStartAxis() != "D4" || merges[1].GetEndAxis() != "E4" {
		t.Errorf("duplicated merges = %+v, err=%v", merges, err)
	}
	if width, err := out.GetColWidth(sheet, "A"); err != nil || width != 24 {
		t.Errorf("column width = %v, %v; want 24", width, err)
	}
	if len(out.GetDefinedName()) == 0 {
		t.Error("defined names were lost")
	}
}

func assertCell(t *testing.T, f *excelize.File, sheet, axis, want string) {
	t.Helper()
	got, err := f.GetCellValue(sheet, axis)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", axis, got, want)
	}
}

func TestRenderBytesEmptyTablePartLeavesBlankTemplateRow(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "{{Строки.Значение}}"); err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	if err := f.Write(&input); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		TableParts: map[string][]map[string]any{"Строки": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			t.Errorf("Close XLSX: %v", err)
		}
	}()
	assertCell(t, out, "Sheet1", "A1", "")
}

func TestRenderBytesRepeatKeepsRowFirstForEntityNamedReference(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "{{Строки.Контрагент.Наименование}}"); err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	if err := f.Write(&input); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		EntityName: "Контрагент",
		Document: map[string]any{
			"Наименование": "Корневая запись",
		},
		TableParts: map[string][]map[string]any{
			"Строки": {{"Контрагент": "row-ref"}},
		},
		Refs: map[string]map[string]any{
			"row-ref": {"Наименование": "Ссылка из строки"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			t.Errorf("Close XLSX: %v", err)
		}
	}()
	assertCell(t, out, "Sheet1", "A1", "Ссылка из строки")
}

// Числа из хранилища (decimal.Decimal) и теги с форматом number:N / currency
// должны попадать в книгу числом: иначе Excel помечает ячейки «число сохранено
// как текст» и не суммирует их формулой (обсуждение #1109).
func TestRenderBytesWritesNumericTagsAsNumbers(t *testing.T) {
	f := excelize.NewFile()
	sheet := "Sheet1"
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SetCellStr(sheet, "A1", "{{Сумма}}"))
	must(f.SetCellStr(sheet, "B1", "{{Сумма | number:2}}"))
	must(f.SetCellStr(sheet, "C1", "{{Сумма | currency}}"))
	must(f.SetCellStr(sheet, "D1", "{{Сумма | number:0}}"))
	must(f.SetCellStr(sheet, "E1", "{{Сумма | number:2}}"))
	must(f.SetCellStr(sheet, "F1", "{{ИНН}}"))
	must(f.SetCellStr(sheet, "G1", "{{ИНН | number:0}}"))
	must(f.SetCellStr(sheet, "H1", "Итого: {{Сумма | number:2}}"))
	must(f.SetCellStr(sheet, "I1", "{{Итог.Товары.Сумма | number:2}}"))
	must(f.SetCellStr(sheet, "A3", "{{Товары.Количество}}"))
	must(f.SetCellStr(sheet, "B3", "{{Товары.Сумма | number:2}}"))
	custom := "#,##0.00\" руб.\""
	customStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &custom})
	must(err)
	must(f.SetCellStyle(sheet, "E1", "E1", customStyle))

	var input bytes.Buffer
	must(f.Write(&input))
	must(f.Close())

	result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
		Document: map[string]any{
			"Сумма": decimal.RequireFromString("1250.456"),
			"ИНН":   "0012345678",
		},
		TableParts: map[string][]map[string]any{
			"Товары": {
				{"Количество": decimal.RequireFromString("2"), "Сумма": decimal.RequireFromString("100.5")},
				{"Количество": decimal.RequireFromString("3"), "Сумма": decimal.RequireFromString("200.25")},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := excelize.OpenReader(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := out.Close(); err != nil {
			t.Errorf("Close XLSX: %v", err)
		}
	}()

	for axis, want := range map[string]string{
		"A1": "1250.456", "B1": "1250.46", "C1": "1250.46", "D1": "1250", "E1": "1250.46",
		"I1": "300.75", "A3": "2", "B3": "100.5", "A4": "3", "B4": "200.25",
	} {
		assertNumberCell(t, out, sheet, axis, want)
	}
	for axis, want := range map[string]string{
		"F1": "0012345678", "G1": "12345678", "H1": "Итого: 1250.46",
	} {
		assertTextCell(t, out, sheet, axis, want)
	}

	// Формат отображения: без своего формата в шаблоне — столько знаков,
	// сколько просили в теге; свой формат автора бланка не перетирается.
	assertNumFmt(t, out, sheet, "B1", "0.00")
	assertNumFmt(t, out, sheet, "D1", "0")
	assertNumFmt(t, out, sheet, "E1", custom)
	if got, _ := out.GetCellValue(sheet, "B1"); got != "1250.46" {
		t.Errorf("B1 formatted = %q, want 1250.46", got)
	}
}

func assertNumberCell(t *testing.T, f *excelize.File, sheet, axis, want string) {
	t.Helper()
	typ, err := f.GetCellType(sheet, axis)
	if err != nil {
		t.Fatal(err)
	}
	if typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
		t.Errorf("%s type = %v, want number", axis, typ)
	}
	got, err := f.GetCellValue(sheet, axis, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s raw = %q, want %q", axis, got, want)
	}
}

func assertTextCell(t *testing.T, f *excelize.File, sheet, axis, want string) {
	t.Helper()
	typ, err := f.GetCellType(sheet, axis)
	if err != nil {
		t.Fatal(err)
	}
	if typ != excelize.CellTypeSharedString && typ != excelize.CellTypeInlineString {
		t.Errorf("%s type = %v, want text", axis, typ)
	}
	assertCell(t, f, sheet, axis, want)
}

func assertNumFmt(t *testing.T, f *excelize.File, sheet, axis, want string) {
	t.Helper()
	id, err := f.GetCellStyle(sheet, axis)
	if err != nil {
		t.Fatal(err)
	}
	style, err := f.GetStyle(id)
	if err != nil {
		t.Fatal(err)
	}
	if style.CustomNumFmt == nil || *style.CustomNumFmt != want {
		got := "<nil>"
		if style.CustomNumFmt != nil {
			got = *style.CustomNumFmt
		}
		t.Errorf("%s number format = %q (NumFmt %d), want %q", axis, got, style.NumFmt, want)
	}
}

// Точность недоверенного тега ограничивается до округления и создания стиля.
func TestRenderBytesBoundsNumericPrecision(t *testing.T) {
	for _, tc := range []struct {
		name         string
		precision    string
		wantDecimals int
	}{
		{"zero", "0", 0},
		{"maximum", "30", 30},
		{"above maximum", "31", 30},
		{"int32 maximum", "2147483647", 30},
		{"negative", "-1", 0},
		{"invalid", "invalid", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := excelize.NewFile()
			if err := f.SetCellStr("Sheet1", "A1", "{{Amount | number:"+tc.precision+"}}"); err != nil {
				t.Fatal(err)
			}
			// Свой формат шаблона не отменяет проверку перед округлением.
			if err := f.SetCellStr("Sheet1", "B1", "{{Amount | number:"+tc.precision+"}}"); err != nil {
				t.Fatal(err)
			}
			custom := "0.00"
			style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &custom})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStyle("Sheet1", "B1", "B1", style); err != nil {
				t.Fatal(err)
			}
			var input bytes.Buffer
			if err := f.Write(&input); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
				Document: map[string]any{"Amount": decimal.RequireFromString("1.25")},
			})
			if err != nil {
				t.Fatal(err)
			}
			out, err := excelize.OpenReader(bytes.NewReader(result))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := out.Close(); err != nil {
					t.Error(err)
				}
			}()
			wantValue := "1.25"
			code := "0"
			if tc.wantDecimals == 0 {
				wantValue = "1"
			} else {
				code += "." + strings.Repeat("0", tc.wantDecimals)
			}
			assertNumberCell(t, out, "Sheet1", "A1", wantValue)
			assertNumberCell(t, out, "Sheet1", "B1", wantValue)
			assertNumFmt(t, out, "Sheet1", "A1", code)
			assertNumFmt(t, out, "Sheet1", "B1", custom)
		})
	}
}

// Одинаковый тег округляется одинаково с текстом вокруг него и без текста.
// Полноячеечный тег при этом остаётся числом для формул Excel.
func TestRenderBytesNumericRoundingMatchesMixedText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		format string
		want   string
	}{
		{"decimal number", decimal.RequireFromString("2.675"), "number:2", "2.67"},
		{"decimal currency", decimal.RequireFromString("2.675"), "currency", "2.67"},
		{"negative number", decimal.RequireFromString("-2.675"), "number:2", "-2.67"},
		{"negative currency", decimal.RequireFromString("-2.675"), "currency", "-2.67"},
		{"float number", 2.675, "number:2", "2.67"},
		{"half even", 2.625, "number:2", "2.62"},
		{"half away positive", 2.5, "number:0", "3"},
		{"half away negative", -2.5, "number:0", "-3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := excelize.NewFile()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			for axis, expr := range map[string]string{
				"A1": "Amount", "A2": "Итог.Items.Amount", "A3": "Items.Amount",
			} {
				tag := "{{" + expr + " | " + tc.format + "}}"
				must(f.SetCellStr("Sheet1", axis, tag))
				must(f.SetCellStr("Sheet1", "B"+axis[1:], "Total: "+tag))
			}
			must(f.SetCellFormula("Sheet1", "C1", "SUM(A1:A1)"))
			var input bytes.Buffer
			must(f.Write(&input))
			must(f.Close())
			result, err := RenderBytes(input.Bytes(), &printform.RenderContext{
				Document:   map[string]any{"Amount": tc.value},
				TableParts: map[string][]map[string]any{"Items": {{"Amount": tc.value}}},
			})
			must(err)
			out, err := excelize.OpenReader(bytes.NewReader(result))
			must(err)
			defer func() { must(out.Close()) }()
			for _, row := range []string{"1", "2", "3"} {
				assertNumberCell(t, out, "Sheet1", "A"+row, tc.want)
				assertTextCell(t, out, "Sheet1", "B"+row, "Total: "+tc.want)
				assertCell(t, out, "Sheet1", "A"+row, tc.want)
			}
			got, err := out.CalcCellValue("Sheet1", "C1")
			must(err)
			if got != tc.want {
				t.Errorf("SUM of numeric tag = %q, want %q", got, tc.want)
			}
		})
	}
}
