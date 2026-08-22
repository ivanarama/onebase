package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAppYAML(t *testing.T, body string, modified time.Time) string {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "app.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !modified.IsZero() {
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Заявка #1098: база перестала запускаться сразу после обновления платформы, и
// по сообщению нельзя было понять, обновление ли сломало конфигурацию. На деле
// app.yaml был сломан за два с половиной месяца до того, а обновление лишь
// перестало проглатывать ошибку разбора (#417). Время изменения файла отвечает
// на этот вопрос сразу — без раскопок в логах и истории загрузчика.
func TestLoadConfig_BrokenYAMLReportsFileAge(t *testing.T) {
	// Дата взята из заявки: там ответом было именно «правка двухмесячной
	// давности», а не обновление.
	broken := time.Date(2026, 6, 9, 20, 5, 0, 0, time.Local)
	dir := writeAppYAML(t, "name: Демо\n  llm:\n    provider: x\n", broken)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("сломанный app.yaml принят без ошибки")
	}
	msg := err.Error()

	// Исходная ошибка обязана уцелеть: без строки YAML чинить нечего.
	for _, want := range []string{"app.yaml", "yaml:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в сообщении нет %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(msg, "09.06.2026 20:05") {
		t.Errorf("в сообщении нет времени изменения файла:\n%s", msg)
	}
	if !strings.Contains(msg, "onebase check --project "+dir) {
		t.Errorf("в сообщении нет подсказки про check с путём проекта:\n%s", msg)
	}
	// Приписка идёт со своей строки: она адресована человеку, а первая строка
	// остаётся машинно-читаемой, как была.
	if !strings.Contains(msg, "\nфайл изменён ") {
		t.Errorf("приписка не отделена переводом строки:\n%s", msg)
	}
}

// Приписка не должна появляться там, где ошибки нет: отсутствующий app.yaml —
// это конфигурация по умолчанию, а не поломка.
func TestLoadConfig_MissingFileStaysDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("отсутствующий app.yaml дал ошибку: %v", err)
	}
	if cfg == nil || cfg.Name != filepath.Base(dir) {
		t.Fatalf("ожидалась конфигурация по умолчанию, получено %+v", cfg)
	}
}

// Второй YAML-документ в файле — тоже отказ разбора, и он обязан получить ту же
// приписку: пользователю всё равно, каким именно способом файл сломан.
func TestLoadConfig_SecondDocumentAlsoReportsFileAge(t *testing.T) {
	when := time.Date(2026, 3, 1, 9, 30, 0, 0, time.Local)
	dir := writeAppYAML(t, "name: Демо\n---\nname: Второй\n", when)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("второй YAML-документ принят без ошибки")
	}
	if !strings.Contains(err.Error(), "01.03.2026 09:30") {
		t.Errorf("в сообщении нет времени изменения файла:\n%s", err.Error())
	}
}
