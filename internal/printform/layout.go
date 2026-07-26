package printform

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/sheet"
	"gopkg.in/yaml.v3"
)

// LayoutTemplate defines a макет (template) with named areas for print forms.
// Each area contains rows of cells with static text or parameter placeholders.
//
// Этап 3 (план 64): Areas стал упорядоченным slice (имя — поле Name внутри
// LayoutArea) вместо map — порядок областей важен для декларативного движка
// (BuildSheet выводит области по порядку). Совместимость со старым map-форматом
// YAML обеспечивается кастомным UnmarshalYAML (см. ниже): принимаются и mapping
// (legacy), и sequence (новый). MarshalYAML всегда пишет sequence.
//
// Page использует sheet.PageSetup напрямую: sheet — нейтральный пакет (не
// импортирует printform), поэтому цикла импортов нет; собственный тип не плодим.
type LayoutTemplate struct {
	Name     string
	Document string
	Page     *sheet.PageSetup
	Columns  []LayoutColumn
	Areas    []*LayoutArea
	Binding  *Binding
}

// LayoutColumn defines column-level properties (width applies to all areas).
type LayoutColumn struct {
	Width string `yaml:"width,omitempty"` // CSS value: "120px", "10%", "auto"
}

// LayoutArea defines a named rectangular area with rows of cells.
// Name заполняется из ключа mapping (legacy) или из поля name: (новый формат).
type LayoutArea struct {
	Name string      `yaml:"name,omitempty"`
	Rows []LayoutRow `yaml:"rows"`
}

// LayoutRow is a single row of cells in a layout area.
type LayoutRow struct {
	Height string       `yaml:"height,omitempty"` // CSS value: "20px", "auto"
	Cells  []LayoutCell `yaml:"cells"`
}

// CellBorders задаёт рамки ячейки по сторонам. Значения каждой стороны:
// ""|none|thin|medium|thick. Приоритетнее legacy-пресета Border, если задана
// хотя бы одна сторона.
type CellBorders struct {
	Left   string `yaml:"left,omitempty"`
	Top    string `yaml:"top,omitempty"`
	Right  string `yaml:"right,omitempty"`
	Bottom string `yaml:"bottom,omitempty"`
}

// IsZero сообщает, что все стороны пусты (рамки по сторонам не заданы).
func (b *CellBorders) IsZero() bool {
	return b == nil || (b.Left == "" && b.Top == "" && b.Right == "" && b.Bottom == "")
}

// LayoutCell defines a single cell in a layout template.
// A cell can contain either static text or a named parameter placeholder.
type LayoutCell struct {
	Text        string       `yaml:"text,omitempty"`
	Parameter   string       `yaml:"parameter,omitempty"`
	Bold        bool         `yaml:"bold,omitempty"`
	Italic      bool         `yaml:"italic,omitempty"`
	Align       string       `yaml:"align,omitempty"`  // left/center/right
	VAlign      string       `yaml:"valign,omitempty"` // top/middle/bottom
	FontSize    int          `yaml:"fontSize,omitempty"`
	FontFamily  string       `yaml:"fontFamily,omitempty"` // e.g. "Arial", "Times New Roman"
	BackColor   string       `yaml:"backColor,omitempty"`
	TextColor   string       `yaml:"textColor,omitempty"`
	ColSpan     int          `yaml:"colspan,omitempty"`
	RowSpan     int          `yaml:"rowspan,omitempty"`
	Border      string       `yaml:"border,omitempty"`      // legacy-пресет: none/all/thin/thick
	BorderColor string       `yaml:"borderColor,omitempty"` // CSS color, default #ccc
	Borders     *CellBorders `yaml:"borders,omitempty"`     // per-side, приоритет над Border
}

// Binding описывает декларативную привязку данных к областям макета — печатная
// форма без кода (план 64, этап 3). См. BuildSheet (declarative.go).
type Binding struct {
	// Sequence — порядок вывода областей. Пустой = порядок Areas.
	Sequence []string `yaml:"sequence,omitempty"`
	// RepeatHeader — имя области, повторяемой на каждой странице PDF.
	RepeatHeader string `yaml:"repeat_header,omitempty"`
	// Parameters — параметр области → выражение (язык binding.go).
	Parameters map[string]string `yaml:"parameters,omitempty"`
	// Repeat — области, выводимые на каждую строку табличной части.
	Repeat []RepeatBinding `yaml:"repeat,omitempty"`
}

// RepeatBinding — область, повторяемая по строкам табличной части Source.
type RepeatBinding struct {
	Area       string            `yaml:"area"`
	Source     string            `yaml:"source"` // имя табличной части
	Parameters map[string]string `yaml:"parameters,omitempty"`
}

// layoutTemplateMarshal — внутренний вид для сериализации (sequence Areas).
type layoutTemplateMarshal struct {
	Name     string           `yaml:"name,omitempty"`
	Document string           `yaml:"document,omitempty"`
	Page     *sheet.PageSetup `yaml:"page,omitempty"`
	Columns  []LayoutColumn   `yaml:"columns,omitempty"`
	Areas    []*LayoutArea    `yaml:"areas,omitempty"`
	Binding  *Binding         `yaml:"binding,omitempty"`
}

// MarshalYAML пишет макет в новом формате: areas — sequence элементов с name.
func (lt LayoutTemplate) MarshalYAML() (any, error) {
	return layoutTemplateMarshal(lt), nil
}

// UnmarshalYAML принимает оба формата областей:
//   - mapping (legacy): areas: {Имя: {rows: ...}} — yaml.v3 сохраняет порядок
//     ключей документа, поэтому обходим node.Content попарно (ключ, значение).
//   - sequence (новый): areas: [{name: Имя, rows: ...}].
//
// Остальные поля (name/document/page/columns/binding) декодируются стандартно.
func (lt *LayoutTemplate) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("layout: ожидался mapping на верхнем уровне, получено kind=%d", node.Kind)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := strings.ToLower(keyNode.Value)
		switch key {
		case "name":
			lt.Name = valNode.Value
		case "document":
			lt.Document = valNode.Value
		case "page":
			var p sheet.PageSetup
			if err := valNode.Decode(&p); err != nil {
				return fmt.Errorf("layout: page: %w", err)
			}
			lt.Page = &p
		case "columns":
			if err := valNode.Decode(&lt.Columns); err != nil {
				return fmt.Errorf("layout: columns: %w", err)
			}
		case "binding":
			var b Binding
			if err := valNode.Decode(&b); err != nil {
				return fmt.Errorf("layout: binding: %w", err)
			}
			lt.Binding = &b
		case "areas":
			areas, err := decodeAreas(valNode)
			if err != nil {
				return err
			}
			lt.Areas = areas
		}
	}
	return nil
}

// decodeAreas разбирает узел areas: mapping (legacy, порядок ключей сохранён) или
// sequence (новый формат).
func decodeAreas(node *yaml.Node) ([]*LayoutArea, error) {
	switch node.Kind {
	case yaml.MappingNode:
		// legacy: попарный обход (ключ — имя области, значение — {rows: ...}).
		out := make([]*LayoutArea, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			name := node.Content[i].Value
			var area LayoutArea
			if err := node.Content[i+1].Decode(&area); err != nil {
				return nil, fmt.Errorf("layout: area %q: %w", name, err)
			}
			if area.Name == "" {
				area.Name = name
			}
			out = append(out, &area)
		}
		return out, nil
	case yaml.SequenceNode:
		var out []*LayoutArea
		if err := node.Decode(&out); err != nil {
			return nil, fmt.Errorf("layout: areas sequence: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("layout: areas должен быть mapping или sequence, получено kind=%d", node.Kind)
	}
}

// Area возвращает область по имени (регистронезависимо) или nil.
func (lt *LayoutTemplate) Area(name string) *LayoutArea {
	if lt == nil {
		return nil
	}
	for _, a := range lt.Areas {
		if strings.EqualFold(a.Name, name) {
			return a
		}
	}
	return nil
}

// LoadLayout loads a layout template from a YAML file.
func LoadLayout(path string) (*LayoutTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("layout: read %s: %w", path, err)
	}
	lt, err := ParseLayoutBytes(data)
	if err != nil {
		return nil, fmt.Errorf("layout: parse %s: %w", path, err)
	}
	if lt.Name == "" {
		lt.Name = strings.TrimSuffix(filepath.Base(path), ".layout.yaml")
	}
	return lt, nil
}

// ParseLayoutBytes разбирает макет из памяти (без файла). Имя из имени файла
// здесь не подставляется — вызывающий задаёт его сам при необходимости.
func ParseLayoutBytes(data []byte) (*LayoutTemplate, error) {
	var lt LayoutTemplate
	if err := yaml.Unmarshal(data, &lt); err != nil {
		return nil, err
	}
	return &lt, nil
}

// FindLayoutFile looks for a .layout.yaml file matching the given .os file path.
// For "накладная.os", it looks for "накладная.layout.yaml" in the same directory.
func FindLayoutFile(osPath string) string {
	dir := filepath.Dir(osPath)
	base := strings.TrimSuffix(filepath.Base(osPath), ".os")
	layoutPath := filepath.Join(dir, base+".layout.yaml")
	if _, err := os.Stat(layoutPath); err == nil {
		return layoutPath
	}
	return ""
}

// borderCSS returns the CSS border declaration for the given preset.
func borderCSS(preset, color string) string {
	color = csssafe.Color(color)
	if color == "" {
		color = "#ccc"
	}
	switch strings.ToLower(preset) {
	case "", "all", "thin":
		return "1px solid " + color
	case "medium":
		return "1.5px solid " + color
	case "thick":
		return "2px solid " + color
	case "none":
		return "none"
	default:
		return "1px solid " + color
	}
}

// PreviewHTML returns an HTML preview of the layout template showing all named areas.
func (lt *LayoutTemplate) PreviewHTML() string {
	var sb strings.Builder
	sb.WriteString(`<div style="font-family:Arial,sans-serif;font-size:12px">`)
	for _, area := range lt.Areas {
		fmt.Fprintf(&sb,
			`<div style="margin-bottom:16px"><div style="font-weight:bold;color:#4a9;margin-bottom:4px">%s</div>`,
			html.EscapeString(area.Name),
		)
		sb.WriteString(`<table style="border-collapse:collapse">`)
		// <colgroup> for column widths.
		if len(lt.Columns) > 0 {
			sb.WriteString("<colgroup>")
			for _, c := range lt.Columns {
				if width := csssafe.Length(c.Width); width != "" {
					fmt.Fprintf(&sb, `<col style="width:%s">`, html.EscapeString(width))
				} else {
					sb.WriteString("<col>")
				}
			}
			sb.WriteString("</colgroup>")
		}
		for _, row := range area.Rows {
			if height := csssafe.Length(row.Height); height != "" {
				fmt.Fprintf(&sb, `<tr style="height:%s">`, html.EscapeString(height))
			} else {
				sb.WriteString("<tr>")
			}
			for _, cell := range row.Cells {
				style := "padding:4px 8px;min-width:40px;"
				style += previewBorderCSS(cell)
				if cell.Bold {
					style += "font-weight:bold;"
				}
				if cell.Italic {
					style += "font-style:italic;"
				}
				if cell.FontSize > 0 {
					style += fmt.Sprintf("font-size:%dpt;", cell.FontSize)
				}
				if family := csssafe.FontFamily(cell.FontFamily); family != "" {
					style += "font-family:" + family + ";"
				}
				if color := csssafe.Color(cell.BackColor); color != "" {
					style += "background-color:" + color + ";"
				}
				if color := csssafe.Color(cell.TextColor); color != "" {
					style += "color:" + color + ";"
				}
				if align := csssafe.TextAlign(cell.Align); align != "" {
					style += "text-align:" + align + ";"
				}
				if cell.VAlign != "" {
					switch strings.ToLower(cell.VAlign) {
					case "middle", "center":
						style += "vertical-align:middle;"
					case "top":
						style += "vertical-align:top;"
					case "bottom":
						style += "vertical-align:bottom;"
					}
				}
				attrs := ""
				if cell.ColSpan > 1 {
					attrs += fmt.Sprintf(` colspan="%d"`, cell.ColSpan)
				}
				if cell.RowSpan > 1 {
					attrs += fmt.Sprintf(` rowspan="%d"`, cell.RowSpan)
				}
				var text string
				if cell.Parameter != "" {
					text = fmt.Sprintf(`<span style="color:#888">[%s]</span>`, html.EscapeString(cell.Parameter))
				} else if cell.Text != "" {
					text = html.EscapeString(cell.Text)
				} else {
					text = "&nbsp;"
				}
				fmt.Fprintf(&sb, `<td style="%s"%s>%s</td>`, style, attrs, text)
			}
			sb.WriteString("</tr>")
		}
		sb.WriteString("</table></div>")
	}
	sb.WriteString("</div>")
	return sb.String()
}

// previewBorderCSS строит CSS-рамку ячейки для превью: per-side Borders имеют
// приоритет над legacy-пресетом Border.
func previewBorderCSS(cell LayoutCell) string {
	if !cell.Borders.IsZero() {
		b := cell.Borders
		out := ""
		out += "border-left:" + borderCSS(b.Left, cell.BorderColor) + ";"
		out += "border-top:" + borderCSS(b.Top, cell.BorderColor) + ";"
		out += "border-right:" + borderCSS(b.Right, cell.BorderColor) + ";"
		out += "border-bottom:" + borderCSS(b.Bottom, cell.BorderColor) + ";"
		return out
	}
	return "border:" + borderCSS(cell.Border, cell.BorderColor) + ";"
}
