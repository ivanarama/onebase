package printform

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

var (
	reExpr   = regexp.MustCompile(`\{\{([^}]+)\}\}`)
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
)

// Render produces a complete HTML page for the given print form and data context.
func Render(form *PrintForm, ctx *RenderContext) (RenderedForm, error) {
	title := interpolate(form.Title, ctx, 0)
	headerHTML := renderMarkdown(interpolate(form.Header, ctx, 0))
	footerHTML := renderMarkdown(interpolate(form.Footer, ctx, 0))

	var tableHTML string
	if form.Table != nil {
		tableHTML = renderTable(form.Table, ctx)
	}

	page := buildPage(title, headerHTML, tableHTML, footerHTML)
	return RenderedForm(page), nil
}

// interpolate replaces {{...}} expressions in text with values from ctx.
// rowNum is the current table row (for @row), 0 outside table context.
func interpolate(text string, ctx *RenderContext, rowNum int) string {
	return reExpr.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.TrimSpace(match[2 : len(match)-2])
		// split on | for formatter
		parts := strings.SplitN(inner, "|", 2)
		expr := strings.TrimSpace(parts[0])
		fmtSpec := ""
		if len(parts) == 2 {
			fmtSpec = strings.TrimSpace(parts[1])
		}

		val := resolveExpr(expr, ctx, rowNum, nil)
		return template.HTMLEscapeString(ApplyFormat(val, fmtSpec))
	})
}

// interpolateRow is like interpolate but with a specific table row available.
func interpolateRow(text string, ctx *RenderContext, row map[string]any, rowNum int) string {
	return reExpr.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.TrimSpace(match[2 : len(match)-2])
		parts := strings.SplitN(inner, "|", 2)
		expr := strings.TrimSpace(parts[0])
		fmtSpec := ""
		if len(parts) == 2 {
			fmtSpec = strings.TrimSpace(parts[1])
		}
		val := resolveExpr(expr, ctx, rowNum, row)
		return template.HTMLEscapeString(ApplyFormat(val, fmtSpec))
	})
}

// resolveExpr resolves a dot-path expression against the render context.
// currentRow is the current table row (may be nil when resolving header/footer).
func resolveExpr(expr string, ctx *RenderContext, rowNum int, currentRow map[string]any) any {
	if expr == "@row" {
		return rowNum
	}

	// Константы.Name
	if strings.HasPrefix(expr, "Константы.") {
		key := strings.TrimPrefix(expr, "Константы.")
		if ctx.Constants != nil {
			return ctx.Constants[key]
		}
		return nil
	}

	// Итог.Field — totals are resolved during table rendering, not here
	if strings.HasPrefix(expr, "Итог.") {
		return nil
	}

	// FieldName.SubField — resolve reference
	if idx := strings.Index(expr, "."); idx != -1 {
		fieldName := expr[:idx]
		subField := expr[idx+1:]
		// first try to find in currentRow
		if currentRow != nil {
			if refVal, ok := currentRow[fieldName]; ok {
				if refID, ok := refVal.(string); ok {
					if refData, ok := ctx.Refs[refID]; ok {
						return refData[subField]
					}
				}
			}
		}
		// then try in document-level refs
		if ctx.Refs != nil {
			if docVal, ok := ctx.Document[fieldName]; ok {
				if refID, ok := docVal.(string); ok {
					if refData, ok := ctx.Refs[refID]; ok {
						return refData[subField]
					}
				}
			}
		}
		return nil
	}

	// Simple field — try current row first, then document
	if currentRow != nil {
		if v, ok := currentRow[expr]; ok {
			return v
		}
	}
	if ctx.Document != nil {
		if v, ok := ctx.Document[expr]; ok {
			return v
		}
	}
	return nil
}

func renderTable(ts *TableSection, ctx *RenderContext) string {
	rows := ctx.TableParts[ts.Source]

	var sb strings.Builder
	sb.WriteString(`<table class="pf-table">`)

	// header row
	sb.WriteString("<thead><tr>")
	for _, col := range ts.Columns {
		style := ""
		if col.Width != "" {
			style += "width:" + col.Width + ";"
		}
		if col.Align != "" {
			style += "text-align:" + col.Align + ";"
		}
		if style != "" {
			sb.WriteString(fmt.Sprintf(`<th style="%s">%s</th>`, template.HTMLEscapeString(style), template.HTMLEscapeString(col.Label)))
		} else {
			sb.WriteString(fmt.Sprintf(`<th>%s</th>`, template.HTMLEscapeString(col.Label)))
		}
	}
	sb.WriteString("</tr></thead>")

	// compute totals
	totals := make(map[string]float64)
	for _, row := range rows {
		for _, tot := range ts.Totals {
			if tot.Sum {
				if v, ok := row[tot.Field]; ok {
					f, ok2 := toFloat(v)
					if ok2 {
						totals[tot.Field] += f
					}
				}
			}
		}
	}

	// data rows
	sb.WriteString("<tbody>")
	for i, row := range rows {
		sb.WriteString("<tr>")
		for _, col := range ts.Columns {
			align := col.Align
			style := ""
			if align != "" {
				style = "text-align:" + align + ";"
			}
			var val any
			if col.Field == "@row" {
				val = i + 1
			} else if idx := strings.Index(col.Field, "."); idx != -1 {
				// sub-field reference
				fieldName := col.Field[:idx]
				subField := col.Field[idx+1:]
				if refVal, ok := row[fieldName]; ok {
					if refID, ok2 := refVal.(string); ok2 {
						if refData, ok3 := ctx.Refs[refID]; ok3 {
							val = refData[subField]
						}
					}
				}
			} else {
				val = row[col.Field]
			}
			cell := template.HTMLEscapeString(ApplyFormat(val, col.Format))
			if style != "" {
				sb.WriteString(fmt.Sprintf(`<td style="%s">%s</td>`, template.HTMLEscapeString(style), cell))
			} else {
				sb.WriteString(fmt.Sprintf(`<td>%s</td>`, cell))
			}
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody>")

	// totals row
	if len(ts.Totals) > 0 {
		sb.WriteString(`<tfoot><tr class="pf-totals">`)
		// build a set of which columns get totals
		totColIdx := make(map[int]TotalSpec)
		for _, tot := range ts.Totals {
			for ci, col := range ts.Columns {
				if col.Field == tot.Field {
					totColIdx[ci] = tot
				}
			}
		}
		for ci, col := range ts.Columns {
			if tot, ok := totColIdx[ci]; ok {
				label := tot.Label
				if label == "" {
					label = ApplyFormat(totals[tot.Field], col.Format)
				} else {
					label = fmt.Sprintf("%s: %s", label, ApplyFormat(totals[tot.Field], col.Format))
				}
				sb.WriteString(fmt.Sprintf(`<td style="text-align:%s">%s</td>`, col.Align, template.HTMLEscapeString(label)))
			} else {
				sb.WriteString("<td></td>")
			}
		}
		sb.WriteString("</tr></tfoot>")
	}

	sb.WriteString("</table>")
	return sb.String()
}

// renderMarkdown converts a small subset of markdown to HTML.
// Supports: **bold**, ## heading, ___ (hr), blank lines → paragraphs.
func renderMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var paragraph []string

	flush := func() {
		if len(paragraph) > 0 {
			joined := strings.Join(paragraph, " ")
			joined = inlineMarkdown(joined)
			out = append(out, "<p>"+joined+"</p>")
			paragraph = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			out = append(out, "<h2>"+template.HTMLEscapeString(trimmed[3:])+"</h2>")
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			flush()
			out = append(out, "<h1>"+template.HTMLEscapeString(trimmed[2:])+"</h1>")
			continue
		}
		if trimmed == "___" || trimmed == "---" {
			flush()
			out = append(out, "<hr>")
			continue
		}
		paragraph = append(paragraph, trimmed)
	}
	flush()
	return strings.Join(out, "\n")
}

// inlineMarkdown converts **text** → <strong>text</strong>, *text* → <em>text</em>.
func inlineMarkdown(s string) string {
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return "<strong>" + template.HTMLEscapeString(inner) + "</strong>"
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		return "<em>" + template.HTMLEscapeString(inner) + "</em>"
	})
	return s
}

func buildPage(title, headerHTML, tableHTML, footerHTML string) string {
	return `<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="UTF-8">
<title>` + template.HTMLEscapeString(title) + `</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
@page{margin:1cm}
body{font-family:'Times New Roman',Times,serif;font-size:11pt;color:#000;padding:20px}
h1{font-size:16pt;margin-bottom:8px}
h2{font-size:13pt;margin-bottom:6px}
p{margin-bottom:6px;line-height:1.4}
hr{border:none;border-top:1px solid #000;margin:10px 0}
.pf-title{font-size:14pt;font-weight:bold;margin-bottom:16px;text-align:center}
.pf-header{margin-bottom:16px}
.pf-table{width:100%;border-collapse:collapse;margin-bottom:16px}
.pf-table th,.pf-table td{border:1px solid #000;padding:4px 8px;vertical-align:top}
.pf-table th{background:#f0f0f0;font-weight:bold;text-align:center}
.pf-totals td{font-weight:bold;background:#f8f8f8}
.pf-footer{margin-top:16px}
.pf-noprint{display:none}
@media screen{
  .pf-noprint{display:block;margin-bottom:20px}
  .pf-print-btn{display:inline-block;padding:8px 20px;background:#1a5fa8;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:14px;font-family:sans-serif}
  .pf-print-btn:hover{background:#1550a0}
  body{padding:30px;max-width:900px;margin:0 auto}
}
@media print{
  .pf-noprint{display:none!important}
}
</style>
</head>
<body>
<div class="pf-noprint">
  <button class="pf-print-btn" onclick="window.print()">Печать</button>
  &nbsp;
  <a href="javascript:history.back()" style="font-family:sans-serif;font-size:13px;color:#666">← Назад</a>
</div>
<div class="pf-title">` + template.HTMLEscapeString(title) + `</div>
<div class="pf-header">` + headerHTML + `</div>
` + tableHTML + `
<div class="pf-footer">` + footerHTML + `</div>
</body>
</html>`
}
