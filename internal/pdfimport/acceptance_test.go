package pdfimport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/printform"
)

// TestAcceptanceRealCorpus прогоняет ImportPage на реальных PDF пользователя
// (план 64, этап 6, фаза 3). Активируется ONEBASE_PDF_CORPUS=<каталог>.
// PDF-файлы НЕ хранятся в репозитории (персональные данные) — тест читает их с
// диска пользователя только локально.
func TestAcceptanceRealCorpus(t *testing.T) {
	dir := os.Getenv("ONEBASE_PDF_CORPUS")
	if dir == "" {
		t.Skip("set ONEBASE_PDF_CORPUS to run acceptance on real PDFs")
	}
	files := []string{
		"УПД (статус 2) № ПО-100020 от 30 апреля 2026 г.pdf",
		"УПД (статус 1) № 1 от 13 января 2026 г.pdf",
		"Счет на оплату № ПО-22 от 02 июня 2026 г.pdf",
		"Акт об оказании услуг № АС-100006 от 05 июня 2025 г.pdf",
		"Акт Инфостарт (с печатью) № 10 от 23.01.2025.pdf",
	}
	for _, fn := range files {
		path := filepath.Join(dir, fn)
		data, err := os.ReadFile(path) //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
		if err != nil {
			t.Logf("SKIP %s: %v", fn, err)
			continue
		}
		start := time.Now()
		tpl, err := ImportBytes(data, 1)
		elapsed := time.Since(start)
		if err != nil {
			t.Logf("%s: ОШИБКА %v (%.0fмс)", fn, err, float64(elapsed.Milliseconds()))
			continue
		}
		nTexts, nRows, nCols := layoutStats(tpl)
		page := ""
		if tpl.Page != nil {
			page = tpl.Page.Orientation + "/" + tpl.Page.Format
		}
		t.Logf("%s: тексты=%d строк=%d колонок=%d page=%s (%.0fмс)",
			fn, nTexts, nRows, nCols, page, float64(elapsed.Milliseconds()))

		// Для целевого УПД печатаем образцы распознанных ячеек.
		if strings.Contains(fn, "ПО-100020") {
			t.Logf("  --- образцы ячеек целевого УПД ---")
			samples := sampleCells(tpl, 18)
			for _, s := range samples {
				t.Logf("    %s", s)
			}
		}
	}
}

func layoutStats(tpl *printform.LayoutTemplate) (texts, rows, cols int) {
	cols = len(tpl.Columns)
	for _, area := range tpl.Areas {
		rows += len(area.Rows)
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				if strings.TrimSpace(cell.Text) != "" {
					texts++
				}
			}
		}
	}
	return
}

func sampleCells(tpl *printform.LayoutTemplate, max int) []string {
	var out []string
	for _, area := range tpl.Areas {
		for _, row := range area.Rows {
			for _, cell := range row.Cells {
				txt := strings.TrimSpace(cell.Text)
				if txt == "" {
					continue
				}
				if len([]rune(txt)) > 50 {
					txt = string([]rune(txt)[:50]) + "…"
				}
				span := ""
				if cell.ColSpan > 1 || cell.RowSpan > 1 {
					cs, rs := cell.ColSpan, cell.RowSpan
					if cs == 0 {
						cs = 1
					}
					if rs == 0 {
						rs = 1
					}
					span = " [span " + strconv.Itoa(cs) + "x" + strconv.Itoa(rs) + "]"
				}
				out = append(out, "«"+txt+"»"+span)
				if len(out) >= max {
					return out
				}
			}
		}
	}
	return out
}
