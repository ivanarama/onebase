package xlsximport

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
)

// Сквозной тест: импортированный бланк проходит тем же путём, что и у
// пользователя, — BuildSheet → HTML и PDF. Проверять только структуру макета
// мало: она может быть верной, а печать при этом падать или терять данные.

func TestImportedLayoutRendersHTMLAndPDF(t *testing.T) {
	lt := importBlank(t, blankOpts{}, "Товары").Layout
	lt.Name = "Накладная"
	lt.Document = "Реализация"

	doc, err := printform.BuildSheet(lt, demoContext())
	if err != nil {
		t.Fatalf("BuildSheet: %v", err)
	}

	html := doc.HTMLString()
	for _, want := range []string{
		"Накладная № РН-000007", // {{Номер}}
		"01.03.2026",            // {{Дата | date}}
		"ООО «Ромашка»",         // {{Контрагент.Наименование}} через Refs
		"Пила",                  // строка 1 табличной части
		"Молоток",               // строка 2 — область размножилась
		"1250.00",               // {{Итог.Товары.Сумма | number:2}}
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в HTML нет %q", want)
		}
	}
	// Нумерация строк @row внутри repeat-области.
	if !strings.Contains(html, ">1<") || !strings.Contains(html, ">2<") {
		t.Errorf("номера строк @row не подставлены")
	}

	pdf, err := doc.PDF(sheet.PDFOptions{Title: "Накладная"})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) || len(pdf) < 1000 {
		t.Errorf("PDF не собрался: %d байт", len(pdf))
	}
}

// demoContext — данные документа для сквозного рендера.
func demoContext() *printform.RenderContext {
	return &printform.RenderContext{
		Document: map[string]any{
			"Номер":      "РН-000007",
			"Дата":       "2026-03-01",
			"Контрагент": "ref-1",
		},
		// Refs ключуется идентификатором ссылки (значением поля документа),
		// а не именем поля — см. refSubValue в printform/binding.go.
		Refs: map[string]map[string]any{
			"ref-1": {"Наименование": "ООО «Ромашка»"},
		},
		TableParts: map[string][]map[string]any{
			"Товары": {
				{"Номенклатура": "Пила", "Количество": 1.0, "Цена": 1000.0, "Сумма": 1000.0},
				{"Номенклатура": "Молоток", "Количество": 2.0, "Цена": 125.0, "Сумма": 250.0},
			},
		},
	}
}

// Логотип в шапке бланка — самый частый случай, ради которого у LayoutCell
// заведено поле picture: до этого декларативный макет картинку не мог показать
// вообще.
func TestImport_PictureReachesSheetCell(t *testing.T) {
	data := blankWithLogo(t)

	res, err := ImportBytes(data, Options{TableParts: []string{"Товары"}})
	if err != nil {
		t.Fatalf("ImportBytes: %v", err)
	}

	found := ""
	for _, area := range res.Layout.Areas {
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				if cell.Picture != "" {
					found = cell.Picture
				}
			}
		}
	}
	if !strings.HasPrefix(found, "data:image/png;base64,") {
		t.Fatalf("картинка не перенесена в макет: %q", truncate(found))
	}

	// И доезжает до модели табличного документа, из которой рисуются HTML и PDF.
	doc, err := printform.BuildSheet(res.Layout, demoContext())
	if err != nil {
		t.Fatalf("BuildSheet: %v", err)
	}
	if !strings.Contains(doc.HTMLString(), "data:image/png;base64,") {
		t.Errorf("картинки нет в HTML печатной формы")
	}
}

// blankWithLogo — бланк с картинкой в свободной ячейке шапки.
func blankWithLogo(t *testing.T) []byte {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(buildBlank(t, blankOpts{})))
	if err != nil {
		t.Fatalf("открыть бланк: %v", err)
	}
	defer func() { _ = f.Close() }()

	if err := f.AddPictureFromBytes(f.GetSheetName(0), "A2", &excelize.Picture{
		Extension: ".png",
		File:      tinyPNG(t),
		Format:    &excelize.GraphicOptions{},
	}); err != nil {
		t.Fatalf("вставить картинку: %v", err)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("записать бланк: %v", err)
	}
	return buf.Bytes()
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, G: 30, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
