package bugreport

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/incident"
)

func TestMarkdown_HasEnvironmentAndIncident(t *testing.T) {
	rec := &incident.Record{
		ID:    "E-3F7A2C",
		Time:  time.Date(2026, 8, 7, 14, 32, 11, 0, time.UTC),
		Kind:  incident.KindError,
		Where: "POST /ui/doc/заказ/new",
		Text:  "no such column: цена",
		User:  "ivanov",
	}
	md := Markdown(Input{
		Did: "провёл документ", Expected: "документ проведён", Got: "красная плашка",
		Incident:     rec,
		AppName:      "Торговля",
		AppVersion:   "2.4.1",
		ConfigSource: "file",
		DBKind:       "sqlite",
		Now:          time.Date(2026, 8, 7, 14, 33, 0, 0, time.UTC),
	})

	for _, want := range []string{
		"# OneBase — сообщение об ошибке",
		"провёл документ", "документ проведён", "красная плашка",
		"E-3F7A2C", "POST /ui/doc/заказ/new", "no such column: цена",
		"onebase", "SQLite", "Торговля 2.4.1 (файлы)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("в отчёте нет %q\n---\n%s", want, md)
		}
	}
	// Логин пользователя в отчёт не идёт: он не нужен разработчику платформы.
	if strings.Contains(md, "ivanov") {
		t.Errorf("логин попал в отчёт:\n%s", md)
	}
}

func TestMarkdown_EmptyFieldsAreDashes(t *testing.T) {
	md := Markdown(Input{Now: time.Now()})
	if !strings.Contains(md, "**Что делал:** —") {
		t.Errorf("пустое поле должно давать прочерк:\n%s", md)
	}
	// Без инцидента и журнала соответствующих разделов быть не должно.
	if strings.Contains(md, "## Инцидент") || strings.Contains(md, "## Журнал") {
		t.Errorf("лишние разделы в пустом отчёте:\n%s", md)
	}
}

func TestMarkdown_RedactsSecrets(t *testing.T) {
	md := Markdown(Input{
		Got: "не подключается к postgres://user:s3cret@db.local:5432/trade",
		LogTail: strings.Join([]string{
			"2026-08-07 open db host=db.local password=hunter2 sslmode=require",
			"2026-08-07 GET /ui/export?token=abcdef&format=xlsx",
		}, "\n"),
		Now: time.Now(),
	})
	for _, leak := range []string{"s3cret", "hunter2", "abcdef"} {
		if strings.Contains(md, leak) {
			t.Errorf("секрет %q попал в отчёт:\n%s", leak, md)
		}
	}
	// Полезная часть при этом остаётся читаемой.
	for _, keep := range []string{"db.local", "sslmode=require", "format=xlsx"} {
		if !strings.Contains(md, keep) {
			t.Errorf("маскирование съело полезное %q:\n%s", keep, md)
		}
	}
}

func TestMarkdown_LogTailOnlyWhenGiven(t *testing.T) {
	with := Markdown(Input{LogTail: "строка журнала", Now: time.Now()})
	if !strings.Contains(with, "## Журнал") || !strings.Contains(with, "строка журнала") {
		t.Errorf("журнал не попал в отчёт:\n%s", with)
	}
	without := Markdown(Input{LogTail: "   \n  ", Now: time.Now()})
	if strings.Contains(without, "## Журнал") {
		t.Errorf("пустой журнал не должен давать раздел:\n%s", without)
	}
}

func TestContactsBlock_OrderAndAccountWarning(t *testing.T) {
	md := Markdown(Input{
		Contacts: Contacts{App: "help@trade.ru", Platform: "@onebase_support", IssuesURL: "https://github.com/x/y/issues/new"},
		Now:      time.Now(),
	})
	app := strings.Index(md, "help@trade.ru")
	plat := strings.Index(md, "@onebase_support")
	iss := strings.Index(md, "https://github.com/x/y/issues/new")
	if app < 0 || plat < 0 || iss < 0 {
		t.Fatalf("не все контакты попали в отчёт:\n%s", md)
	}
	if !(app < plat && plat < iss) {
		t.Errorf("порядок контактов должен быть конфигурация → платформа → трекер:\n%s", md)
	}
	if !strings.Contains(md, "нужен аккаунт GitHub") {
		t.Errorf("про аккаунт для трекера надо предупреждать честно:\n%s", md)
	}
}

func TestContactsBlock_OmittedWhenEmpty(t *testing.T) {
	md := Markdown(Input{Now: time.Now()})
	if strings.Contains(md, "## Куда отправить") {
		t.Errorf("без контактов раздел не нужен:\n%s", md)
	}
}

func TestPlatformContacts_TrimsAppContact(t *testing.T) {
	c := PlatformContacts("  help@trade.ru  ")
	if c.App != "help@trade.ru" {
		t.Errorf("контакт конфигурации не обрезан: %q", c.App)
	}
	if c.IssuesURL == "" {
		t.Error("трекер платформы должен быть задан по умолчанию")
	}
}

func TestRedact(t *testing.T) {
	cases := []struct{ in, wantGone, wantKept string }{
		{"postgres://u:p@h/db", "p@h", "postgres://u:***@h/db"},
		{"host=h password=secret x=1", "secret", "password=***"},
		{"/ui/x?token=abc&a=1", "abc", "token=***"},
		{"обычная строка", "", "обычная строка"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if c.wantGone != "" && strings.Contains(got, c.wantGone) {
			t.Errorf("Redact(%q) = %q, ожидалось без %q", c.in, got, c.wantGone)
		}
		if !strings.Contains(got, c.wantKept) {
			t.Errorf("Redact(%q) = %q, ожидалось с %q", c.in, got, c.wantKept)
		}
	}
	if Redact("") != "" {
		t.Error("Redact(\"\") должен возвращать пустую строку")
	}
}

func TestCurrentEnv(t *testing.T) {
	env := CurrentEnv()
	if !strings.HasPrefix(env.Platform, "onebase ") {
		t.Errorf("платформа = %q", env.Platform)
	}
	if !strings.Contains(env.OS, "/") {
		t.Errorf("ОС = %q, ожидалось вида windows/amd64", env.OS)
	}
	if !strings.Contains(env.String(), env.Platform) {
		t.Errorf("String() = %q", env.String())
	}
}

func TestWriteBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "report.zip")
	md := "# отчёт\n"
	err := WriteBundle(path, md, map[string]string{
		"logs/base.log": "строка журнала",
		"startup.log":   "запуск",
		"пусто.log":     "",
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("архив не открывается: %v", err)
	}
	defer func() { _ = zr.Close() }()

	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("открыть %s: %v", f.Name, err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(body)
	}
	if got["report.md"] != md {
		t.Errorf("report.md = %q, ожидалось %q", got["report.md"], md)
	}
	if got["logs/base.log"] != "строка журнала" {
		t.Errorf("журнал базы = %q", got["logs/base.log"])
	}
	if _, ok := got["пусто.log"]; ok {
		t.Error("пустое вложение не должно попадать в архив")
	}
}

func TestWriteBundle_TruncatesHugeAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.zip")
	huge := strings.Repeat("x", maxAttachmentBytes+1000) + "ХВОСТ"
	if err := WriteBundle(path, "# отчёт\n", map[string]string{"big.log": huge}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("архив не открывается: %v", err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name != "big.log" {
			continue
		}
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		if len(body) > maxAttachmentBytes {
			t.Fatalf("вложение не обрезано: %d байт", len(body))
		}
		// Обрезаем с начала: важен конец журнала.
		if !strings.HasSuffix(string(body), "ХВОСТ") {
			t.Fatal("обрезка должна оставлять конец файла")
		}
	}
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base.log")
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, "строка "+string(rune('0'+i%10)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	got := TailFile(path, 5, 8<<10)
	if n := len(strings.Split(got, "\n")); n != 5 {
		t.Fatalf("вернулось %d строк, ожидалось 5:\n%s", n, got)
	}
	if TailFile(filepath.Join(dir, "нет.log"), 5, 8<<10) != "" {
		t.Error("отсутствие файла должно давать пустую строку, а не ошибку")
	}
	if TailFile("", 5, 8<<10) != "" || TailFile(path, 0, 8<<10) != "" {
		t.Error("пустой путь и нулевое число строк дают пустой результат")
	}

	// Пустой файл — тоже не повод падать.
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if TailFile(empty, 5, 8<<10) != "" {
		t.Error("пустой файл должен давать пустую строку")
	}
}

func TestFileName(t *testing.T) {
	got := FileName(time.Date(2026, 8, 7, 14, 32, 11, 0, time.UTC), ".md")
	if got != "onebase-report-20260807-143211.md" {
		t.Errorf("FileName = %q", got)
	}
	if zip := FileName(time.Date(2026, 8, 7, 14, 32, 11, 0, time.UTC), "zip"); zip != "onebase-report-20260807-143211.zip" {
		t.Errorf("FileName без точки = %q", zip)
	}
}
