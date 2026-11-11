package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
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
} // Форма администратора «Стили интерфейса»: форма правит элемент за элементом,
// настройки живут в JSON, а ui.css — сгенерированный из них артефакт; сброс
// удаляет оба файла, страницы возвращаются на встроенные стили.
func newThemeTestServer(t *testing.T) *Server {
	t.Helper()
	s := newServerForFormMode(t)
	s.cfg = Config{ConfigLocation: t.TempDir(), AppName: "тест"}
	return s
}

func TestThemeCSSAdminFormSaveAndReset(t *testing.T) {
	s := newThemeTestServer(t)
	dir := s.cfg.ConfigLocation

	form := url.Values{}
	form.Set("el", "topbar")
	form.Set("p_background", "#123456")
	form.Set("p_color", "#E2E8F0")
	form.Set("raw", "h3{color:#336699}")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)
	// «Применить» рендерит форму на месте: без перезагрузки страницы.
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d после сохранения", rec.Code)
	}

	st := s.loadThemeSettings()
	if st.Raw != "h3{color:#336699}" {
		t.Errorf("свободный CSS: %q", st.Raw)
	}
	if st.Elements["topbar"]["background"] != "#123456" {
		t.Errorf("цвет фона шапки: %q", st.Elements["topbar"])
	}
	// Значение нормализуется к нижнему регистру — в CSS одно и то же правило
	// записывается одинаково при любой правке формы.
	if st.Elements["topbar"]["color"] != "#e2e8f0" {
		t.Errorf("цвет текста шапки: %q", st.Elements["topbar"])
	}

	body, err := os.ReadFile(filepath.Join(dir, "ui.css"))
	if err != nil {
		t.Fatalf("файл стилей не записан: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "h3{color:#336699}") {
		t.Errorf("свободный CSS не попал в файл: %q", got)
	}
	if !strings.Contains(got, ".topbar{background:#123456;color:#e2e8f0}") {
		t.Errorf("правило шапки не сгенерировано: %q", got)
	}

	s.adminThemeCSSReset(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/reset", nil))
	for _, name := range []string{"ui.css", ".ui_theme.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("после сброса остался %s", name)
		}
	}
}

// Сброс редактируемого элемента снимает его свойства, свободный CSS и
// остальные элементы остаются.
func TestThemeCSSAdminFormElementReset(t *testing.T) {
	s := newThemeTestServer(t)
	dir := s.cfg.ConfigLocation

	form := url.Values{}
	form.Set("el", "topbar")
	form.Set("p_background", "#123456")
	form.Set("raw", "h3{color:#336699}")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)

	form = url.Values{}
	form.Set("el", "topbar")
	req = httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/reset-element", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSElementReset(httptest.NewRecorder(), req)

	st := s.loadThemeSettings()
	if _, ok := st.Elements["topbar"]; ok {
		t.Errorf("свойства элемента не сняты: %v", st.Elements)
	}
	if st.Raw != "h3{color:#336699}" {
		t.Errorf("свободный CSS задет сбросом: %q", st.Raw)
	}
	body, err := os.ReadFile(filepath.Join(dir, "ui.css"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), ".topbar{") {
		t.Errorf("правило элемента осталось в файле: %q", body)
	}
	if !strings.Contains(string(body), "h3{color:#336699}") {
		t.Errorf("свободный CSS потерян: %q", body)
	}
}

// Значение формы не должно превращаться в произвольный текст CSS: форма
// принимает цвет, размер и начертание по своим правилам, всё остальное — пусто.
func TestThemeCSSFormSanitizesValues(t *testing.T) {
	s := newThemeTestServer(t)
	dir := s.cfg.ConfigLocation
	form := url.Values{}
	form.Set("el", "body")
	form.Set("p_background", "red;body{display:none}")
	form.Set("p_font-size", "12px;color:red")
	form.Set("p_font-family", "system-ui};body{visibility:hidden}")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)

	st := s.loadThemeSettings()
	if len(st.Elements["body"]) != 0 {
		t.Errorf("грязные значения попали в настройки: %v", st.Elements["body"])
	}
	body, err := os.ReadFile(filepath.Join(dir, "ui.css"))
	if err != nil {
		t.Fatalf("файл стилей не записан: %v", err)
	}
	if strings.Contains(string(body), "display:none") || strings.Contains(string(body), "visibility:hidden") {
		t.Errorf("в файл стилей попала чужая декларация: %q", body)
	}
}

func TestThemeCSSAdminFormRejectsOversize(t *testing.T) {
	s := newThemeTestServer(t)
	dir := s.cfg.ConfigLocation
	big := strings.Repeat("a", themeCSSMaxBytes+1)
	form := url.Values{}
	form.Set("raw", big)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSSave(rec, req)
	if _, err := os.Stat(filepath.Join(dir, "ui.css")); err == nil {
		t.Error("файл сверх потолка записан")
	}
}

// «По умолчанию» заполняет настройки стандартными стилями встроенной светлой
// темы — поля формы и ui.css получают стандартные значения.
func TestThemeCSSAdminFormDefaultFillsStandard(t *testing.T) {
	s := newThemeTestServer(t)
	dir := s.cfg.ConfigLocation
	form := url.Values{}
	form.Set("el", "topbar")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/theme-css/default", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.adminThemeCSSDefault(rec, req)

	st := s.loadThemeSettings()
	for id, want := range themeElementDefaults {
		if st.Elements[id]["background"] != want["background"] {
			t.Errorf("%s: фон %q, ждали %q", id, st.Elements[id]["background"], want["background"])
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "ui.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), ".topbar{background:var(--ob-topbar-bg);color:var(--ob-topbar-text)}") {
		t.Errorf("стандартное правило шапки не сгенерировано: %q", body)
	}
}
