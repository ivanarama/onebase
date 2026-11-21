package sheet

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strconv"
	"testing"
)

func tabularPDFStreams(t *testing.T, data []byte) []byte {
	t.Helper()
	var text []byte
	for _, match := range regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(data, -1) {
		r, err := zlib.NewReader(bytes.NewReader(match[1]))
		if err != nil {
			continue
		}
		part, err := io.ReadAll(io.LimitReader(r, 16<<20))
		closeErr := r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		text = append(text, part...)
	}
	return text
}

func tabularUTF16(text string) []byte {
	var data []byte
	for _, b := range []byte(text) {
		data = append(data, 0, b)
	}
	return data
}

func TestTabularExportPDFFormatsAndPrintsCollapsedRows(t *testing.T) {
	d := NewDocument()
	d.Page.Orientation = "landscape"
	head := d.GetOrCreateCell(0, 0)
	head.Text = ""
	head.Value = 0.25
	head.Format = "0.0%"
	cell := d.GetOrCreateCell(1, 0)
	cell.Text = "RAW_SENTINEL"
	cell.Value = 123.456
	cell.Format = "0.00"
	d.GetOrCreateCell(2, 0).Text = "COLLAPSED_DETAIL"
	d.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: false}}
	d.PageBreaks = []int{2}
	d.RepeatHeader = true
	d.HeaderArea = &Area{Cells: map[string]*Cell{"0,0": {Value: 0.25, Format: "0.0%"}}}
	data, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	streams := tabularPDFStreams(t, data)
	for _, want := range []string{"123.46", "COLLAPSED_DETAIL"} {
		if !bytes.Contains(streams, tabularUTF16(want)) {
			t.Fatalf("PDF missing %q", want)
		}
	}
	if bytes.Contains(streams, tabularUTF16("RAW_SENTINEL")) {
		t.Fatal("PDF used unformatted Text")
	}
	if bytes.Count(streams, tabularUTF16("25.0%")) < 2 {
		t.Fatal("formatted repeat header missing")
	}
	box := regexp.MustCompile(`/MediaBox\s*\[\s*0\s+0\s+([0-9.]+)\s+([0-9.]+)\s*\]`).FindSubmatch(data)
	if len(box) != 3 {
		t.Fatal("PDF media box missing")
	}
	w, _ := strconv.ParseFloat(string(box[1]), 64)
	h, _ := strconv.ParseFloat(string(box[2]), 64)
	if w <= h {
		t.Fatal("landscape lost")
	}
	if pages := len(regexp.MustCompile(`/Type /Page\b`).FindAll(data, -1)); pages != 2 {
		t.Fatalf("page break/repeat pages=%d", pages)
	}
	if cell.Text != "RAW_SENTINEL" || cell.Value != 123.456 || d.Groups[0].Expanded {
		t.Fatal("PDF mutated source document")
	}
}
