package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// baseWithAppYAML создаёт файловую базу с заданным содержимым config/app.yaml.
func baseWithAppYAML(t *testing.T, content string) *Base {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Base{ID: "b", Name: "Проверка", ConfigSource: "file", Path: dir}
}

type appCfgProbe struct {
	Name string `yaml:"name"`
	Logo string `yaml:"logo"`
}

func TestReadAppYAMLParses(t *testing.T) {
	b := baseWithAppYAML(t, "name: Торговля\nlogo: images/logo.png\n")
	var cfg appCfgProbe
	if err := readAppYAML(context.Background(), b, &cfg); err != nil {
		t.Fatalf("readAppYAML: %v", err)
	}
	if cfg.Name != "Торговля" || cfg.Logo != "images/logo.png" {
		t.Errorf("разобрано неверно: %+v", cfg)
	}
}

// Отсутствие конфигурации — штатная ситуация (база ещё не наполнена) и должно
// отличаться от битого YAML: на неё нет смысла ругаться в журнале.
func TestReadAppYAMLAbsentIsDistinctFromBroken(t *testing.T) {
	records := captureLog(t)

	var cfg appCfgProbe
	err := readAppYAML(context.Background(), &Base{ID: "b", ConfigSource: "file"}, &cfg)
	if !errors.Is(err, errAppConfigAbsent) {
		t.Fatalf("ожидался errAppConfigAbsent, получено %v", err)
	}
	if recs := records(); len(recs) != 0 {
		t.Errorf("отсутствие конфигурации не должно писать в журнал: %v", recs)
	}
}

// Главное. Раньше битый app.yaml давал нулевую структуру без единого следа:
// интерфейс показывал не «конфигурация не читается», а «имя не задано,
// логотипа нет», и пользователь искал несуществующую проблему в настройках.
func TestReadAppYAMLBrokenReportsAndLogs(t *testing.T) {
	records := captureLog(t)
	b := baseWithAppYAML(t, "name: [не закрыт\n")

	var cfg appCfgProbe
	if err := readAppYAML(context.Background(), b, &cfg); err == nil {
		t.Fatal("битый YAML должен возвращать ошибку")
	}

	rec := onlyRecord(t, records())
	if rec["level"] != "WARN" {
		t.Errorf("уровень = %v, ожидался WARN", rec["level"])
	}
	if rec["base"] != "Проверка" {
		t.Errorf("в записи должна быть база: %v", rec)
	}
}

// Пустой backup.directory означает «каталог по умолчанию», поэтому битый
// app.yaml молча менял место хранения резервных копий. Поведение сохранено
// (падать нельзя — каталог нужен и для показа настроек), но теперь это видно.
func TestBackupDirFallsBackAndLogsOnBrokenConfig(t *testing.T) {
	records := captureLog(t)
	b := baseWithAppYAML(t, "backup: [не закрыт\n")

	h := &handler{}
	if got := h.loadBackupDirSetting(b); got != "" {
		t.Errorf("ожидалась пустая настройка, получено %q", got)
	}
	if want := filepath.Join(b.Path, "backups"); h.backupDir(b) != want {
		t.Errorf("каталог по умолчанию = %q, ожидался %q", h.backupDir(b), want)
	}
	if len(records()) == 0 {
		t.Error("подмена каталога резервных копий должна попадать в журнал")
	}
}
