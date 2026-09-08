package ui

import (
	"bytes"
	"context"
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

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// TestManagedLayout_BrowserPositionsAndFillsControls проверяет не наличие CSS-
// строки, а вычисленную браузером геометрию production-страницы. Именно такой
// прогон ловит две исходные ошибки: auto-margin у inline-button не сдвигает его,
// а высокий flex-контейнер со ссылкой оставляет select однострочным.
func TestManagedLayout_BrowserPositionsAndFillsControls(t *testing.T) {
	browser := managedLayoutTestBrowser(t)

	contractors := &metadata.Entity{
		Name:   "Контрагенты",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	clients := &metadata.Entity{
		Name: "Клиенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагенты", RefEntity: contractors.Name},
			{Name: "Файл", Type: metadata.FieldTypeString},
		},
	}
	clients.Forms = []*metadata.FormModule{{
		Name:       "Форма",
		EntityName: clients.Name,
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementButton, Name: "КнопкаСправа", HorizontalAlign: "right"},
			{Kind: metadata.FormElementButton, Name: "КнопкаЦентр", HorizontalAlign: "center"},
			{Kind: metadata.FormElementPicture, Name: "КартинкаСправа", Picture: "layout-test.svg", Width: 40, Height: 40, HorizontalAlign: "right"},
			{Kind: metadata.FormElementPicture, Name: "КартинкаРастянуть", Picture: "layout-test.svg", Width: 40, Height: 40, HorizontalAlign: "stretch"},
			{Kind: metadata.FormElementField, Name: "Ссылка", DataPath: "Объект.Контрагент", Height: 180},
			{Kind: metadata.FormElementField, Name: "Путь", DataPath: "Объект.Файл", Type: "file", Height: 180},
		},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{contractors, clients})

	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/клиенты/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиенты")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}

	const measureScript = `<script>
(() => {
  const rightButton = document.querySelector('[data-ob-el="КнопкаСправа"]');
  const centerButton = document.querySelector('[data-ob-el="КнопкаЦентр"]');
  const right = rightButton && rightButton.closest('.managed-btn-layout');
  const center = centerButton && centerButton.closest('.managed-btn-layout');
  const host = right && right.parentElement;
  const picture = document.querySelector('[data-ob-el="КартинкаСправа"]');
  const pictureBox = picture && picture.closest('.form-picture');
  const pictureHost = pictureBox && pictureBox.parentElement;
  const stretchPicture = document.querySelector('[data-ob-el="КартинкаРастянуть"]');
  const stretchBox = stretchPicture && stretchPicture.closest('.form-picture');
  const stretchHost = stretchBox && stretchBox.parentElement;
  const ref = document.querySelector('select[name="Контрагент"]');
  const file = document.querySelector('input[name="Файл"]');
  const impossible = 1000000;
  const result = {
    rightGap: right && host ? Math.abs(host.getBoundingClientRect().right - right.getBoundingClientRect().right) : impossible,
    centerDelta: center && host ? Math.abs((host.getBoundingClientRect().left + host.getBoundingClientRect().right) / 2 - (center.getBoundingClientRect().left + center.getBoundingClientRect().right) / 2) : impossible,
    pictureGap: pictureBox && pictureHost ? Math.abs(pictureHost.getBoundingClientRect().right - pictureBox.getBoundingClientRect().right) : impossible,
    pictureRatio: pictureBox && pictureHost ? pictureBox.getBoundingClientRect().width / pictureHost.getBoundingClientRect().width : impossible,
    stretchRatio: stretchBox && stretchHost ? stretchBox.getBoundingClientRect().width / stretchHost.getBoundingClientRect().width : 0,
    refHeight: ref ? ref.getBoundingClientRect().height : 0,
    refRatio: ref && ref.parentElement ? ref.getBoundingClientRect().height / ref.parentElement.getBoundingClientRect().height : 0,
    fileHeight: file ? file.getBoundingClientRect().height : 0,
    fileRatio: file && file.parentElement ? file.getBoundingClientRect().height / file.parentElement.getBoundingClientRect().height : 0
  };
  const out = document.createElement('pre');
  out.id = 'layout-measure';
  out.textContent = JSON.stringify(result);
  document.body.appendChild(out);
})();
</script>`

	page := rec.Body.String()
	endBody := strings.LastIndex(page, "</body>")
	if endBody < 0 {
		t.Fatal("production-шаблон не содержит </body>")
	}
	page = page[:endBody] + measureScript + page[endBody:]
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static/forms/layout-test.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><rect width="40" height="40" fill="red"/></svg>`))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(pageServer.Close)

	profile := t.TempDir()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-extensions",
		"--disable-sync",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir=" + profile,
		"--dump-dom",
		pageServer.URL,
	}
	if runtime.GOOS == "linux" {
		// CI запускает Edge внутри контейнера без настроенного setuid-sandbox.
		// Флаг ограничен тестовым headless-процессом без внешней навигации.
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(t.Context(), browser, args...) //nolint:gosec // test-only browser is resolved from a closed allow-list below
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dumped, err := cmd.Output()
	if err != nil {
		t.Fatalf("headless browser: %v\n%s", err, stderr.String())
	}

	const marker = `<pre id="layout-measure">`
	start := bytes.Index(dumped, []byte(marker))
	if start < 0 {
		t.Fatalf("браузер не выполнил измерение:\n%s\n%s", stderr.String(), string(dumped))
	}
	start += len(marker)
	end := bytes.Index(dumped[start:], []byte(`</pre>`))
	if end < 0 {
		t.Fatalf("результат измерения не закрыт: %s", string(dumped[start:]))
	}
	var got struct {
		RightGap     float64 `json:"rightGap"`
		CenterDelta  float64 `json:"centerDelta"`
		PictureGap   float64 `json:"pictureGap"`
		PictureRatio float64 `json:"pictureRatio"`
		StretchRatio float64 `json:"stretchRatio"`
		RefHeight    float64 `json:"refHeight"`
		RefRatio     float64 `json:"refRatio"`
		FileHeight   float64 `json:"fileHeight"`
		FileRatio    float64 `json:"fileRatio"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(string(dumped[start:start+end]))), &got); err != nil {
		t.Fatalf("разобрать измерение: %v: %s", err, string(dumped[start:start+end]))
	}
	if got.RightGap > 8 {
		t.Errorf("halign:right не довёл кнопку до правого края: gap=%.1fpx", got.RightGap)
	}
	if got.CenterDelta > 4 {
		t.Errorf("halign:center не центрировал кнопку: delta=%.1fpx", got.CenterDelta)
	}
	if got.PictureGap > 8 || got.PictureRatio >= 0.5 {
		t.Errorf("halign:right не выровнял картинку собственной ширины: gap=%.1fpx ratio=%.2f", got.PictureGap, got.PictureRatio)
	}
	if got.StretchRatio < 0.95 || got.StretchRatio > 1.05 {
		t.Errorf("halign:stretch не растянул обёртку картинки: ratio=%.2f", got.StretchRatio)
	}
	if got.RefHeight < 100 || got.RefRatio < 0.8 {
		t.Errorf("высота ссылки осталась у строки, но не у select: height=%.1fpx ratio=%.2f", got.RefHeight, got.RefRatio)
	}
	if got.FileHeight < 100 || got.FileRatio < 0.8 {
		t.Errorf("высота file-пути осталась у строки, но не у input: height=%.1fpx ratio=%.2f", got.FileHeight, got.FileRatio)
	}
}

func managedLayoutTestBrowser(t *testing.T) string {
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
