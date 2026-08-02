package launcher

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Весь смысл cleanup.go — в уровне. Эти сбои штатный фон (занятый файл на
// Windows, гонка с антивирусом, уже закрытое соединение); на Warn журнал
// конфигуратора превратился бы в шум, и настоящие предупреждения в нём
// потерялись бы. Тест держит именно Debug.
func TestCleanupHelpersLogAtDebug(t *testing.T) {
	records := captureLog(t)

	bestEffort("сделать что-то неважное", errors.New("сбой"))

	rec := onlyRecord(t, records())
	if rec["level"] != "DEBUG" {
		t.Errorf("уровень = %v, ожидался DEBUG", rec["level"])
	}
	if rec["msg"] != "не удалось сделать что-то неважное" {
		t.Errorf("сообщение должно называть действие, получено %v", rec["msg"])
	}
}

func TestBestEffortSilentOnSuccess(t *testing.T) {
	records := captureLog(t)
	bestEffort("что-то", nil)
	if recs := records(); len(recs) != 0 {
		t.Errorf("успех не должен писать в журнал: %v", recs)
	}
}

func TestRemoveTempDeletesTree(t *testing.T) {
	records := captureLog(t)
	dir := filepath.Join(t.TempDir(), "поддерево")
	if err := os.MkdirAll(filepath.Join(dir, "вложенный"), 0o700); err != nil {
		t.Fatal(err)
	}

	removeTemp(dir)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("каталог не удалён: %v", err)
	}
	if recs := records(); len(recs) != 0 {
		t.Errorf("удачное удаление не должно писать в журнал: %v", recs)
	}
}

func TestCloseReadLogsSecondClose(t *testing.T) {
	records := captureLog(t)
	f, err := os.CreateTemp("", "close-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	t.Cleanup(func() { removeTemp(name) })
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	closeRead("уже закрытый файл", f) // второй Close — ошибка

	if rec := onlyRecord(t, records()); rec["level"] != "DEBUG" {
		t.Errorf("уровень = %v, ожидался DEBUG", rec["level"])
	}
}

// portFree раньше игнорировал ошибку Close и возвращал true, даже если
// слушатель остался висеть, — то есть докладывал «порт свободен» о занятом
// нами же порте. Проверяем обе стороны: свободный порт и занятый.
func TestPortFreeReflectsActualState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("сеть недоступна в окружении теста: %v", err)
	}
	busy := ln.Addr().(*net.TCPAddr).Port

	if portFree(busy) {
		t.Errorf("порт %d занят слушателем, но признан свободным", busy)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if !portFree(busy) {
		t.Errorf("порт %d освобождён, но признан занятым", busy)
	}
}
