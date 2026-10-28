package sheet

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Exercise the HTML parser through the shipped PDF entry, including formatting
// and the final text after many adjacent HTML fragments. The input is synthetic.
func TestPDFFragmentedRichHTML(t *testing.T) {
	for _, fragments := range []int{1, 2000} {
		t.Run(strconv.Itoa(fragments), func(t *testing.T) {
			d := NewDocument()
			d.SetColumnWidth(1, 600)
			d.GetOrCreateCell(0, 0).RichHTML = "<p>START <b>BOLD</b> <i>ITALIC</i> " +
				strings.Repeat("<span>x</span>", fragments) + " TAIL</p>"
			pdf, err := d.PDF(PDFOptions{Title: "fragmented HTML"})
			if err != nil {
				t.Fatal(err)
			}
			if len(pdf) < 1000 || !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("%%EOF")) {
				t.Fatalf("invalid PDF (%d bytes)", len(pdf))
			}
			// Decode page content, rather than accepting a header-only/blank PDF.
			contentRef := regexp.MustCompile(`/Contents ([0-9]+) 0 R`).FindSubmatch(pdf)
			if len(contentRef) != 2 {
				t.Fatal("PDF has no page contents")
			}
			start := bytes.Index(pdf, append(append([]byte("\n"), contentRef[1]...), []byte(" 0 obj\n")...))
			if start < 0 {
				t.Fatal("page contents object missing")
			}
			stream := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindSubmatch(pdf[start:])
			if len(stream) != 2 {
				t.Fatal("page contents stream missing")
			}
			reader, err := zlib.NewReader(bytes.NewReader(stream[1]))
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"START", "BOLD", "ITALIC", "TAIL"} {
				var utf16 bytes.Buffer
				for _, ch := range []byte(text) {
					utf16.WriteByte(0)
					utf16.WriteByte(ch)
				}
				if !bytes.Contains(content, utf16.Bytes()) {
					t.Fatalf("PDF lost %q", text)
				}
			}
			if bytes.Count(content, []byte{0, 'x'}) != fragments {
				t.Fatalf("PDF did not preserve %d fragments", fragments)
			}
			// Separate regular/bold/italic font selections must survive rendering.
			fonts := regexp.MustCompile(`/F[A-Za-z0-9]+ [0-9.]+ Tf`).FindAll(content, -1)
			seen := map[string]bool{}
			for _, font := range fonts {
				seen[string(font)] = true
			}
			if len(seen) < 3 {
				t.Fatalf("PDF lost font styles: %q", fonts)
			}
		})
	}
}
