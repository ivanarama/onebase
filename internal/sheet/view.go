package sheet

import (
 "fmt"
 "math"
 "strconv"
 "strings"
)

// Reference is an inert typed drilldown, never an arbitrary URL or command.
type Reference struct { Entity, ID string }
type RowGroup struct { Start, HeaderEnd, End, Level int; Title string; Expanded bool }

func (d *Document) ResolvedGroups() []RowGroup {
 maxRow,_:=d.ContentBounds()
 groups:=append([]RowGroup(nil),d.Groups...)
 for i:=range groups {
  groups[i].End=maxRow
  for j:=i+1;j<len(groups);j++ {if groups[j].Level<=groups[i].Level {groups[i].End=groups[j].Start-1;break}}
 }
 return groups
}

// Snapshot detaches mutable builder cells, dimensions and areas from the viewer.
func (d *Document) Snapshot() *Document {
 out:=*d
 out.Cells=make(map[CellKey]*Cell,len(d.Cells))
 for k,c:=range d.Cells {if c!=nil {v:=*c;if c.Drilldown!=nil {r:=*c.Drilldown;v.Drilldown=&r};out.Cells[k]=&v}}
 out.ColWidths=make(map[int]float64,len(d.ColWidths));for k,v:=range d.ColWidths {out.ColWidths[k]=v}
 out.RowHeights=make(map[int]float64,len(d.RowHeights));for k,v:=range d.RowHeights {out.RowHeights[k]=v}
 out.Groups=append([]RowGroup(nil),d.Groups...);out.PageBreaks=append([]int(nil),d.PageBreaks...)
 if d.HeaderArea!=nil {a:=*d.HeaderArea;a.Cells=make(map[string]*Cell,len(d.HeaderArea.Cells));for k,c:=range d.HeaderArea.Cells {if c!=nil {v:=*c;a.Cells[k]=&v}};out.HeaderArea=&a}
 return &out
}

// DisplayText preserves numeric Value for export; formatting only affects text.
// The supported 1C subset is ЧДЦ, ЧГ, ЧН and conventional Excel decimal/% masks.
func (c *Cell) DisplayText() string {
 if c.Format=="" {return c.Text}
 v,err:=strconv.ParseFloat(fmt.Sprint(c.Value),64);if err!=nil||math.IsNaN(v)||math.IsInf(v,0) {return c.Text}
 precision:=0; percent:=strings.Contains(c.Format,"%")
 format:=strings.ReplaceAll(c.Format," ","")
 if strings.Contains(format,"ЧДЦ=") {for _,p:=range strings.Split(format,";"){if strings.HasPrefix(p,"ЧДЦ="){precision,_=strconv.Atoi(strings.TrimPrefix(p,"ЧДЦ="))}}} else if dot:=strings.IndexByte(format,'.');dot>=0 {for _,r:=range format[dot+1:] {if r=='0'||r=='#' {precision++} else {break}}}
 if precision<0||precision>15 {return c.Text}
 if percent {v*=100}
 text:=strconv.FormatFloat(v,'f',precision,64)
 if strings.Contains(c.Format,"Ч") {text=strings.ReplaceAll(text,".",",")}
 if percent {text+="%"}
 return text
}

func (c *Cell) ExcelFormat() string {
 if !strings.Contains(c.Format,"Ч") {return c.Format}
 digits:=0
 for _,p:=range strings.Split(strings.ReplaceAll(c.Format," ",""),";"){if strings.HasPrefix(p,"ЧДЦ="){digits,_=strconv.Atoi(strings.TrimPrefix(p,"ЧДЦ="))}}
 if digits<0||digits>15 {return "General"}
 f:="0";if digits>0 {f+="."+strings.Repeat("0",digits)};return f
}
