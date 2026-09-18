package excel

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html/charset"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

const (
	importMaxBytes = 32 << 20
	importMaxRows  = 10000
	importMaxCols  = 256
	importMaxCells = 1000000
)

var (
	errImportLimit = i18nerr.New("Excel: превышен предел импорта (32 МиБ, 10000 строк, 256 колонок, 1000000 ячеек)")
	errImportXML   = i18nerr.New("Excel: повреждённая структура книги")
)

// ImportRows reads the first worksheet as formatted text, including its header.
// Short rows are padded to the widest row; leading zeroes remain intact.
// Validate the ZIP and actual row/cell coordinates before excelize materializes
// any cells: a tiny sparse sheet can otherwise expand into a huge rectangle.
func ImportRows(filename string) ([][]string, error) {
	input, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(input, importMaxBytes+1))
	closeErr := input.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > importMaxBytes {
		return nil, errImportLimit
	}
	if err := validateImportWorkbook(data); err != nil {
		return nil, err
	}
	// Use the same immutable bytes as preflight, including for offline callers.
	// Equal unzip limits keep every XML part within the checked aggregate limit.
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit: importMaxBytes, UnzipXMLSizeLimit: importMaxBytes,
	})
	if f != nil {
		defer closeBook(f)
	}
	if err != nil {
		return nil, err
	}
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errImportXML
	}
	return readImportRows(f, sheets[0])
}

func readImportRows(f *excelize.File, sheetName string) (out [][]string, err error) {
	rows, err := f.Rows(sheetName)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			out, err = nil, closeErr
		}
	}()
	width, lastNonEmpty := 0, 0
	for rows.Next() {
		cells, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		width = max(width, len(cells))
		count := len(out) + 1
		if count > importMaxRows || width > importMaxCols || count*width > importMaxCells {
			return nil, errImportLimit
		}
		out = append(out, cells)
		if len(cells) > 0 {
			lastNonEmpty = len(out)
		}
	}
	if err := rows.Error(); err != nil {
		return nil, err
	}
	out = out[:lastNonEmpty] // Preserve GetRows' omission of trailing empty rows.
	for i, row := range out {
		if len(row) < width {
			cells := make([]string, width)
			copy(cells, row)
			out[i] = cells
		}
	}
	return out, nil
}

// Resolve the first worksheet from package relationships, not sheet1.xml or the
// optional dimension hint. Other names and absolute relationship targets are
// valid XLSX. Reject ambiguous names/IDs instead of validating a different part
// from the one excelize will read.
func validateImportWorkbook(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%w: %v", errImportXML, err)
	}
	parts := make(map[string]*zip.File, len(zr.File))
	var total uint64
	for _, part := range zr.File {
		if part.UncompressedSize64 > uint64(importMaxBytes)-total {
			return errImportLimit
		}
		total += part.UncompressedSize64
		name := strings.ReplaceAll(part.Name, "\\", "/")
		// Match excelize's two case-insensitive package aliases as well, so a
		// duplicate alias cannot replace the bytes validated below.
		switch strings.ToLower(name) {
		case "[content_types].xml":
			name = "[Content_Types].xml"
		case "xl/sharedstrings.xml":
			name = "xl/sharedStrings.xml"
		}
		if _, exists := parts[name]; exists {
			return errImportXML
		}
		parts[name] = part
	}
	var rels importRelationships
	if err := decodeImportPart(parts["_rels/.rels"], &rels); err != nil {
		return err
	}
	bookPath := ""
	for _, rel := range rels.Items {
		if rel.Type == excelize.SourceRelationshipOfficeDocument || rel.Type == "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument" {
			if bookPath != "" || rel.Mode == "External" {
				return errImportXML
			}
			bookPath = strings.TrimPrefix(rel.Target, "/")
		}
	}
	if bookPath == "" || strings.Contains(bookPath, "\\") || path.Clean(bookPath) != bookPath {
		return errImportXML
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decodeImportPart(parts[bookPath], &book); err != nil {
		return err
	}
	if len(book.Sheets) == 0 || book.Sheets[0].ID == "" {
		return errImportXML
	}
	for _, sheet := range book.Sheets[1:] {
		if strings.EqualFold(sheet.Name, book.Sheets[0].Name) {
			return errImportXML
		}
	}
	rels = importRelationships{}
	relsPath := path.Join(path.Dir(bookPath), "_rels", path.Base(bookPath)+".rels")
	if err := decodeImportPart(parts[relsPath], &rels); err != nil {
		return err
	}
	var sheetPart *zip.File
	for _, rel := range rels.Items {
		if rel.ID != book.Sheets[0].ID {
			continue
		}
		if sheetPart != nil || rel.Mode == "External" || strings.Contains(rel.Target, "\\") {
			return errImportXML
		}
		target := path.Join(path.Dir(bookPath), rel.Target)
		if strings.HasPrefix(rel.Target, "/") {
			target = strings.TrimPrefix(path.Clean(rel.Target), "/")
		}
		sheetPart = parts[target]
		if sheetPart == nil {
			return errImportXML
		}
	}
	if sheetPart == nil {
		return errImportXML
	}
	r, err := sheetPart.Open()
	if err != nil {
		return err
	}
	err = validateImportSheet(r)
	closeErr := r.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type importRelationships struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
		Mode   string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

func decodeImportPart(part *zip.File, dst any) error {
	if part == nil {
		return errImportXML
	}
	r, err := part.Open()
	if err != nil {
		return err
	}
	decoder := xml.NewDecoder(r)
	decoder.CharsetReader = charset.NewReaderLabel
	err = decoder.Decode(dst)
	if err == nil {
		for {
			token, nextErr := decoder.Token()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				err = nextErr
				break
			}
			switch v := token.(type) {
			case xml.Comment, xml.ProcInst:
				continue
			case xml.CharData:
				if strings.TrimSpace(string(v)) == "" {
					continue
				}
			}
			err = errImportXML
			break
		}
	}
	closeErr := r.Close()
	if err != nil {
		return fmt.Errorf("%w: %v", errImportXML, err)
	}
	return closeErr
}

func validateImportSheet(r io.Reader) error {
	decoder := xml.NewDecoder(r)
	decoder.CharsetReader = charset.NewReaderLabel
	var stack []string
	row, col, width := 0, 0, 0
	seenWorksheet, seenData := false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			if !seenWorksheet || !seenData {
				return errImportXML
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", errImportXML, err)
		}
		switch el := token.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			} else {
				if seenWorksheet || el.Name.Local != "worksheet" {
					return errImportXML
				}
				seenWorksheet = true
			}
			stack = append(stack, el.Name.Local)
			switch el.Name.Local {
			case "sheetData":
				if parent != "worksheet" || seenData {
					return errImportXML
				}
				seenData = true
			case "row":
				if parent != "sheetData" {
					return errImportXML
				}
				next := row + 1
				for _, attr := range el.Attr {
					if attr.Name.Local == "r" {
						next, err = strconv.Atoi(attr.Value)
						if err != nil || next <= row {
							return errImportXML
						}
					}
				}
				row, col = next, 0
			case "c":
				if parent != "row" {
					return errImportXML
				}
				next := col + 1
				for _, attr := range el.Attr {
					if attr.Name.Local == "r" {
						var cellRow int
						next, cellRow, err = excelize.CellNameToCoordinates(attr.Value)
						if err != nil || cellRow != row || next <= col {
							return errImportXML
						}
					}
				}
				col, width = next, max(width, next)
			}
			if row > importMaxRows || width > importMaxCols || row*width > importMaxCells {
				return errImportLimit
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(el)) != "" {
				return errImportXML
			}
		}
	}
}
