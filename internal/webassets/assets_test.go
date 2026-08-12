package webassets

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// TestMonacoHandlerServesCriticalFiles guards the offline self-hosting: every
// file the templates load from /vendor/monaco/ must be embedded and served.
func TestMonacoHandlerServesCriticalFiles(t *testing.T) {
	h := http.StripPrefix("/vendor/monaco/", MonacoHandler())
	files := []string{
		"vs/loader.js",
		"vs/editor/editor.main.js",
		"vs/editor/editor.main.css",
		"vs/base/worker/workerMain.js",
		"vs/base/browser/ui/codicons/codicon/codicon.ttf",
		"vs/basic-languages/yaml/yaml.js",
	}
	for _, f := range files {
		req := httptest.NewRequest(http.MethodGet, "/vendor/monaco/"+f, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", f, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", f)
		}
	}

	// A path outside the embedded tree must 404, not leak the filesystem.
	req := httptest.NewRequest(http.MethodGet, "/vendor/monaco/vs/language/typescript/tsMode.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent file: status = %d, want 404", rec.Code)
	}

	// Cache header must be set for long-lived versioned assets.
	req = httptest.NewRequest(http.MethodGet, "/vendor/monaco/vs/loader.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want max-age", cc)
	}
}

// TestEChartsHandlerServesBundle guards that the shared ECharts bundle is
// embedded and served — both the base UI and the configurator load it from
// /vendor/echarts/echarts.min.js.
func TestEChartsHandlerServesBundle(t *testing.T) {
	h := http.StripPrefix("/vendor/echarts/", EChartsHandler())
	req := httptest.NewRequest(http.MethodGet, "/vendor/echarts/echarts.min.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("echarts.min.js: status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("echarts.min.js: empty body")
	}
}

// TestSlickGridHandlerServesCriticalFiles guards that all required SlickGrid
// assets are embedded and served. Managed forms load them for editable table
// parts from /vendor/slickgrid/.
func TestSlickGridHandlerServesCriticalFiles(t *testing.T) {
	h := http.StripPrefix("/vendor/slickgrid/", SlickGridHandler())
	files := []string{
		"slick.core.js",
		"slick.interactions.js",
		"slick.grid.js",
		"slick.dataview.js",
		"slick.editors.js",
		"slick.formatters.js",
		"slick.grid.css",
		"slick-default-theme.css",
	}
	for _, f := range files {
		req := httptest.NewRequest(http.MethodGet, "/vendor/slickgrid/"+f, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", f, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", f)
		}
	}

	// A path outside the embedded tree must 404.
	req := httptest.NewRequest(http.MethodGet, "/vendor/slickgrid/nonexistent.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent file: status = %d, want 404", rec.Code)
	}

	// Cache header must be set for long-lived versioned assets.
	req = httptest.NewRequest(http.MethodGet, "/vendor/slickgrid/slick.grid.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want max-age", cc)
	}
}

// TestLucideHandlerServesSprite guards the icon sprite: навигация ссылается на
// него из каждой страницы через <use href="/vendor/lucide/sprite.svg#имя">, так
// что недоступный путь = страница без иконок.
func TestLucideHandlerServesSprite(t *testing.T) {
	h := http.StripPrefix("/vendor/lucide/", LucideHandler())
	version := LucideSpriteVersion()
	if len(version) != 24 {
		t.Fatalf("LucideSpriteVersion() = %q, want 24 hex characters", version)
	}
	req := httptest.NewRequest(http.MethodGet, "/vendor/lucide/sprite.svg?v="+version, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sprite.svg: status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("sprite.svg: empty body")
	}
	// Тип обязателен: с text/plain браузер не отрисует <use>.
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned Cache-Control = %q, want immutable", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("versioned sprite has no ETag")
	}

	// The compatibility URL has no content key and must never be immutable.
	req = httptest.NewRequest(http.MethodGet, "/vendor/lucide/sprite.svg", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("unversioned Cache-Control = %q, want no-cache", cc)
	}
	req = httptest.NewRequest(http.MethodGet, "/vendor/lucide/sprite.svg", nil)
	req.Header.Set("If-None-Match", `"other", W/`+etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("weak/list If-None-Match: status=%d body=%d, want 304 and empty body", rec.Code, rec.Body.Len())
	}
	req = httptest.NewRequest(http.MethodHead, "/vendor/lucide/sprite.svg?v="+version, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag {
		t.Errorf("HEAD sprite: status=%d body=%d etag=%q", rec.Code, rec.Body.Len(), rec.Header().Get("ETag"))
	}

	// A path outside the embedded tree must 404, not leak the filesystem.
	req = httptest.NewRequest(http.MethodGet, "/vendor/lucide/nonexistent.svg", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent file: status = %d, want 404", rec.Code)
	}

	// The distributed binary contains the sprite, so the corresponding ISC
	// notice must be embedded and served with the same vendor directory.
	req = httptest.NewRequest(http.MethodGet, "/vendor/lucide/LICENSE", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ISC License") {
		t.Fatalf("embedded Lucide LICENSE: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestLucideSpriteContainsOnlyPassiveGeometry is a supply-chain boundary for
// future vendor upgrades. The same-origin SVG is referenced from authenticated
// pages, so scripts, links, styles and other active content must never enter it.
func TestLucideSpriteContainsOnlyPassiveGeometry(t *testing.T) {
	data, err := lucideFS.ReadFile("lucide/sprite.svg")
	if err != nil {
		t.Fatal(err)
	}
	allowedAttrs := map[string]map[string]bool{
		"svg":      {"xmlns": true, "version": true},
		"defs":     {},
		"symbol":   {"id": true, "viewBox": true},
		"path":     {"d": true},
		"circle":   {"cx": true, "cy": true, "r": true, "fill": true},
		"ellipse":  {"cx": true, "cy": true, "rx": true, "ry": true},
		"line":     {"x1": true, "x2": true, "y1": true, "y2": true},
		"polygon":  {"points": true},
		"polyline": {"points": true},
		"rect":     {"x": true, "y": true, "width": true, "height": true, "rx": true, "ry": true},
	}
	wantParent := map[string]string{
		"svg": "", "defs": "svg", "symbol": "defs",
		"path": "symbol", "circle": "symbol", "ellipse": "symbol",
		"line": "symbol", "polygon": "symbol", "polyline": "symbol", "rect": "symbol",
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	var stack []string
	ids := make(map[string]struct{}, 2048)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse sprite: %v", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			name := tok.Name.Local
			if tok.Name.Space != "http://www.w3.org/2000/svg" {
				t.Fatalf("element <%s> has namespace %q, want SVG namespace", name, tok.Name.Space)
			}
			attrs, ok := allowedAttrs[name]
			if !ok {
				t.Fatalf("active or unsupported SVG element <%s>", name)
			}
			parent := ""
			if len(stack) != 0 {
				parent = stack[len(stack)-1]
			}
			if parent != wantParent[name] {
				t.Fatalf("element <%s> under <%s>, want <%s>", name, parent, wantParent[name])
			}
			for _, attr := range tok.Attr {
				if attr.Name.Space != "" && attr.Name.Space != "xmlns" {
					t.Fatalf("unsupported attribute namespace %q on %s", attr.Name.Space, attr.Name.Local)
				}
				if !attrs[attr.Name.Local] {
					t.Fatalf("unsupported attribute %s on <%s>", attr.Name.Local, name)
				}
				if name == "svg" && attr.Name.Local == "xmlns" && attr.Value != "http://www.w3.org/2000/svg" {
					t.Fatalf("unexpected SVG namespace %q", attr.Value)
				}
				if attr.Name.Local == "id" {
					if !validLucideID(attr.Value) {
						t.Fatalf("unsafe symbol id %q", attr.Value)
					}
					if _, duplicate := ids[attr.Value]; duplicate {
						t.Fatalf("duplicate symbol id %q", attr.Value)
					}
					ids[attr.Value] = struct{}{}
				}
				if attr.Name.Local == "fill" && attr.Value != "currentColor" {
					t.Fatalf("unexpected fill value %q", attr.Value)
				}
			}
			stack = append(stack, name)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != tok.Name.Local {
				t.Fatalf("unbalanced closing element </%s>", tok.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if strings.TrimSpace(string(tok)) != "" {
				t.Fatalf("unexpected text in sprite: %q", strings.TrimSpace(string(tok)))
			}
		case xml.ProcInst:
			if tok.Target != "xml" {
				t.Fatalf("unexpected processing instruction %q", tok.Target)
			}
		case xml.Directive:
			t.Fatalf("XML directives/DTDs are forbidden: %q", strings.TrimSpace(string(tok)))
		}
	}
	if len(stack) != 0 {
		t.Fatalf("unclosed elements: %v", stack)
	}
	if len(ids) != len(LucideSymbolNames()) {
		t.Fatalf("validated %d unique symbols, names parser returned %d", len(ids), len(LucideSymbolNames()))
	}
}

func validLucideID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// TestLucideSymbolNamesMatchSprite — список имён не отдельный артефакт, а разбор
// самого спрайта: проверяем, что разбор нашёл все <symbol> и ничего лишнего.
func TestLucideSymbolNamesMatchSprite(t *testing.T) {
	names := LucideSymbolNames()
	if len(names) < 1000 {
		t.Fatalf("имён всего %d — похоже, спрайт подменили огрызком", len(names))
	}
	if !sort.StringsAreSorted(names) {
		t.Error("имена не отсортированы (datalist конфигуратора ждёт порядок)")
	}
	data, err := lucideFS.ReadFile("lucide/sprite.svg")
	if err != nil {
		t.Fatalf("спрайт не читается: %v", err)
	}
	if got, want := len(names), strings.Count(string(data), "<symbol "); got != want {
		t.Errorf("разобрано %d имён при %d символах в спрайте", got, want)
	}
	// Каждое имя обязано быть настоящим id символа.
	for _, n := range []string{names[0], names[len(names)/2], names[len(names)-1], "square", "shopping-cart"} {
		if !strings.Contains(string(data), `<symbol id="`+n+`"`) {
			t.Errorf("имя %q не найдено среди символов спрайта", n)
		}
	}
}

func TestLucideSymbolNamesReturnsCopy(t *testing.T) {
	names := LucideSymbolNames()
	if len(names) == 0 {
		t.Fatal("empty names")
	}
	want := names[0]
	names[0] = "caller-mutation"
	if got := LucideSymbolNames()[0]; got != want {
		t.Fatalf("caller mutated cached symbol names: got %q, want %q", got, want)
	}
}

// TestQuillHandlerServesBundle guards that the Quill WYSIWYG editor is embedded
// and served offline — richtext-fields load quill.js and quill.snow.css from
// /vendor/quill/.
func TestQuillHandlerServesBundle(t *testing.T) {
	h := http.StripPrefix("/vendor/quill/", QuillHandler())
	for _, f := range []string{"quill.js", "quill.snow.css"} {
		req := httptest.NewRequest(http.MethodGet, "/vendor/quill/"+f, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", f, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", f)
		}
	}

	// A path outside the embedded tree must 404, not leak the filesystem.
	req := httptest.NewRequest(http.MethodGet, "/vendor/quill/nonexistent.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent file: status = %d, want 404", rec.Code)
	}

	// Cache header must be set for long-lived versioned assets.
	req = httptest.NewRequest(http.MethodGet, "/vendor/quill/quill.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want max-age", cc)
	}
}
