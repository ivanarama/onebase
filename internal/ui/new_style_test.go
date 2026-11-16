package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestNewStyleFormStyle_HeadLinksTheme — включённый новый вид форм подключает
// таблицу стилей и вешает класс на <html>; выключенный не делает ни того, ни
// другого, иначе у баз с обычным видом был бы лишний запрос.
func TestNewStyleFormStyle_HeadLinksTheme(t *testing.T) {
	render := func(t *testing.T, cfg Config) string {
		t.Helper()
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "head", map[string]any{"Cfg": cfg, "Lang": "ru"}); err != nil {
			t.Fatalf("рендер head: %v", err)
		}
		return buf.String()
	}

	on := render(t, Config{FormStyle: "new-style"})
	for _, want := range []string{
		`class="ob-style-new"`,
		`<link rel="stylesheet" href="/static/theme-new-style.css">`,
	} {
		if !strings.Contains(on, want) {
			t.Errorf("при включённом виде нет %q", want)
		}
	}

	off := render(t, Config{})
	for _, unwanted := range []string{"ob-style-new", "theme-new-style.css"} {
		if strings.Contains(off, unwanted) {
			t.Errorf("при выключенном виде найдено %q", unwanted)
		}
	}
}

// TestNewStyleFormStyle_StaticCSS — таблица стилей нового вида отдаётся с типом
// text/css и ревалидируется по ETag: имя файла фиксированное, поэтому
// immutable-кэш отдавал бы старый CSS после обновления движка.
func TestNewStyleFormStyle_StaticCSS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/theme-new-style.css", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("content-type = %q", ct)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("нет ETag")
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	body := rr.Body.String()
	// Класс на <html> — то, чем новый вид ограничен: без него файл не влияет
	// ни на одно правило интерфейса.
	if !strings.Contains(body, ".ob-style-new") {
		t.Error("в таблице стилей нет ограничения по классу ob-style-new")
	}
	if !strings.Contains(body, ".card.ob-form-card") {
		t.Error("в таблице стилей нет правил для карточки формы")
	}

	req = httptest.NewRequest(http.MethodGet, "/static/theme-new-style.css", nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("повторный запрос status = %d, ожидали 304", rr.Code)
	}
}
