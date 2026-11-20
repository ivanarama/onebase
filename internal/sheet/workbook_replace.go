package sheet

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/xuri/excelize/v2"
)

const MaxUpdateXLSXBytes = 32 << 20

const workbookRels = "xl/_rels/workbook.xml.rels"

const emptyWorksheet = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"/></sheetViews><sheetData/></worksheet>`

// ReplaceXLSX renders snapshots into the existing workbook's style and string
// tables. Existing tabs keep their identity, position, names and visibility.
func (b *Workbook) ReplaceXLSX(src io.Reader) (data []byte, err error) {
	if len(b.sheets) == 0 {
		return nil, fmt.Errorf("добавьте в книгу хотя бы один лист")
	}
	original, err := io.ReadAll(io.LimitReader(src, MaxUpdateXLSXBytes+1))
	if err != nil {
		return nil, err
	}
	if len(original) > MaxUpdateXLSXBytes {
		return nil, fmt.Errorf("книга XLSX превышает 32 МиБ")
	}
	// The library can panic on malformed input. Nothing has been written to
	// the destination while decoding, rendering, or closing the workbook.
	defer func() {
		if recover() != nil {
			data, err = nil, fmt.Errorf("не удалось обработать повреждённую книгу XLSX")
		}
	}()
	f, err := excelize.OpenReader(bytes.NewReader(original), excelize.Options{
		UnzipSizeLimit: 256 << 20, UnzipXMLSizeLimit: 256 << 20,
	})
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть книгу XLSX: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			data, err = nil, errors.Join(err, closeErr)
		}
	}()
	names := f.GetSheetList()
	if len(names) == 0 {
		return nil, fmt.Errorf("в исходной книге XLSX нет листов")
	}
	before, err := workbookWorksheetParts(f)
	if err != nil {
		return nil, err
	}
	keep := make(map[string][]byte, len(before))
	for name, part := range before {
		raw, ok := f.Pkg.Load(part)
		if !ok {
			return nil, fmt.Errorf("в исходной книге отсутствует лист %q", name)
		}
		partXML := raw.([]byte)
		var element struct{ XMLName xml.Name }
		if err := xml.Unmarshal(partXML, &element); err != nil || element.XMLName.Local != "worksheet" {
			return nil, fmt.Errorf("исходный лист %q повреждён", name)
		}
		keep[part] = partXML
	}
	for _, sh := range b.sheets {
		index, err := f.GetSheetIndex(sh.name)
		if err != nil {
			return nil, err
		}
		if index < 0 {
			if _, err := f.NewSheet(sh.name); err != nil {
				return nil, err
			}
		} else if names[index] != sh.name {
			return nil, fmt.Errorf("имя %q отличается регистром от существующего листа %q", sh.name, names[index])
		}
	}
	parts, err := workbookWorksheetParts(f)
	if err != nil {
		return nil, err
	}
	outlines := map[string]xlsxSheetOutline{}
	for _, sh := range b.sheets {
		p := parts[sh.name]
		if p == "" {
			return nil, fmt.Errorf("вкладка %q не является листом с ячейками", sh.name)
		}
		if before[sh.name] != "" {
			delete(keep, p)
			f.Pkg.Store(p, []byte(emptyWorksheet))
			f.Sheet.Delete(p)
			rels := path.Join(path.Dir(p), "_rels", path.Base(p)+".rels")
			f.Relationships.Delete(rels)
			f.Pkg.Delete(rels)
			for _, defined := range f.GetDefinedName() {
				if defined.Scope == sh.name && (defined.Name == "_xlnm.Print_Titles" || defined.Name == "_xlnm.Print_Area" || defined.Name == "_xlnm._FilterDatabase") {
					if err := f.DeleteDefinedName(&defined); err != nil {
						return nil, err
					}
				}
			}
		}
		collapsed, level, err := sh.doc.writeXLSXSheet(f, sh.name)
		if err != nil {
			return nil, fmt.Errorf("не удалось записать лист %q: %w", sh.name, err)
		}
		if level > 0 {
			outlines[p] = xlsxSheetOutline{collapsed: collapsed, level: level}
		}
	}
	// Re-read references on open, including formulas on unchanged tabs that
	// depend on a value replaced by this operation.
	full := true
	if err := f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &full}); err != nil {
		return nil, err
	}
	if err := discardCalculationChain(f); err != nil {
		return nil, err
	}
	// Keep the original worksheet XML, even if Excelize imported a view while
	// preparing a sheet. Shared styles and strings are extended by the writer.
	for p, original := range keep {
		f.Sheet.Delete(p)
		f.Pkg.Store(p, original)
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	if len(outlines) > 0 {
		return xlsxWorkbookOutlineXML(buf.Bytes(), outlines)
	}
	return buf.Bytes(), nil
}

type workbookRelationship struct {
	ID         string `xml:"Id,attr"`
	Target     string `xml:"Target,attr"`
	Type       string `xml:"Type,attr"`
	TargetMode string `xml:"TargetMode,attr,omitempty"`
}

type workbookRelationships struct {
	XMLName xml.Name               `xml:"http://schemas.openxmlformats.org/package/2006/relationships Relationships"`
	Items   []workbookRelationship `xml:"Relationship"`
}

func relationshipsForWorkbook(f *excelize.File) (rels workbookRelationships, err error) {
	raw, ok := f.Relationships.Load(workbookRels)
	if !ok {
		return rels, fmt.Errorf("в исходной книге отсутствуют связи листов")
	}
	data, err := xml.Marshal(raw)
	if err != nil {
		return rels, err
	}
	err = xml.Unmarshal(data, &rels)
	return rels, err
}

func workbookWorksheetParts(f *excelize.File) (map[string]string, error) {
	rels, err := relationshipsForWorkbook(f)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]workbookRelationship, len(rels.Items))
	for _, rel := range rels.Items {
		byID[rel.ID] = rel
	}
	parts := make(map[string]string)
	seen := make(map[string]bool)
	for _, sh := range f.WorkBook.Sheets.Sheet {
		rel := byID[sh.ID]
		if rel.Target == "" || rel.TargetMode == "External" {
			return nil, fmt.Errorf("в исходной книге отсутствует связь листа %q", sh.Name)
		}
		if !strings.HasSuffix(rel.Type, "/worksheet") {
			continue
		}
		p := relationshipPart(rel.Target)
		if seen[p] {
			return nil, fmt.Errorf("несколько листов ссылаются на одну часть XLSX")
		}
		parts[sh.Name] = p
		seen[p] = true
	}
	return parts, nil
}

func relationshipPart(target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return path.Join("xl", target)
}

func discardCalculationChain(f *excelize.File) error {
	rels, err := relationshipsForWorkbook(f)
	if err != nil {
		return err
	}
	found := false
	rels.Items = slices.DeleteFunc(rels.Items, func(rel workbookRelationship) bool {
		if !strings.HasSuffix(rel.Type, "/calcChain") {
			return false
		}
		found = true
		part := relationshipPart(rel.Target)
		f.Pkg.Delete(part)
		for i := len(f.ContentTypes.Overrides) - 1; i >= 0; i-- {
			if f.ContentTypes.Overrides[i].PartName == "/"+part {
				f.ContentTypes.Overrides = slices.Delete(f.ContentTypes.Overrides, i, i+1)
			}
		}
		return true
	})
	if !found {
		return nil
	}
	data, err := xml.Marshal(rels)
	if err != nil {
		return err
	}
	f.CalcChain = nil
	f.Relationships.Delete(workbookRels)
	f.Pkg.Store(workbookRels, append([]byte(xml.Header), data...))
	return nil
}
