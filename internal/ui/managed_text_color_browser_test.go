package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// text_color и importance: error у ПолеВвода и Надпись. Проверяется цвет,
// вычисленный браузером на production-странице формы: у нередактируемого
// поля серый цвет задаёт отдельное правило, и наличие style-атрибута ещё не
// значит, что значение покраснело.
func TestManagedTextColor_BrowserColorsReadonlyFieldAndLabel(t *testing.T) {
	browser := managedLayoutTestBrowser(t)

	ent := &metadata.Entity{
		Name: "Заявки",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ОшибкаВыгрузки", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{{
		Name:       "Форма",
		EntityName: ent.Name,
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеОшибка", DataPath: "Объект.ОшибкаВыгрузки", ReadOnly: true, Importance: "error"},
			{Kind: metadata.FormElementField, Name: "ПолеСиний", DataPath: "Объект.Комментарий", TextColor: "#0055aa"},
			{Kind: metadata.FormElementField, Name: "ПолеОбычное", DataPath: "Объект.Наименование", ReadOnly: true},
			{Kind: metadata.FormElementLabel, Name: "НадписьОшибка", Importance: "error"},
			{Kind: metadata.FormElementLabel, Name: "НадписьНеверныйЦвет", TextColor: "url(x)"},
		},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})

	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/заявки/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "заявки")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	page := rec.Body.String()
	if strings.Contains(page, "url(x)") {
		t.Fatal("недопустимый text_color попал в разметку")
	}

	const measureScript = `<script>
(() => {
  const color = (sel) => { const n = document.querySelector(sel); return n ? getComputedStyle(n).color : ''; };
  const result = {
    errorInput: color('[data-ob-el="ПолеОшибка"] input[name="ОшибкаВыгрузки"]'),
    errorLabel: color('[data-ob-el="ПолеОшибка"] > label'),
    blueInput: color('[data-ob-el="ПолеСиний"] input[name="Комментарий"]'),
    plainInput: color('[data-ob-el="ПолеОбычное"] input[name="Наименование"]'),
    errorDecoration: color('[data-ob-el="НадписьОшибка"]'),
    badDecoration: color('[data-ob-el="НадписьНеверныйЦвет"]')
  };
  const out = document.createElement('pre');
  out.id = 'color-measure';
  out.textContent = JSON.stringify(result);
  document.body.appendChild(out);
})();
</script>`
	endBody := strings.LastIndex(page, "</body>")
	if endBody < 0 {
		t.Fatal("production-шаблон не содержит </body>")
	}
	page = page[:endBody] + measureScript + page[endBody:]
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(pageServer.Close)

	args := []string{
		"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update",
		"--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir=" + t.TempDir(), "--dump-dom", pageServer.URL,
	}
	if runtime.GOOS == "linux" {
		// CI запускает Edge внутри контейнера без настроенного setuid-sandbox.
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(t.Context(), browser, args...) //nolint:gosec // test-only browser is resolved from a closed allow-list
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dumped, err := cmd.Output()
	if err != nil {
		t.Fatalf("headless browser: %v\n%s", err, stderr.String())
	}
	const marker = `<pre id="color-measure">`
	start := bytes.Index(dumped, []byte(marker))
	if start < 0 {
		t.Fatalf("браузер не выполнил измерение:\n%s", stderr.String())
	}
	start += len(marker)
	end := bytes.Index(dumped[start:], []byte(`</pre>`))
	var got map[string]string
	if end < 0 || json.Unmarshal([]byte(html.UnescapeString(string(dumped[start:start+end]))), &got) != nil {
		t.Fatalf("разобрать измерение: %s", string(dumped[start:]))
	}

	const red = "rgb(185, 28, 28)"
	for key, want := range map[string]string{
		"errorInput":      red,
		"errorLabel":      red,
		"blueInput":       "rgb(0, 85, 170)",
		"plainInput":      "rgb(51, 65, 85)",
		"errorDecoration": red,
		"badDecoration":   "rgb(71, 85, 105)",
	} {
		if got[key] != want {
			t.Errorf("%s: цвет %q, ждали %q", key, got[key], want)
		}
	}
}
