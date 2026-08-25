package printform

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/sheet"
)

// declarative.go — декларативный движок печатных форм (план 64, этап 3).
// BuildSheet строит sheet.Document по макету v2 + Binding, без кода .os:
//   - области выводятся в порядке Binding.Sequence (или порядке Areas);
//   - область из Binding.Repeat выводится на каждую строку табличной части;
//   - параметры ячеек подставляются через язык выражений binding.go;
//   - {{...}} в text-ячейках интерполируется;
//   - Page → Document.Page; columns → ColWidths; repeat_header → HeaderArea.

// BuildSheet формирует табличный документ по декларативному макету и данным.
func BuildSheet(lt *LayoutTemplate, ctx *RenderContext) (*sheet.Document, error) {
	if lt == nil {
		return nil, fmt.Errorf("buildsheet: nil layout")
	}
	doc := sheet.NewDocument()

	// Страница.
	if lt.Page != nil {
		doc.Page = *lt.Page
		if doc.Page.Format == "" {
			doc.Page.Format = sheet.DefaultPageSetup().Format
		}
	}

	// Ширины колонок (1-based) — конвертация CSS-строк в px модели.
	for i, c := range lt.Columns {
		if px, ok := columnWidthPx(c.Width); ok {
			doc.SetColumnWidth(i+1, px)
		}
	}

	binding := lt.Binding
	if binding == nil {
		binding = &Binding{}
	}

	// Повторяемая шапка (repeat_header) — выводится один раз как обычная область,
	// но дополнительно регистрируется как HeaderArea для повтора на каждой странице.
	repeatHeaderName := binding.RepeatHeader

	// Области, привязанные к строкам ТЧ (Repeat) — выводятся в специальном порядке.
	repeatByArea := make(map[string]*RepeatBinding, len(binding.Repeat))
	for i := range binding.Repeat {
		rb := &binding.Repeat[i]
		repeatByArea[strings.ToLower(rb.Area)] = rb
	}

	// Порядок вывода.
	order := binding.Sequence
	if len(order) == 0 {
		for _, a := range lt.Areas {
			order = append(order, a.Name)
		}
	}

	for _, areaName := range order {
		area := lt.Area(areaName)
		if area == nil {
			continue // молча пропускаем несуществующие имена (валидация — configcheck, этап 4)
		}
		if rb, ok := repeatByArea[strings.ToLower(areaName)]; ok {
			// Повтор по строкам табличной части.
			rows := lookupTablePart(ctx, rb.Source)
			for i, row := range rows {
				sa := buildAreaSheet(area, ctx, row, i+1, rb.Parameters)
				doc.Put(sa)
			}
			continue
		}
		// Обычная область (контекст документа).
		sa := buildAreaSheet(area, ctx, nil, 0, binding.Parameters)
		if strings.EqualFold(areaName, repeatHeaderName) {
			doc.RepeatOnPrint(sa, true)
		}
		doc.Put(sa)
	}

	return doc, nil
}

// buildAreaSheet материализует область макета в sheet.Area, подставляя параметры
// и интерполируя {{...}} в text-ячейках. row/rowNum — контекст строки ТЧ (nil/0 —
// контекст документа). params — карта «имя параметра → выражение»; для параметра
// без записи срабатывает автопривязка по одноимённому полю.
func buildAreaSheet(area *LayoutArea, ctx *RenderContext, row map[string]any, rowNum int, params map[string]string) *sheet.Area {
	rows := len(area.Rows)
	if rows == 0 {
		rows = 1
	}

	sa := sheet.NewArea(0, 0, rows-1, 0)
	sa.Name = area.Name

	// covered — позиции (строка,колонка), накрытые RowSpan/ColSpan ранее
	// размещённых ячеек. Без этого учёта ячейка следующей строки молча села бы
	// в позицию под RowSpan-шапкой и при HTML-рендере (covered-карта sheet/html)
	// исчезла бы. Та же раскладка — общее ядро DSL-пути (BuildAreaCells), поэтому
	// и Макет.Область() с rowspan-шапкой раскладывается корректно (план 64, этап 3).
	covered := make(map[sheet.CellKey]bool)
	maxCol := 0

	for r, lrow := range area.Rows {
		colIdx := 0
		for _, ld := range lrow.Cells {
			// Пропускаем колонки, перекрытые спаном из строк выше.
			for covered[sheet.CellKey{Row: r, Col: colIdx}] {
				colIdx++
			}
			cell := layoutCellToSheet(ld)
			// Текст ячейки: параметр > интерполяция text > статический text.
			if ld.Parameter != "" {
				// richtext-поле (план 65, этап 3): кладём санитизированный HTML в
				// RichHTML (форматирование+картинки в печати), а не в экранируемый
				// Text. Применяется только к параметрам контекста документа (row==nil):
				// richtext в табличных частях запрещён валидацией (этап 1).
				if row == nil && isRichTextParam(ld.Parameter, params, ctx) {
					cell.RichHTML = resolveRichParameter(ld.Parameter, params, ctx, row, rowNum)
					cell.Text = ""
					cell.Value = cell.RichHTML
				} else {
					cell.Text = resolveParameter(ld.Parameter, params, ctx, row, rowNum)
					cell.Value = cell.Text
				}
			} else if strings.Contains(ld.Text, "{{") {
				cell.Text = InterpolateText(ld.Text, ctx, row, rowNum)
				cell.Value = cell.Text
			}
			key := fmt.Sprintf("%d,%d", r, colIdx)
			sa.Cells[key] = cell

			colSpan := cell.ColSpan
			if colSpan < 1 {
				colSpan = 1
			}
			rowSpan := cell.RowSpan
			if rowSpan < 1 {
				rowSpan = 1
			}
			// Помечаем накрытые спаном позиции (кроме самой якорной ячейки).
			for dr := 0; dr < rowSpan; dr++ {
				for dc := 0; dc < colSpan; dc++ {
					if dr == 0 && dc == 0 {
						continue
					}
					covered[sheet.CellKey{Row: r + dr, Col: colIdx + dc}] = true
				}
			}
			if right := colIdx + colSpan - 1; right > maxCol {
				maxCol = right
			}
			colIdx += colSpan
		}
	}
	sa.Right = maxCol
	return sa
}

// resolveParameter вычисляет значение именованного параметра. Если в params есть
// выражение для параметра — используется оно; иначе автопривязка по одноимённому
// полю (имя параметра трактуется как выражение).
func resolveParameter(name string, params map[string]string, ctx *RenderContext, row map[string]any, rowNum int) string {
	return ResolveValue(paramExpr(name, params), ctx, row, rowNum)
}

// paramExpr возвращает выражение параметра: из карты params либо само имя
// (автопривязка по одноимённому полю).
func paramExpr(name string, params map[string]string) string {
	if params != nil {
		if e, ok := lookupParam(params, name); ok {
			return e
		}
	}
	return name
}

// isRichTextParam сообщает, привязан ли параметр к richtext-полю сущности
// (план 65, этап 3). Выражение параметра берётся из params (или имя параметра)
// и проверяется по ctx.RichTextFields.
func isRichTextParam(name string, params map[string]string, ctx *RenderContext) bool {
	return ctx.isRichTextField(paramExpr(name, params))
}

// resolveRichParameter возвращает санитизированный richtext-HTML параметра.
// Значение поля резолвится напрямую (без ApplyFormat — формат к HTML неприменим)
// и пропускается через richtext.Sanitize ещё раз (defense-in-depth: данные могли
// прийти из БД/импорта/API в обход санитайза на сохранении).
func resolveRichParameter(name string, params map[string]string, ctx *RenderContext, row map[string]any, rowNum int) string {
	expr, _ := splitExprFormat(paramExpr(name, params))
	val := ResolveExpr(expr, ctx, row, rowNum)
	s, _ := val.(string)
	return richtext.Sanitize(s)
}

// lookupParam ищет параметр в карте регистронезависимо.
func lookupParam(params map[string]string, name string) (string, bool) {
	if e, ok := params[name]; ok {
		return e, true
	}
	for k, v := range params {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// layoutCellToSheet конвертирует LayoutCell в sheet.Cell (без подстановки текста).
// Это ядро, общее с DSL-путём (maket.go использует его же). Включает per-side
// границы: Borders приоритетнее legacy-пресета Border.
func layoutCellToSheet(ld LayoutCell) *sheet.Cell {
	cell := sheet.NewCell(ld.Text)
	cell.Value = ld.Text
	cell.Bold = ld.Bold
	cell.Italic = ld.Italic
	if ld.Align != "" {
		cell.Align = strings.ToLower(ld.Align)
	}
	if ld.VAlign != "" {
		cell.Vertical = strings.ToLower(ld.VAlign)
	}
	if ld.FontSize > 0 {
		cell.FontSize = ld.FontSize
	}
	if ld.FontFamily != "" {
		cell.FontFamily = ld.FontFamily
	}
	if ld.BackColor != "" {
		cell.BackColor = ld.BackColor
	}
	if ld.TextColor != "" {
		cell.TextColor = ld.TextColor
	}
	if ld.ColSpan > 1 {
		cell.ColSpan = ld.ColSpan
	}
	if ld.RowSpan > 1 {
		cell.RowSpan = ld.RowSpan
	}
	if ld.Parameter != "" {
		cell.ParameterName = ld.Parameter
	}
	if ld.Picture != "" {
		cell.Picture = ld.Picture
	}
	// Границы: per-side приоритетнее legacy-пресета.
	if !ld.Borders.IsZero() {
		cell.Border = ""
		cell.BorderLeft = strings.ToLower(ld.Borders.Left)
		cell.BorderTop = strings.ToLower(ld.Borders.Top)
		cell.BorderRight = strings.ToLower(ld.Borders.Right)
		cell.BorderBottom = strings.ToLower(ld.Borders.Bottom)
	} else if ld.Border != "" {
		cell.Border = strings.ToLower(ld.Border)
	}
	return cell
}

// BuildAreaCells материализует область макета в sheet.Area БЕЗ подстановки данных
// (статические тексты ячеек). Используется DSL-путём (maket.go), где значения
// параметров устанавливаются позже через Область.Параметры.X. Экспортирована,
// чтобы DSL-обвязка и декларативный движок строили ячейки одинаково.
func BuildAreaCells(area *LayoutArea) *sheet.Area {
	return buildAreaSheet(area, nil, nil, 0, nil)
}

// columnWidthPx конвертирует CSS-ширину колонки в px модели sheet.
// Поддержка: "120px"→120, "30mm"→mm→px, число без единиц→px, "auto"/""/"%"→(0,false)
// (авто-распределение остатка делает рендер).
func columnWidthPx(w string) (float64, bool) {
	w = strings.TrimSpace(strings.ToLower(w))
	if w == "" || w == "auto" || strings.HasSuffix(w, "%") {
		return 0, false
	}
	if strings.HasSuffix(w, "px") {
		if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(w, "px")), 64); err == nil {
			return v, true
		}
		return 0, false
	}
	if strings.HasSuffix(w, "mm") {
		if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(w, "mm")), 64); err == nil {
			return mmToPx(v), true
		}
		return 0, false
	}
	// Голое число — трактуем как px.
	if v, err := strconv.ParseFloat(w, 64); err == nil {
		return v, true
	}
	return 0, false
}

// mmToPx конвертирует миллиметры в пиксели CSS (96 dpi).
func mmToPx(mm float64) float64 { return mm * 96.0 / 25.4 }
