package sheet

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strings"
	"testing"
)

// Игнорируемые закрывающие теги дробят текст на множество токенов HTML.
// Проверяем весь публичный путь печати, включая сохранение текста и начертаний.
func TestPDFRichFragmentedHTML(t *testing.T) {
	const prefix = `<p>Normal <b>Bold</b> <i>Italic</i></p><p>`
	const suffix = `</p>`
	const fragments = 4096
	render := func(t *testing.T, html string) []byte {
		t.Helper()
		d := NewDocument()
		d.SetColumnWidth(1, 600)
		d.GetOrCreateCell(0, 0).RichHTML = html
		data, err := d.PDF(PDFOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(data) < 1000 || !bytes.HasPrefix(data, []byte("%PDF-")) ||
			!bytes.Contains(data, []byte("%%EOF")) || countPages(data) == 0 {
			t.Fatal("PDF пустой или не завершён")
		}
		var contents []byte
		for _, stream := range regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(data, -1) {
			r, err := zlib.NewReader(bytes.NewReader(stream[1]))
			if err != nil {
				continue // Несжатый поток, например таблица Unicode шрифта.
			}
			plain, readErr := io.ReadAll(r)
			closeErr := r.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("PDF stream: read=%v, close=%v", readErr, closeErr)
			}
			if bytes.Contains(plain, []byte("BT ")) {
				contents = append(contents, plain...)
			}
		}
		// Текст в UTF-16BE и смена шрифта должны присутствовать в самом PDF.
		for _, text := range []string{"Normal", "Bold", "Italic"} {
			var encoded []byte
			for _, ch := range []byte(text) {
				encoded = append(encoded, 0, ch)
			}
			if !bytes.Contains(contents, encoded) {
				t.Fatalf("PDF потерял текст %q", text)
			}
		}
		fonts := regexp.MustCompile(`/F\w+ [\d.]+ Tf`).FindAll(contents, -1)
		styles := map[string]bool{}
		for _, font := range fonts {
			styles[string(font)] = true
		}
		if len(styles) < 3 {
			t.Fatalf("PDF потерял обычное/жирное/курсивное начертание: %q", fonts)
		}
		return contents
	}

	control := render(t, prefix+strings.Repeat("a", fragments)+suffix)
	fragmented := render(t, prefix+strings.Repeat("a</ignored>", fragments)+suffix)
	if !bytes.Equal(fragmented, control) {
		t.Fatal("фрагментированный HTML изменил текст или оформление страниц PDF")
	}
}
