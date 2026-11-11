package ui

import (
	"bytes"
	"strings"
	"testing"
)

func renderHead(t *testing.T, cfg Config, isAdmin bool) string {
	t.Helper()
	var buf bytes.Buffer
	data := map[string]any{
		"Cfg":         cfg,
		"IsAdmin":     isAdmin,
		"Lang":        "ru",
		"SearchQuery": "",
	}
	if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
		t.Fatalf("render nav: %v", err)
	}
	return buf.String()
}

func TestTopbar_AdminTitleShowsConfigLocation(t *testing.T) {
	html := renderHead(t, Config{
		AppName:          "Торговля",
		ConfigSource:     "file",
		ConfigLocation:   `/srv/configs/<trade>`,
		DatabaseType:     "sqlite",
		DatabaseLocation: `/srv/data/<trade>.db`,
	}, true)

	for _, want := range []string{
		`Конфигурация: file · /srv/configs/&lt;trade&gt;`,
		"SQLite · /srv/data/&lt;trade&gt;.db",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("в подсказке шапки нет %q", want)
		}
	}
	if strings.Contains(html, "<trade>") {
		t.Error("пути в подсказке должны быть HTML-экранированы")
	}
}

func TestTopbar_RegularUserDoesNotSeeConfigLocation(t *testing.T) {
	html := renderHead(t, Config{
		AppName:          "Торговля",
		ConfigSource:     "file",
		ConfigLocation:   `/srv/configs/trade`,
		DatabaseType:     "sqlite",
		DatabaseLocation: `/srv/data/trade.db`,
	}, false)

	if strings.Contains(html, "/srv/configs/trade") || strings.Contains(html, "/srv/data/trade.db") {
		t.Errorf("обычный пользователь не должен видеть серверные пути: %s", html)
	}
	if !strings.Contains(html, `title="Главная"`) {
		t.Error("у обычного пользователя подсказка осталась «Главная»")
	}
}
