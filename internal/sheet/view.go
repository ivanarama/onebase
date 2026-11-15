package sheet

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// Reference is an inert typed drilldown, never an arbitrary URL or command.
type Reference struct{ Entity, ID string }
type RowGroup struct {
	Closed                       bool
	Start, HeaderEnd, End, Level int
	Title                        string
	Expanded                     bool
}

func (d *Document) ResolvedGroups() []RowGroup {
	maxRow, _ := d.ContentBounds()
	groups := append([]RowGroup(nil), d.Groups...)
	for i := range groups {
		if !groups[i].Closed {
			groups[i].End = maxRow
		}
		for j := i + 1; j < len(groups); j++ {
			if groups[j].Level <= groups[i].Level && groups[j].Start <= groups[i].End {
				groups[i].End = groups[j].Start - 1
				break
			}
		}
	}
	return groups
}

// Snapshot detaches mutable builder cells, dimensions and areas from the viewer.
func (d *Document) Snapshot() *Document {
	out := *d
	out.Cells = make(map[CellKey]*Cell, len(d.Cells))
	for k, c := range d.Cells {
		if c != nil {
			v := *c
			if c.Drilldown != nil {
				r := *c.Drilldown
				v.Drilldown = &r
			}
			out.Cells[k] = &v
		}
	}
	out.ColWidths = make(map[int]float64, len(d.ColWidths))
	for k, v := range d.ColWidths {
		out.ColWidths[k] = v
	}
	out.RowHeights = make(map[int]float64, len(d.RowHeights))
	for k, v := range d.RowHeights {
		out.RowHeights[k] = v
	}
	out.Groups = append([]RowGroup(nil), d.Groups...)
	out.PageBreaks = append([]int(nil), d.PageBreaks...)
	if d.HeaderArea != nil {
		a := *d.HeaderArea
		a.Cells = make(map[string]*Cell, len(d.HeaderArea.Cells))
		for k, c := range d.HeaderArea.Cells {
			if c != nil {
				v := *c
				if c.Drilldown != nil {
					ref := *c.Drilldown
					v.Drilldown = &ref
				}
				a.Cells[k] = &v
			}
		}
		out.HeaderArea = &a
	}
	return &out
}

// DisplayText preserves numeric Value for export; formatting only affects text.
// The supported 1C subset is ЧДЦ, ЧГ, ЧН and conventional Excel decimal/% masks.
type numberStyle struct {
	digits, minimum          int
	group, percent           bool
	decimal, separator, zero string
	hasZero                  bool
}

func parseNumberStyle(format string) numberStyle {
	st := numberStyle{decimal: ".", separator: ","}
	if strings.Contains(format, "Ч") {
		options := map[string]string{}
		for _, part := range strings.Split(format, ";") {
			key, value, ok := strings.Cut(part, "=")
			if ok {
				options[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "'\"")
			}
		}
		st.digits = 2
		st.minimum = 2
		st.decimal = ","
		st.separator = " "
		st.group = options["ЧГ"] != "0"
		if v, ok := options["ЧДЦ"]; ok {
			st.digits, _ = strconv.Atoi(v)
			st.minimum = st.digits
		}
		if v, ok := options["ЧРД"]; ok {
			st.decimal = v
		}
		if v, ok := options["ЧРГ"]; ok {
			st.separator = v
		}
		st.zero, st.hasZero = options["ЧН"]
	} else {
		st.group = strings.Contains(format, "#,##")
		st.percent = strings.Contains(format, "%")
		if dot := strings.IndexByte(format, '.'); dot >= 0 {
			for _, r := range format[dot+1:] {
				if r == '0' || r == '#' {
					st.digits++
					if r == '0' {
						st.minimum++
					}
				} else {
					break
				}
			}
		}
	}
	return st
}

func (c *Cell) DisplayText() string {
	if c.Format == "" || c.Format == "General" {
		return c.Text
	}
	value, err := decimal.NewFromString(fmt.Sprint(c.Value))
	if err != nil {
		return c.Text
	}
	st := parseNumberStyle(c.Format)
	if st.digits < 0 || st.digits > 15 {
		return c.Text
	}
	if value.IsZero() && st.hasZero {
		return st.zero
	}
	if st.percent {
		value = value.Mul(decimal.NewFromInt(100))
	}
	text := value.StringFixed(int32(st.digits))
	integral, fraction, _ := strings.Cut(text, ".")
	for len(fraction) > st.minimum && strings.HasSuffix(fraction, "0") {
		fraction = strings.TrimSuffix(fraction, "0")
	}
	if st.group {
		sign := ""
		if strings.HasPrefix(integral, "-") {
			sign = "-"
			integral = integral[1:]
		}
		var parts []string
		for len(integral) > 3 {
			parts = append([]string{integral[len(integral)-3:]}, parts...)
			integral = integral[:len(integral)-3]
		}
		parts = append([]string{integral}, parts...)
		integral = sign + strings.Join(parts, st.separator)
	}
	text = integral
	if fraction != "" {
		text += st.decimal + fraction
	}
	if st.percent {
		text += "%"
	}
	return text
}

func (c *Cell) ExcelFormat() string {
	if !strings.Contains(c.Format, "Ч") {
		return c.Format
	}
	st := parseNumberStyle(c.Format)
	if st.digits < 0 || st.digits > 15 {
		return "General"
	}
	f := "0"
	if st.group {
		f = "#,##0"
	}
	if st.digits > 0 {
		f += "." + strings.Repeat("0", st.digits)
	}
	if st.hasZero {
		f += ";-" + f + ";\"" + strings.ReplaceAll(st.zero, "\"", "\"\"") + "\""
	}
	return f
}
