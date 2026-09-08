package launcher

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// TestDesignerPictureLayout_BrowserGeometry проверяет вычисленную браузером
// геометрию обоих представлений конструктора. Проверки HTML-строк не ловят
// width:auto у блочной обёртки: auto-margin присутствует, но картинка остаётся
// шириной во всю строку и визуально не двигается.
func TestDesignerPictureLayout_BrowserGeometry(t *testing.T) {
	browser := designerLayoutTestBrowser(t)

	const sample = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
elements:
  - kind: ПолеКартинки
    name: КартинкаЦентр
    width: 80
    height: 60
    halign: center
  - kind: ПолеКартинки
    name: КартинкаСправа
    width: 64
    height: 48
    halign: right
`
	doc, err := formdoc.Load([]byte(sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	canvas, err := renderFormCanvas(doc, "")
	if err != nil {
		t.Fatalf("renderFormCanvas: %v", err)
	}
	editor := renderFormsEditorHTML(t)
	styles := strings.Join(regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`).FindAllString(editor, -1), "\n")
	if styles == "" || !strings.Contains(styles, ".fc-pic-wrap") {
		t.Fatal("production CSS холста не найден")
	}
	canvasPage := `<!doctype html><html><head><meta charset="utf-8">` + styles + `</head><body><div id="canvas-host">` + canvas + `</div></body></html>`
	assertDesignerPictureGeometry(t, runDesignerLayoutBrowser(t, browser, canvasPage,
		`[data-node-id="elements.0"]`, `[data-node-id="elements.1"]`, `.fc-pic`), "canvas")

	preview := renderManagedFormPreview(&metadata.FormModule{
		EntityName: "Контрагент",
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementPicture, Name: "КартинкаЦентр", Width: 80, Height: 60, HorizontalAlign: "center"},
			{Kind: metadata.FormElementPicture, Name: "КартинкаСправа", Width: 64, Height: 48, HorizontalAlign: "right"},
		},
	}, nil)
	assertDesignerPictureGeometry(t, runDesignerLayoutBrowser(t, browser, preview,
		`[data-preview-picture="КартинкаЦентр"]`, `[data-preview-picture="КартинкаСправа"]`, `.form-picture-placeholder`), "preview")
}

type designerPictureGeometry struct {
	CenterDelta  float64 `json:"centerDelta"`
	RightGap     float64 `json:"rightGap"`
	CenterWidth  float64 `json:"centerWidth"`
	CenterHeight float64 `json:"centerHeight"`
	RightWidth   float64 `json:"rightWidth"`
	RightHeight  float64 `json:"rightHeight"`
}

func assertDesignerPictureGeometry(t *testing.T, got designerPictureGeometry, view string) {
	t.Helper()
	if got.CenterDelta > 3 {
		t.Errorf("%s: картинка не центрирована: delta=%.1fpx", view, got.CenterDelta)
	}
	if got.RightGap > 3 {
		t.Errorf("%s: картинка не у правого края: gap=%.1fpx", view, got.RightGap)
	}
	if got.CenterWidth < 79 || got.CenterWidth > 81 || got.CenterHeight < 59 || got.CenterHeight > 61 {
		t.Errorf("%s: размер центрированной картинки %.1fx%.1f, ожидался 80x60", view, got.CenterWidth, got.CenterHeight)
	}
	if got.RightWidth < 63 || got.RightWidth > 65 || got.RightHeight < 47 || got.RightHeight > 49 {
		t.Errorf("%s: размер правой картинки %.1fx%.1f, ожидался 64x48", view, got.RightWidth, got.RightHeight)
	}
}

func runDesignerLayoutBrowser(t *testing.T, browser, page, centerSelector, rightSelector, innerSelector string) designerPictureGeometry {
	t.Helper()
	script := `<script>
(() => {
  const center = document.querySelector('` + centerSelector + `');
  const right = document.querySelector('` + rightSelector + `');
  const host = right && right.parentElement;
  const inner = (el) => el && el.querySelector('` + innerSelector + `');
  const box = (el) => el ? el.getBoundingClientRect() : {left:1000000,right:0,width:0,height:0};
  const hostBox = box(host);
  const hostStyle = host ? getComputedStyle(host) : null;
  const contentLeft = hostBox.left + (hostStyle ? parseFloat(hostStyle.paddingLeft) || 0 : 0);
  const contentRight = hostBox.right - (hostStyle ? parseFloat(hostStyle.paddingRight) || 0 : 0);
  const centerBox = box(center);
  const rightBox = box(right);
  const centerInner = box(inner(center));
  const rightInner = box(inner(right));
  const result = {
    centerDelta: Math.abs((contentLeft + contentRight) / 2 - (centerBox.left + centerBox.right) / 2),
    rightGap: Math.abs(contentRight - rightBox.right),
    centerWidth: centerInner.width,
    centerHeight: centerInner.height,
    rightWidth: rightInner.width,
    rightHeight: rightInner.height
  };
  const out = document.createElement('pre');
  out.id = 'designer-layout-measure';
  out.textContent = JSON.stringify(result);
  document.body.appendChild(out);
})();
</script>`
	endBody := strings.LastIndex(page, "</body>")
	if endBody < 0 {
		t.Fatal("страница не содержит </body>")
	}
	page = page[:endBody] + script + page[endBody:]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(server.Close)

	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + t.TempDir(), "--dump-dom", server.URL}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(t.Context(), browser, args...) //nolint:gosec // test-only browser из закрытого allow-list
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dumped, err := cmd.Output()
	if err != nil {
		t.Fatalf("headless browser: %v\n%s", err, stderr.String())
	}
	const marker = `<pre id="designer-layout-measure">`
	start := bytes.Index(dumped, []byte(marker))
	if start < 0 {
		t.Fatalf("браузер не выполнил измерение:\n%s\n%s", stderr.String(), string(dumped))
	}
	start += len(marker)
	end := bytes.Index(dumped[start:], []byte(`</pre>`))
	if end < 0 {
		t.Fatalf("результат измерения не закрыт: %s", string(dumped[start:]))
	}
	var got designerPictureGeometry
	if err := json.Unmarshal([]byte(html.UnescapeString(string(dumped[start:start+end]))), &got); err != nil {
		t.Fatalf("разобрать измерение: %v: %s", err, string(dumped[start:start+end]))
	}
	return got
}

func designerLayoutTestBrowser(t *testing.T) string {
	t.Helper()
	var fixed []string
	switch runtime.GOOS {
	case "windows":
		fixed = []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
	case "darwin":
		fixed = []string{
			`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`,
			`/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge`,
		}
	}
	for _, candidate := range fixed {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	for _, name := range []string{"msedge", "microsoft-edge", "google-chrome", "chromium", "chromium-browser"} {
		if candidate, err := exec.LookPath(name); err == nil {
			return filepath.Clean(candidate)
		}
	}
	t.Skip("Chromium/Edge не найден: фактическая геометрия проверяется на test-windows")
	return ""
}
