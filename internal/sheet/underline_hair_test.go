package sheet

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func payloadPDFStreams(t *testing.T, data []byte) string {
	t.Helper()
	var out strings.Builder
	for _, m := range regexp.MustCompile(`stream\r?\n([\s\S]*?)\r?\nendstream`).FindAllSubmatch(data, -1) {
		r, err := zlib.NewReader(bytes.NewReader(m[1]))
		if err != nil {
			out.Write(m[1])
			continue
		}
		v, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil {
			t.Fatal("PDF stream decode failed")
		}
		out.Write(v)
	}
	return out.String()
}

func TestTabularUnderlineHairExports(t *testing.T) {
	d := NewDocument()
	c := d.GetOrCreateCell(0, 0)
	c.Text = "own header"
	c.Underline = true
	c.Border = "none"
	c.BorderBottom = "hair"
	other := d.GetOrCreateCell(1, 0)
	*other = *c
	other.Underline = false
	other.BorderBottom = "thin"
	html := d.HTML(HTMLOptions{})
	if !strings.Contains(html, "text-decoration:underline") || !strings.Contains(html, "border-bottom:0.25pt solid") {
		t.Fatal("HTML underline/hair lost")
	}
	data, err := d.XLSX()
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := book.Close(); err != nil {
			t.Error(err)
		}
	}()
	first, err := book.GetCellStyle("Лист1", "A1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := book.GetCellStyle("Лист1", "A2")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("style cache merged underline/hair")
	}
	style, err := book.GetStyle(first)
	if err != nil {
		t.Fatal(err)
	}
	if style.Font == nil || style.Font.Underline != "single" {
		t.Fatal("XLSX underline missing")
	}
	hair := false
	for _, b := range style.Border {
		if b.Type == "bottom" && b.Style == 7 {
			hair = true
		}
	}
	if !hair {
		t.Fatal("XLSX hair replaced with thin")
	}
	pdf, err := d.PDF(PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	streams := payloadPDFStreams(t, pdf)
	if !strings.Contains(streams, "0.25 w") || !strings.Contains(streams, " re f") {
		t.Fatal("PDF hair or underline missing")
	}
}
