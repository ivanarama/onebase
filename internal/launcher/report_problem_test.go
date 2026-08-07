package launcher

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
)

func renderReportProblemPage(t *testing.T, data map[string]any) string {
	t.Helper()
	base := map[string]any{
		"Title": "onebase", "Lang": "ru",
		"Bases": []*Base{{ID: "b1", Name: "Торговля"}},
		"Did":   "", "Expected": "", "Got": "", "BaseID": "", "AttachLog": true,
		"Preview": "", "SavedPath": "", "Error": "",
		"Contacts": bugreport.Contacts{},
	}
	for k, v := range data {
		base[k] = v
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-report-problem", base); err != nil {
		t.Fatalf("ExecuteTemplate page-report-problem: %v", err)
	}
	return buf.String()
}

func TestLauncherReportProblem_FormListsBases(t *testing.T) {
	html := renderReportProblemPage(t, nil)
	for _, want := range []string{"Что вы делали", "Торговля", `name="base"`, "attach_log", "Сформировать отчёт"} {
		if !strings.Contains(html, want) {
			t.Errorf("в форме нет %q", want)
		}
	}
}

func TestLauncherReportProblem_PreviewIsEditable(t *testing.T) {
	html := renderReportProblemPage(t, map[string]any{"Preview": "# отчёт", "BaseID": "b1"})
	if !strings.Contains(html, `<textarea id="rp-text" name="report"`) {
		t.Error("предпросмотр должен быть редактируемым")
	}
	if !strings.Contains(html, "Сохранить пакет") || !strings.Contains(html, "Скопировать текст") {
		t.Error("в предпросмотре нужны «Сохранить пакет» и «Скопировать текст»")
	}
	if !strings.Contains(html, "Проверьте текст") {
		t.Error("нет предупреждения о проверке текста")
	}
	// Выбранная база должна пережить переход к предпросмотру.
	if !strings.Contains(html, `name="base" value="b1"`) {
		t.Error("выбранная база потерялась при переходе к предпросмотру")
	}
}

func TestLauncherReportProblem_ShowsSavedPathAndError(t *testing.T) {
	ok := renderReportProblemPage(t, map[string]any{"SavedPath": `C:\Users\u\.onebase\reports\r.zip`})
	if !strings.Contains(ok, `C:\Users\u\.onebase\reports\r.zip`) {
		t.Error("путь сохранённого пакета не показан")
	}
	bad := renderReportProblemPage(t, map[string]any{"Error": "нет места на диске"})
	if !strings.Contains(bad, "нет места на диске") {
		t.Error("ошибка сохранения не показана")
	}
}

func TestLauncherReportProblem_ToolbarHasLink(t *testing.T) {
	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "page-index", map[string]any{
		"Title": "onebase", "Lang": "ru", "Bases": []*baseVM{}, "Selected": nil,
		"NativeOK": false, "BaseURL": "",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate page-index: %v", err)
	}
	if !strings.Contains(buf.String(), "/report-problem") {
		t.Error("в тулбаре лаунчера нет кнопки «Сообщить об ошибке»")
	}
}

func TestBaseLogTail_ReadsLauncherLog(t *testing.T) {
	dir := t.TempDir()
	old := logsDirOverride
	logsDirOverride = dir
	t.Cleanup(func() { logsDirOverride = old })

	// В журнале есть строка подключения с паролем — она не должна доехать.
	body := "первая строка\nopen db postgres://u:s3cret@db.local/trade\nпоследняя строка\n"
	if err := os.WriteFile(filepath.Join(dir, "b1.log"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	h := &handler{}
	tail := h.baseLogTail("b1")
	if !strings.Contains(tail, "последняя строка") {
		t.Errorf("хвост журнала не прочитан: %q", tail)
	}
	if strings.Contains(tail, "s3cret") {
		t.Errorf("пароль из журнала попал в отчёт: %q", tail)
	}
	if !strings.Contains(tail, "postgres://u:***@db.local/trade") {
		t.Errorf("маскирование съело строку подключения целиком: %q", tail)
	}
	if h.baseLogTail("") != "" || h.baseLogTail("нет-такой") != "" {
		t.Error("отсутствие журнала должно давать пустой хвост, а не ошибку")
	}
}

// Сквозной путь страницы: форма → предпросмотр → пакет на диске.
func TestLauncherReportProblem_EndToEnd(t *testing.T) {
	logs := t.TempDir()
	oldLogs := logsDirOverride
	logsDirOverride = logs
	t.Cleanup(func() { logsDirOverride = oldLogs })

	// Проводник в тестах не открываем — запоминаем, что его позвали.
	var opened string
	oldOpen := openReportDir
	openReportDir = func(p string) error { opened = p; return nil }
	t.Cleanup(func() { openReportDir = oldOpen })

	store := newTestStore(t)
	b := &Base{ID: "b1", Name: "Торговля", DBType: "sqlite", ConfigSource: "file"}
	if err := store.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logs, "b1.log"), []byte("паника в обработчике\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store}

	// Шаг 1 — предпросмотр.
	form := url.Values{
		"did": {"нажал «Провести»"}, "expected": {"документ проведён"}, "got": {"ошибка"},
		"base": {"b1"}, "attach_log": {"1"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/report-problem", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.reportProblemPreview(rec, req)

	if rec.Code != 200 {
		t.Fatalf("предпросмотр вернул %d", rec.Code)
	}
	html := rec.Body.String()
	for _, want := range []string{"нажал «Провести»", "Торговля", "паника в обработчике", "SQLite"} {
		if !strings.Contains(html, want) {
			t.Errorf("в предпросмотре нет %q", want)
		}
	}

	// Шаг 2 — сохранение. Каталог отчётов подменяем через HOME, чтобы тест не
	// писал в профиль разработчика.
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	edited := "# мой отчёт\nбез лишнего\n"
	save := url.Values{"report": {edited}, "base": {"b1"}, "attach_log": {"1"}}
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/report-problem/save", strings.NewReader(save.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.reportProblemSave(rec2, req2)

	if rec2.Code != 200 {
		t.Fatalf("сохранение вернуло %d", rec2.Code)
	}
	if strings.Contains(rec2.Body.String(), "Не удалось сохранить") {
		t.Fatalf("страница сообщила об ошибке сохранения:\n%s", rec2.Body.String())
	}

	found, err := filepath.Glob(filepath.Join(home, ".onebase", "reports", "*.zip"))
	if err != nil || len(found) != 1 {
		t.Fatalf("пакет не создан: %v %v", found, err)
	}
	zr, err := zip.OpenReader(found[0])
	if err != nil {
		t.Fatalf("пакет не открывается: %v", err)
	}
	defer func() { _ = zr.Close() }()

	names := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		names[f.Name] = string(body)
	}
	if names["report.md"] != edited {
		t.Errorf("в пакете не отредактированный пользователем текст: %q", names["report.md"])
	}
	if !strings.Contains(names["logs/base.log"], "паника в обработчике") {
		t.Errorf("журнал базы не попал в пакет: %q", names["logs/base.log"])
	}
	if opened != filepath.Dir(found[0]) {
		t.Errorf("папку с пакетом не показали пользователю: %q", opened)
	}
}

func TestReportBundlePath(t *testing.T) {
	path, err := reportBundlePath(time.Date(2026, 8, 7, 14, 32, 11, 0, time.UTC))
	if err != nil {
		t.Fatalf("reportBundlePath: %v", err)
	}
	want := filepath.Join(".onebase", "reports", "onebase-report-20260807-143211.zip")
	if !strings.HasSuffix(path, want) {
		t.Errorf("путь пакета = %q, ожидался с хвостом %q", path, want)
	}
}
