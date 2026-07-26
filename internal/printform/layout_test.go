package printform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLayoutRoundTrip ensures every field survives marshal → unmarshal in the
// new sequence-based model (Areas slice).
func TestLayoutRoundTrip(t *testing.T) {
	src := &LayoutTemplate{
		Name:     "Накладная",
		Document: "РеализацияТоваров",
		Columns: []LayoutColumn{
			{Width: "120px"},
			{Width: "auto"},
		},
		Areas: []*LayoutArea{
			{
				Name: "Шапка",
				Rows: []LayoutRow{
					{
						Height: "20px",
						Cells: []LayoutCell{
							{
								Text:        "Поставщик",
								Bold:        true,
								Italic:      true,
								Align:       "center",
								VAlign:      "middle",
								FontSize:    12,
								FontFamily:  "Arial",
								BackColor:   "#E8E8E8",
								TextColor:   "#000000",
								ColSpan:     2,
								RowSpan:     1,
								Border:      "thick",
								BorderColor: "#333",
								Borders:     &CellBorders{Left: "thin", Top: "medium", Right: "thick", Bottom: "none"},
							},
							{
								Parameter: "ИмяПоставщика",
							},
						},
					},
				},
			},
		},
		Binding: &Binding{
			Sequence:     []string{"Шапка"},
			RepeatHeader: "Шапка",
			Parameters:   map[string]string{"НомерУПД": "Номер"},
			Repeat: []RepeatBinding{
				{Area: "Строка", Source: "Товары", Parameters: map[string]string{"Кол": "Количество"}},
			},
		},
	}

	data, err := yaml.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// MarshalYAML must produce a sequence form for areas.
	if !strings.Contains(string(data), "- name: Шапка") {
		t.Fatalf("expected sequence areas in marshal output, got:\n%s", data)
	}

	var got LayoutTemplate
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "Накладная" || got.Document != "РеализацияТоваров" {
		t.Fatalf("name/document lost: %+v", got)
	}
	if len(got.Columns) != 2 || got.Columns[0].Width != "120px" {
		t.Fatalf("columns lost: %+v", got.Columns)
	}
	area := got.Area("Шапка")
	if area == nil {
		t.Fatalf("area Шапка missing")
		return
	}
	if len(area.Rows) != 1 || area.Rows[0].Height != "20px" {
		t.Fatalf("row height lost: %+v", area.Rows)
	}
	c := area.Rows[0].Cells[0]
	if c.Text != "Поставщик" || !c.Bold || !c.Italic ||
		c.Align != "center" || c.VAlign != "middle" ||
		c.FontSize != 12 || c.FontFamily != "Arial" ||
		c.BackColor != "#E8E8E8" || c.TextColor != "#000000" ||
		c.ColSpan != 2 || c.RowSpan != 1 ||
		c.Border != "thick" || c.BorderColor != "#333" {
		t.Fatalf("cell fields lost: %+v", c)
	}
	if c.Borders == nil || c.Borders.Left != "thin" || c.Borders.Top != "medium" ||
		c.Borders.Right != "thick" || c.Borders.Bottom != "none" {
		t.Fatalf("per-side borders lost: %+v", c.Borders)
	}
	if area.Rows[0].Cells[1].Parameter != "ИмяПоставщика" {
		t.Fatalf("parameter cell lost: %+v", area.Rows[0].Cells[1])
	}
	if got.Binding == nil || got.Binding.RepeatHeader != "Шапка" ||
		got.Binding.Parameters["НомерУПД"] != "Номер" ||
		len(got.Binding.Repeat) != 1 || got.Binding.Repeat[0].Source != "Товары" {
		t.Fatalf("binding lost: %+v", got.Binding)
	}
}

// TestLayoutLegacyMappingOrder ensures old YAML with areas as a mapping parses
// AND preserves document order of the areas (yaml.v3 keeps key order).
func TestLayoutLegacyMappingOrder(t *testing.T) {
	src := `
name: Старый
document: Реализация
areas:
  Заголовок:
    rows:
      - cells:
          - text: "Док"
  Шапка:
    rows:
      - cells:
          - text: "Заголовок"
            bold: true
            align: center
            colspan: 3
            backColor: "#EEE"
  Подвал:
    rows:
      - cells:
          - text: "Подпись"
`
	var lt LayoutTemplate
	if err := yaml.Unmarshal([]byte(src), &lt); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if len(lt.Areas) != 3 {
		t.Fatalf("expected 3 areas, got %d", len(lt.Areas))
	}
	wantOrder := []string{"Заголовок", "Шапка", "Подвал"}
	for i, w := range wantOrder {
		if lt.Areas[i].Name != w {
			t.Fatalf("area order lost: at %d want %q got %q (all: %v)", i, w, lt.Areas[i].Name, names(lt.Areas))
		}
	}
	c := lt.Area("Шапка").Rows[0].Cells[0]
	if c.Text != "Заголовок" || !c.Bold || c.Align != "center" || c.ColSpan != 3 {
		t.Fatalf("legacy fields lost: %+v", c)
	}
	// PreviewHTML must not panic and include text.
	html := lt.PreviewHTML()
	if !strings.Contains(html, "Заголовок") {
		t.Fatalf("PreviewHTML missing text: %s", html)
	}
}

// TestLayoutAreaCaseInsensitive verifies Area() lookup is case-insensitive.
func TestLayoutAreaCaseInsensitive(t *testing.T) {
	lt := &LayoutTemplate{Areas: []*LayoutArea{{Name: "ШапкаТаблицы"}}}
	if lt.Area("шапкатаблицы") == nil {
		t.Fatalf("case-insensitive Area lookup failed (lower)")
	}
	if lt.Area("ШАПКАТАБЛИЦЫ") == nil {
		t.Fatalf("case-insensitive Area lookup failed (upper)")
	}
	if lt.Area("Несуществующая") != nil {
		t.Fatalf("Area returned non-nil for missing name")
	}
}

func names(areas []*LayoutArea) []string {
	out := make([]string, len(areas))
	for i, a := range areas {
		out[i] = a.Name
	}
	return out
}

// TestPreviewHTMLNewFields ensures PreviewHTML renders CSS for new fields,
// including per-side borders.
func TestPreviewHTMLNewFields(t *testing.T) {
	lt := &LayoutTemplate{
		Name: "T",
		Columns: []LayoutColumn{
			{Width: "80px"},
			{Width: "auto"},
		},
		Areas: []*LayoutArea{
			{
				Name: "A",
				Rows: []LayoutRow{
					{
						Height: "30px",
						Cells: []LayoutCell{
							{
								Text:        "X",
								FontFamily:  "Times New Roman",
								VAlign:      "middle",
								Border:      "thick",
								BorderColor: "#ff0000",
								RowSpan:     2,
							},
							{Text: "Y", Border: "none"},
							{Text: "Z", Borders: &CellBorders{Left: "thin", Bottom: "thick"}},
						},
					},
				},
			},
		},
	}
	out := lt.PreviewHTML()
	checks := []string{
		"<colgroup>",
		"width:80px",
		"font-family:Times New Roman",
		"vertical-align:middle",
		"2px solid #ff0000",
		`rowspan="2"`,
		"height:30px",
		"border:none",
		"border-left:1px solid", // per-side thin
		"border-bottom:2px solid",
	}
	for _, sub := range checks {
		if !strings.Contains(out, sub) {
			t.Errorf("PreviewHTML missing %q\nGot: %s", sub, out)
		}
	}
}

// TestLayoutAcceptsRealExample parses a representative legacy layout (areas /
// rows / cells with a parameter placeholder) and makes sure the model loads it
// cleanly and renders a preview.
func TestLayoutAcceptsRealExample(t *testing.T) {
	const src = `name: Накладная
document: РеализацияТоваров
columns:
  - width: 120px
  - width: auto
areas:
  Заголовок:
    rows:
      - height: 24px
        cells:
          - text: "Накладная"
            bold: true
            colspan: 4
            align: center
            fontSize: 16
          - parameter: НомерПП
`
	path := filepath.Join(t.TempDir(), "накладная.layout.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lt, err := LoadLayout(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if lt.Name != "Накладная" || lt.Document != "РеализацияТоваров" {
		t.Fatalf("header: %+v", lt)
	}
	if lt.Area("Заголовок") == nil {
		t.Fatalf("Заголовок missing")
	}
	html := lt.PreviewHTML()
	if !strings.Contains(html, "Накладная") {
		t.Errorf("preview missing title: %s", html)
	}
	if !strings.Contains(html, "[НомерПП]") {
		t.Errorf("preview missing parameter placeholder")
	}
	// Round-trip — fields must survive marshal/unmarshal.
	data, err := yaml.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got LayoutTemplate
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	titleCell := got.Area("Заголовок").Rows[0].Cells[0]
	if titleCell.Text != "Накладная" || !titleCell.Bold || titleCell.ColSpan != 4 ||
		titleCell.Align != "center" || titleCell.FontSize != 16 {
		t.Errorf("title cell lost fields after round-trip: %+v", titleCell)
	}
}

// TestLayoutPageRoundTrip ensures page: setup survives load/marshal.
func TestLayoutPageRoundTrip(t *testing.T) {
	const src = `name: УПД
document: РеализацияТоваров
page:
  orientation: landscape
  format: A4
  margins:
    top: 5
    bottom: 5
    left: 8
    right: 8
areas:
  - name: Страница1
    rows:
      - cells:
          - text: "X"
`
	lt, err := ParseLayoutBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if lt.Page == nil || lt.Page.Orientation != "landscape" || lt.Page.MarginsMM.Left != 8 {
		t.Fatalf("page lost: %+v", lt.Page)
	}
	data, err := yaml.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got LayoutTemplate
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Page == nil || got.Page.Orientation != "landscape" {
		t.Fatalf("page lost after round-trip: %+v", got.Page)
	}
}

// TestLayoutPageMarginsBinding проверяет, что документированные ключи page:
// (orientation/format/margins{top,bottom,left,right}) биндятся в PageSetup.
// До добавления yaml-тегов margins: молча игнорировался (нули).
func TestLayoutPageMarginsBinding(t *testing.T) {
	const src = `name: M
page: {orientation: landscape, format: A5, margins: {top: 7, left: 5}}
areas:
  - name: A
    rows:
      - cells:
          - text: "X"
`
	lt, err := ParseLayoutBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if lt.Page == nil {
		t.Fatalf("page nil")
	}
	if lt.Page.Orientation != "landscape" {
		t.Errorf("orientation = %q, want landscape", lt.Page.Orientation)
	}
	if lt.Page.Format != "A5" {
		t.Errorf("format = %q, want A5", lt.Page.Format)
	}
	if lt.Page.MarginsMM.Top != 7 {
		t.Errorf("margins.top = %v, want 7", lt.Page.MarginsMM.Top)
	}
	if lt.Page.MarginsMM.Left != 5 {
		t.Errorf("margins.left = %v, want 5", lt.Page.MarginsMM.Left)
	}
	// Незаданные поля — нули.
	if lt.Page.MarginsMM.Bottom != 0 || lt.Page.MarginsMM.Right != 0 {
		t.Errorf("unset margins not zero: %+v", lt.Page.MarginsMM)
	}

	// Round-trip: MarshalYAML должен писать естественный ключ margins:, а не
	// marginsmm: — иначе документированный формат не читается обратно.
	data, err := yaml.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "marginsmm") {
		t.Errorf("marshal wrote legacy key marginsmm:\n%s", data)
	}
	if !strings.Contains(string(data), "margins:") {
		t.Errorf("marshal did not write margins::\n%s", data)
	}
	var got LayoutTemplate
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if got.Page == nil || got.Page.MarginsMM.Top != 7 || got.Page.MarginsMM.Left != 5 {
		t.Errorf("margins lost after round-trip: %+v", got.Page)
	}
}

// TestPreviewHTMLEscapesText ensures user text/parameter cannot inject HTML.
func TestPreviewHTMLEscapesText(t *testing.T) {
	lt := &LayoutTemplate{
		Areas: []*LayoutArea{
			{Name: "A", Rows: []LayoutRow{{Cells: []LayoutCell{
				{Text: "<script>alert(1)</script>"},
				{Parameter: "<b>bad</b>"},
			}}}},
		},
	}
	out := lt.PreviewHTML()
	if strings.Contains(out, "<script>") {
		t.Errorf("unescaped script tag in preview: %s", out)
	}
	if strings.Contains(out, "<b>bad</b>") {
		t.Errorf("unescaped parameter in preview: %s", out)
	}
}

func TestPreviewHTMLSanitizesInlineCSS(t *testing.T) {
	lt := &LayoutTemplate{
		Columns: []LayoutColumn{{Width: `80px;color:red`}},
		Areas: []*LayoutArea{
			{
				Name: "A",
				Rows: []LayoutRow{
					{
						Height: `30px;background:url(javascript:1)`,
						Cells: []LayoutCell{
							{
								Text:        "X",
								FontFamily:  `Arial";color:red`,
								BackColor:   `red;background:url(javascript:1)`,
								TextColor:   `#fff"`,
								Align:       `left;position:absolute`,
								BorderColor: `blue;background:url(javascript:1)`,
							},
						},
					},
				},
			},
		},
	}
	out := lt.PreviewHTML()
	for _, bad := range []string{
		"javascript",
		"background:url",
		"width:80px;color:red",
		"height:30px;background",
		"font-family:Arial&quot;",
		"text-align:left;position",
		"background-color:red;background",
	} {
		if strings.Contains(out, bad) {
			t.Fatalf("PreviewHTML leaked unsafe CSS %q:\n%s", bad, out)
		}
	}
	if strings.Contains(out, `color:#fff&quot;`) {
		t.Fatalf("PreviewHTML leaked unsafe text color:\n%s", out)
	}
	if !strings.Contains(out, "border:1px solid #ccc") {
		t.Fatalf("unsafe border color should fall back to #ccc:\n%s", out)
	}
}
