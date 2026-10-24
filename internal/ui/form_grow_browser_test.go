package ui

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
)

// grow: true — поле горизонтальной группы занимает остаток строки рядом с
// кнопкой и растягивается на всю строку, когда кнопка скрыта (hidden_when
// прячет элемент display:none). Форма — из YAML штатным обработчиком,
// геометрия — в настоящем браузере.
func TestManagedElementGrow_Browser(t *testing.T) {
	browser := managedLayoutTestBrowser(t)
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\nelements:\n" +
		"  - kind: ГруппаФормы\n    name: ГруппаРеклама\n    orientation: horizontal\n    width: 900\n    children:\n" +
		"      - kind: ПолеВвода\n        name: ПолеРеклама\n        data_path: Объект.Реклама\n        grow: true\n" +
		"      - kind: Кнопка\n        name: КнПодборРеклама\n        title:\n          ru: Реклама по номеру…\n"
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Звонок")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Звонок", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Реклама", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	rec := httptest.NewRecorder()
	srv.form(rec, reqWithChi(http.MethodGet, "/ui/catalog/звонок/new", nil, map[string]string{"entity": "звонок"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	if !strings.Contains(page, "flex:1 1 0;min-width:0;") {
		t.Fatal("grow не попал в стиль поля")
	}
	const script = `<script>
window.addEventListener('load', () => {
 const field = document.querySelector('[data-ob-el="ПолеРеклама"]');
 const button = document.querySelector('[data-ob-el="КнПодборРеклама"]');
 const body = field.parentElement;
 const w = (el) => el.getBoundingClientRect();
 const shown = {field: w(field).width, fieldTop: w(field).top, button: w(button).width, buttonTop: w(button).top, row: w(body).width};
 button.style.display = 'none';
 const hidden = {field: w(field).width, row: w(body).width};
 const out = document.createElement('pre'); out.id = 'grow-measure';
 out.textContent = JSON.stringify({shown, hidden}); document.body.appendChild(out);
});
</script>`
	end := strings.LastIndex(page, "</body>")
	page = page[:end] + script + page[end:]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-extensions", "--no-first-run",
		"--window-size=1440,784", "--user-data-dir=" + t.TempDir(), "--dump-dom", server.URL}
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
	marker := []byte(`<pre id="grow-measure">`)
	start := bytes.Index(dump, marker)
	if start < 0 {
		t.Fatalf("browser did not measure: %s", stderr.String())
	}
	start += len(marker)
	stop := bytes.Index(dump[start:], []byte("</pre>"))
	var m struct {
		Shown struct {
			Field, FieldTop, Button, ButtonTop, Row float64
		}
		Hidden struct{ Field, Row float64 }
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(string(dump[start:start+stop]))), &m); err != nil {
		t.Fatal(err)
	}
	if d := m.Shown.FieldTop - m.Shown.ButtonTop; d > 30 || d < -30 {
		t.Errorf("поле и кнопка не в одной строке: %+v", m.Shown)
	}
	if m.Shown.Field+m.Shown.Button < m.Shown.Row-40 {
		t.Errorf("поле не заняло остаток строки рядом с кнопкой: %+v", m.Shown)
	}
	if m.Hidden.Field < m.Hidden.Row-2 {
		t.Errorf("при скрытой кнопке поле не растянулось на строку: %+v", m.Hidden)
	}
}
