package ui

import (
	"bytes"
	"strings"
	"testing"
)

func renderAbout(t *testing.T, cfg Config, isAdmin bool) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{
		"Cfg":       prepareAboutConfig(cfg),
		"Lang":      "ru",
		"IsAdmin":   isAdmin,
		"Catalogs":  0,
		"Documents": 0,
		"Registers": 0,
		"Reports":   0,
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-about", data); err != nil {
		t.Fatalf("render page-about: %v", err)
	}
	return buf.String()
}

func TestAbout_AdminSeesFileConfigAndSQLiteLocations(t *testing.T) {
	html := renderAbout(t, Config{
		ConfigSource:     "file",
		ConfigLocation:   `/srv/configs/<trade>`,
		DatabaseType:     "sqlite",
		DatabaseLocation: `/srv/data/<trade>.db`,
	}, true)

	for _, want := range []string{
		"Хранение конфигурации",
		"Файлы",
		"Расположение конфигурации",
		`/srv/configs/&lt;trade&gt;`,
		"SQLite · /srv/data/&lt;trade&gt;.db",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в пользовательском «О программе» нет %q", want)
		}
	}
	if strings.Contains(html, "<trade>") {
		t.Error("пути должны быть HTML-экранированы")
	}
}

func TestAbout_DatabaseConfigUsesMaskedDatabaseLocation(t *testing.T) {
	html := renderAbout(t, Config{
		ConfigSource:     "database",
		DatabaseType:     "postgres",
		DatabaseLocation: "postgres://onebase:very-secret@db.example/production",
	}, true)

	if strings.Count(html, "postgres://onebase:***@db.example/production") != 2 {
		t.Errorf("маскированный DSN должен обозначать расположение конфигурации и базы: %s", html)
	}
	if strings.Contains(html, "very-secret") {
		t.Error("страница «О программе» раскрыла пароль PostgreSQL")
	}
}

func TestAbout_RegularUserDoesNotSeeServerPaths(t *testing.T) {
	html := renderAbout(t, Config{
		ConfigSource:     "file",
		ConfigLocation:   "/srv/private/config",
		DatabaseType:     "sqlite",
		DatabaseLocation: "/srv/private/data.db",
	}, false)

	if !strings.Contains(html, "Файлы") || !strings.Contains(html, "SQLite") {
		t.Error("обычному пользователю должны быть видны типы хранения")
	}
	for _, hidden := range []string{"/srv/private/config", "/srv/private/data.db", "Расположение конфигурации"} {
		if strings.Contains(html, hidden) {
			t.Errorf("обычному пользователю раскрыто %q", hidden)
		}
	}
}

// «О программе» в Предприятии сообщает о новой версии платформы, но не
// предлагает её ставить: рабочий процесс базы может быть системной службой и
// своим бинарём не распоряжается — обновляют из лаунчера (план 92).
func TestAbout_ShowsAvailablePlatformUpdate(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{
		"Cfg":  prepareAboutConfig(Config{PlatVersion: "build-660"}),
		"Lang": "ru", "Catalogs": 0, "Documents": 0, "Registers": 0, "Reports": 0,
		"UpdateAvailable": "build-689",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-about", data); err != nil {
		t.Fatalf("render page-about: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, "build-689") {
		t.Error("нет сведений о доступной версии платформы")
	}
	if !strings.Contains(html, "из лаунчера") {
		t.Error("не сказано, где обновляться")
	}
	if strings.Contains(html, "/updates") {
		t.Error("в Предприятии не должно быть кнопки обновления — процесс базы себя не обновляет")
	}

	// Обновлений нет — строки тоже нет.
	buf.Reset()
	data["UpdateAvailable"] = ""
	if err := tmpl.ExecuteTemplate(&buf, "page-about", data); err != nil {
		t.Fatalf("render page-about: %v", err)
	}
	if strings.Contains(buf.String(), "Доступна новая версия") {
		t.Error("без обновления строка о новой версии не нужна")
	}
}
