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
    picture: layout-test.svg
    width: 80
    height: 60
    halign: center
  - kind: ПолеКартинки
    name: КартинкаСправа
    picture: layout-test.svg
    width: 16
    height: 48
    halign: right
  - kind: ПолеКартинки
    name: КартинкаРастянуть
    picture: layout-test.svg
    width: 80
    height: 60
    halign: stretch
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
		`[data-node-id="elements.0"]`, `[data-node-id="elements.1"]`, `[data-node-id="elements.2"]`, `.fc-pic-image`), "canvas")

	preview := renderManagedFormPreview(&metadata.FormModule{
		EntityName: "Контрагент",
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementPicture, Name: "КартинкаЦентр", Picture: "layout-test.svg", Width: 80, Height: 60, HorizontalAlign: "center"},
			{Kind: metadata.FormElementPicture, Name: "КартинкаСправа", Picture: "layout-test.svg", Width: 16, Height: 48, HorizontalAlign: "right"},
			{Kind: metadata.FormElementPicture, Name: "КартинкаРастянуть", Picture: "layout-test.svg", Width: 80, Height: 60, HorizontalAlign: "stretch"},
		},
	}, nil)
	assertDesignerPictureGeometry(t, runDesignerLayoutBrowser(t, browser, preview,
		`[data-preview-picture="КартинкаЦентр"]`, `[data-preview-picture="КартинкаСправа"]`, `[data-preview-picture="КартинкаРастянуть"]`, `.form-picture-image`), "preview")
}

type designerPictureGeometry struct {
	CenterDelta  float64 `json:"centerDelta"`
	RightGap     float64 `json:"rightGap"`
	CenterWidth  float64 `json:"centerWidth"`
	CenterHeight float64 `json:"centerHeight"`
	RightWidth   float64 `json:"rightWidth"`
	RightHeight  float64 `json:"rightHeight"`
	StretchRatio float64 `json:"stretchRatio"`
}

func assertDesignerPictureGeometry(t *testing.T, got designerPictureGeometry, view string) {
	t.Helper()
	if got.CenterDelta > 3 {
		t.Errorf("%s: картинка не центрирована: delta=%.1fpx", view, got.CenterDelta)
	}
	if got.RightGap > 3 {
		t.Errorf("%s: картинка не у правого края: gap=%.1fpx", view, got.RightGap)
	}
	// Asset имеет intrinsic 40x20. Ограничения 80x60 не должны растягивать его
	// до прямоугольника настройки: runtime использует max-width/max-height.
	if got.CenterWidth < 39 || got.CenterWidth > 41 || got.CenterHeight < 19 || got.CenterHeight > 21 {
		t.Errorf("%s: intrinsic-картинка растянута вместо max-size: %.1fx%.1f, ожидался 40x20", view, got.CenterWidth, got.CenterHeight)
	}
	// max-width:16 сжимает тот же asset пропорционально до 16x8; height:48 не
	// превращает ограничение в принудительный размер и не ломает aspect ratio.
	if got.RightWidth < 15 || got.RightWidth > 17 || got.RightHeight < 7 || got.RightHeight > 9 {
		t.Errorf("%s: max-size картинки не сохранил пропорции: %.1fx%.1f, ожидался 16x8", view, got.RightWidth, got.RightHeight)
	}
	if got.StretchRatio < 0.95 || got.StretchRatio > 1.05 {
		t.Errorf("%s: halign:stretch не растянул обёртку картинки: ratio=%.2f", view, got.StretchRatio)
	}
}

func runDesignerLayoutBrowser(t *testing.T, browser, page, centerSelector, rightSelector, stretchSelector, innerSelector string) designerPictureGeometry {
	t.Helper()
	script := `<script>
window.addEventListener('load', () => {
  const center = document.querySelector('` + centerSelector + `');
  const right = document.querySelector('` + rightSelector + `');
  const stretch = document.querySelector('` + stretchSelector + `');
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
    rightHeight: rightInner.height,
    stretchRatio: stretch && host ? box(stretch).width / (contentRight - contentLeft) : 0
  };
  const out = document.createElement('pre');
  out.id = 'designer-layout-measure';
  out.textContent = JSON.stringify(result);
  document.body.appendChild(out);
});
</script>`
	endBody := strings.LastIndex(page, "</body>")
	if endBody < 0 {
		t.Fatal("страница не содержит </body>")
	}
	page = page[:endBody] + script + page[endBody:]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static/forms/layout-test.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="red"/></svg>`))
			return
		}
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
