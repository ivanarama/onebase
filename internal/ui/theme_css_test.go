package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Внешние стили конфигурации (ui.css в корне проекта): файл подключается после
// встроенных стилей только когда он есть; без файла страницы остаются ровно на
// встроенных стилях.

func TestThemeCSSHandler_ServesProjectFile(t *testing.T) {
	dir := t.TempDir()
	css := ".topbar{background:#123456}\n"
	if err := os.WriteFile(filepath.Join(dir, "ui.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{ConfigLocation: dir}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/theme.css", nil)
	s.themeCSS(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("код ответа %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("стили конфигурации должны переизбираться без кэша: %q", cc)
	}
	if body := rec.Body.String(); body != css {
		t.Errorf("тело %q, ждали файл проекта", body)
	}
}

func TestThemeCSSHandler_MissingFileIsEmpty(t *testing.T) {
	s := &Server{cfg: Config{ConfigLocation: t.TempDir()}}
	rec := httptest.NewRecorder()
	s.themeCSS(rec, httptest.NewRequest(http.MethodGet, "/ui/theme.css", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("без файла ожидается пустой 200, получили %d %q", rec.Code, rec.Body.String())
	}
}

func TestThemeCSSHeadLinkOnlyWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{ConfigLocation: dir}}
	if s.hasThemeCSS() {
		t.Fatal("hasThemeCSS на пустом проекте")
	}

	// page-about через прямой рендер head-составляющей: без файла — ссылки нет.
	data := map[string]any{
		"Cfg":       prepareAboutConfig(s.cfg),
		"Lang":      "ru",
		"IsAdmin":   true,
		"Catalogs":  0,
		"Documents": 0,
		"Registers": 0,
		"Reports":   0,
		"ThemeCSS":  s.hasThemeCSS(),
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-about", data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "/ui/theme.css") {
		t.Error("без файла стилей в head появилась ссылка")
	}

	if err := os.WriteFile(filepath.Join(dir, "ui.css"), []byte("body{background:#000}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.hasThemeCSS() {
		t.Fatal("hasThemeCSS не видит файл проекта")
	}
	data["ThemeCSS"] = s.hasThemeCSS()
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "page-about", data); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	// Ссылка стоит после встроенного <style> — внешние правила переопределяют
	// встроенные, порядок в head важен.
	iStyle := strings.LastIndex(html, "</style>")
	iLink := strings.Index(html, `<link rel="stylesheet" href="/ui/theme.css">`)
	if iLink < 0 {
		t.Fatal("со ссылкой на файл стилей")
	}
	if iLink < iStyle {
		t.Error("ссылка на стили конфигурации должна идти после встроенного <style>")
	}
}

// render() добавляет ThemeCSS в данные шаблона автоматически.
func TestRenderInjectsThemeCSS(t *testing.T) {
	s := newServerForFormMode(t)
	dir := t.TempDir()
	s.cfg = Config{ConfigLocation: dir, AppName: "тест"}
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)

	about := func() string {
		rec := httptest.NewRecorder()
		s.render(rec, req, "page-about", map[string]any{
			"Cfg":       prepareAboutConfig(s.cfg),
			"Lang":      "ru",
			"IsAdmin":   true,
			"Catalogs":  0,
			"Documents": 0,
			"Registers": 0,
			"Reports":   0,
		})
		return rec.Body.String()
	}

	if strings.Contains(about(), "/ui/theme.css") {
		t.Error("без файла стилей render подставил ссылку")
	}
	if err := os.WriteFile(filepath.Join(dir, "ui.css"), []byte("body{background:#000}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(about(), `<link rel="stylesheet" href="/ui/theme.css">`) {
		t.Error("при файле стилей конфигурации render не подключил ссылку")
	}
}
// Форма администратора «Стили интерфейса»: сохранение пишет файл проекта,
// сброс удаляет его — страницы возвращаются на встроенные стили.
func TestThemeCSSAdminFormSaveAndReset(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{ConfigLocation: dir, AppName: "тест"}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader("css=body%7Bcolor%3A%23fff%7D"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("код %d после сохранения", rec.Code)
	}
	body, err := os.ReadFile(filepath.Join(dir, "ui.css"))
	if err != nil {
		t.Fatalf("файл стилей не записан: %v", err)
	}
	if string(body) != "body{color:#fff}" {
		t.Errorf("файл %q", body)
	}

	rec = httptest.NewRecorder()
	s.adminThemeCSSReset(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/reset", nil))
	if _, err := os.Stat(filepath.Join(dir, "ui.css")); !os.IsNotExist(err) {
		t.Error("после сброса файл стилей остался")
	}
}

func TestThemeCSSAdminFormRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{ConfigLocation: dir, AppName: "тест"}}
	big := strings.Repeat("a", themeCSSMaxBytes+1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader("css="+big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)
	if _, err := os.Stat(filepath.Join(dir, "ui.css")); err == nil {
		t.Error("файл сверх потолка записан")
	}
}
