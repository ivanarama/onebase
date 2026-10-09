package launcher

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/formtest"
)

func TestEqualColumns_DesignerHTTPRoundTripAndBrowser(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "equal-columns", Path: t.TempDir(), ConfigSource: "file"}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store}
	post := func(path string, values url.Values, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/bases/"+base.ID+"/configurator/forms/"+path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		route := chi.NewRouteContext()
		route.URLParams.Add("id", base.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	source := strings.Replace(formtest.EqualColumnsYAML(), "    equal_columns: true\n", "", 1)
	const hint = "Подсказка <поля> & значение"
	source = strings.Replace(source, "        name: Поле2_0\n", "        name: Поле2_0\n        hint: "+hint+"\n", 1)
	values := url.Values{"yaml": {source}, "op": {"setProp"}, "node": {"elements.0"}, "key": {"equal_columns"}, "value": {"true"}}
	rec := post("edit-op", values, h.configuratorFormsEditOp)
	if rec.Code != 200 {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	var edited editOpResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if !edited.OK || !edited.Model["elements.0"].EqualColumns {
		t.Fatalf("edit lost equal_columns: %+v", edited)
	}
	rec = post("save", url.Values{"entity": {"Клиент"}, "name": {"Форма"}, "yaml": {edited.YAML}}, h.configuratorFormsSave)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	saved := filepath.Join(base.Path, "forms", "клиент", "форма.form.yaml")
	fm, err := loader.NewManagedFormLoader().LoadFormFile(saved, "Клиент")
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Elements) != 4 || !fm.Elements[0].EqualColumns || fm.Elements[0].Children[0].Hint != hint {
		t.Fatal("save/load lost equal_columns")
	}
	body, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	preview := post("preview", url.Values{"yaml": {string(body)}, "entity": {"Клиент"}}, h.configuratorFormsPreview)
	if preview.Code != 200 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	if !strings.Contains(preview.Body.String(), html.EscapeString(hint)) {
		t.Fatal("preview lost or did not escape the field hint")
	}
	// Turning the flag off must restore the legacy class without affecting siblings.
	values.Set("yaml", edited.YAML)
	values.Set("value", "")
	rec = post("edit-op", values, h.configuratorFormsEditOp)
	var disabled editOpResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &disabled); err != nil {
		t.Fatal(err)
	}
	if !disabled.OK || disabled.Model["elements.0"].EqualColumns || strings.Count(disabled.CanvasHTML, "ob-equal-columns") != 3 {
		t.Fatal("turning off equal_columns did not restore default")
	}
	editor := renderFormsEditorHTML(t)
	if !strings.Contains(editor, "'equal_columns', info.equalColumns") {
		t.Fatal("property panel has no binding")
	}
	styles := strings.Join(regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`).FindAllString(editor, -1), "\n")
	browser := designerLayoutTestBrowser(t)
	canvas := `<!doctype html><html><head><meta charset="utf-8">` + styles + `</head><body>` + edited.CanvasHTML + `</body></html>`
	t.Run("canvas", func(t *testing.T) { formtest.AssertEqualColumns(t, browser, canvas) })
	t.Run("preview", func(t *testing.T) { formtest.AssertEqualColumns(t, browser, preview.Body.String()) })
}
