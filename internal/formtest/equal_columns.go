// Package formtest provides shared browser assertions for the form renderers.
package formtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// EqualColumnsYAML exercises fields, mixed children, explicit child layout,
// and a nested group whose own layout must remain vertical.
func EqualColumnsYAML() string {
	var s strings.Builder
	s.WriteString(`schema: onebase.form/v1
form:
  name: Форма
  kind: object
  entity: Клиент
elements:
`)
	for _, count := range []int{2, 3, 4} {
		fmt.Fprintf(&s, "  - kind: ГруппаФормы\n    name: Поля%d\n    orientation: horizontal\n    equal_columns: true\n    children:\n", count)
		for i := 0; i < count; i++ {
			fmt.Fprintf(&s, "      - kind: ПолеВвода\n        name: Поле%d_%d\n        data_path: Объект.Наименование\n", count, i)
		}
	}
	s.WriteString(`  - kind: ГруппаФормы
    name: Смешанная
    orientation: horizontal
    equal_columns: true
    children:
      - kind: ПолеВвода
        name: УзкоеПоле
        data_path: Объект.Наименование
        width: 70
        halign: right
      - kind: Кнопка
        name: Действие
        width: 400
      - kind: Флажок
        name: Активен
        data_path: Объект.Активен
      - kind: ГруппаФормы
        name: Вложенная
        children:
          - kind: ПолеВвода
            name: ВнутреннееПоле
            data_path: Объект.Наименование
`)
	return s.String()
}

// AssertEqualColumns measures production HTML with the browser's CSS engine.
// Each row must fill its container and have equal widths; narrow containers
// must wrap without overflow. No test CSS changes the children themselves.
func AssertEqualColumns(t *testing.T, browser, page string) {
	t.Helper()
	const script = `<script>
window.addEventListener('load', () => {
 const groups = [...document.querySelectorAll('.ob-equal-columns')];
 const samples = [];
 for (const width of [1000,420,160]) {
  for (const group of groups) {
   const body = group.querySelector(':scope > .managed-group-body,:scope > .fc-children,:scope > .group-body');
   group.style.maxWidth='none'; group.style.width=(width+60)+'px';
   body.style.width=width+'px';
   const items=[...body.children].filter(el=>!el.classList.contains('fc-drop') && getComputedStyle(el).display!=='none');
   const rect=body.getBoundingClientRect();
   samples.push({width,scroll:body.scrollWidth,items:items.map(el=>{const r=el.getBoundingClientRect();return {left:r.left-rect.left,width:r.width,right:r.right-rect.left};})});
  }
 }
 const out=document.createElement('pre');out.id='equal-columns-measure';out.textContent=JSON.stringify({groups:groups.length,samples});document.body.appendChild(out);
});
</script>`
	end := strings.LastIndex(page, "</body>")
	if end < 0 {
		t.Fatal("HTML has no body")
	}
	page = page[:end] + script + page[end:]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-extensions", "--no-first-run", "--user-data-dir=" + t.TempDir(), "--dump-dom", server.URL}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(t.Context(), browser, args...) //nolint:gosec // browser selected from existing test allow-list
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dump, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v: %s", err, stderr.String())
	}
	marker := []byte(`<pre id="equal-columns-measure">`)
	start := bytes.Index(dump, marker)
	if start < 0 {
		t.Fatalf("browser did not measure: %s", stderr.String())
	}
	start += len(marker)
	end = bytes.Index(dump[start:], []byte("</pre>"))
	if end < 0 {
		t.Fatal("measurement missing end")
	}
	var result struct {
		Groups  int `json:"groups"`
		Samples []struct {
			Width  float64 `json:"width"`
			Scroll float64 `json:"scroll"`
			Items  []struct{ Left, Width, Right float64 }
		}
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(string(dump[start:start+end]))), &result); err != nil {
		t.Fatal(err)
	}
	if result.Groups != 4 || len(result.Samples) != 12 {
		t.Fatalf("expected 4 groups at 3 widths, got %+v", result)
	}
	for n, sample := range result.Samples {
		if sample.Scroll > sample.Width+1 {
			t.Errorf("sample %d: content overflows %.2fpx container (scrollWidth=%.2f)", n, sample.Width, sample.Scroll)
		}
		wantCount := []int{2, 3, 4, 4}[n%4]
		if len(sample.Items) != wantCount {
			t.Fatalf("sample %d: children=%d, want %d", n, len(sample.Items), wantCount)
		}
		for _, item := range sample.Items {
			if item.Width < 1 || item.Left < -1 || item.Right > sample.Width+1 {
				t.Errorf("sample %d overflow/empty: %+v", n, item)
			}
		}
		// Buttons/checkboxes have top margins. Determine rows by horizontal reset
		// instead of the content's top edge, retaining each renderer's vertical style.
		var row []int
		rowCount := 0
		checkRow := func() {
			if len(row) == 0 {
				return
			}
			rowCount++
			want := (sample.Width - float64(len(row)-1)*12) / float64(len(row))
			for _, i := range row {
				if math.Abs(sample.Items[i].Width-want) > 1 {
					t.Errorf("sample %d row %v: width %.2f, want %.2f", n, row, sample.Items[i].Width, want)
				}
			}
		}
		for i, item := range sample.Items {
			if i > 0 && item.Left < sample.Items[i-1].Left+1 {
				checkRow()
				row = nil
			}
			row = append(row, i)
		}
		checkRow()
		capacity := max(1, int((sample.Width+12)/192))
		wantRows := (wantCount + capacity - 1) / capacity
		if rowCount != wantRows {
			t.Errorf("sample %d: rows=%d, want %d", n, rowCount, wantRows)
		}
	}
}
