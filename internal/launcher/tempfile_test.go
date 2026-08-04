package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTempFileRoundTripAndCleanup(t *testing.T) {
	path, cleanup, err := writeTempFile("probe-*.yaml", "имя: виджет\n")
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: путь только что получен здесь же
	if err != nil {
		t.Fatalf("временный файл не читается: %v", err)
	}
	if string(got) != "имя: виджет\n" {
		t.Errorf("содержимое = %q", got)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup не удалил файл: %v", err)
	}
}

// Каталог временных файлов уводится в никуда.
//
// os.TempDir читает разные переменные на разных ОС: TMPDIR на Unix, TMP/TEMP
// (через GetTempPath) на Windows. Одного TMPDIR мало — на Windows он молча
// игнорируется, и проверка вырождается в «временный файл создался как обычно».
func setMissingTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "нет-такого-каталога")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
}

// Сбой подготовки временного файла — не повод пропустить проверку.
func TestWriteTempFileFailsWhenTempDirMissing(t *testing.T) {
	setMissingTempDir(t)

	if _, _, err := writeTempFile("probe-*.yaml", "x"); err == nil {
		t.Fatal("ожидалась ошибка при недоступном каталоге временных файлов")
	}
}

// Главный случай чанка.
//
// Проверка виджета намеренно идёт через временный файл: YAML разбирается тем же
// загрузчиком, что и при обычной загрузке с диска, чтобы кривой ввод не заменил
// рабочее определение. Раньше весь блок стоял под `if err == nil` — сбой
// создания временного файла молча ОТКЛЮЧАЛ проверку, и непроверенный YAML
// уходил в конфигурацию. То есть guard исчезал ровно в тот момент, когда с
// машиной что-то не так.
//
// Каталог временных файлов в никуда воспроизводит это детерминированно и без
// возни с правами доступа.
func TestSaveWidgetDoesNotSaveWhenValidationCannotRun(t *testing.T) {
	cfgDir := t.TempDir()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "wdg", Name: "wdg", ConfigSource: "file", Path: cfgDir}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}

	setMissingTempDir(t)

	form := url.Values{"widget_name": {"Продажи"}, "yaml": {"это: не виджет\n"}}
	req := httptest.NewRequest(http.MethodPost, "/bases/wdg/configurator/widget/save",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, "wdg")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorSaveWidget(rec, req)

	if entries, err := os.ReadDir(filepath.Join(cfgDir, "widgets")); err == nil && len(entries) > 0 {
		t.Fatalf("виджет сохранён без проверки: %v", entries)
	}
}
