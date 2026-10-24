//go:build windows

package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

// Проверка «уже запущена» по настоящему списку процессов: сам тестовый
// процесс работает, программа по несуществующему пути — нет.
func TestCompanionProcessRunningWindows(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !companionProcessRunning(self) {
		t.Fatalf("работающий процесс %s не найден", self)
	}
	if companionProcessRunning(filepath.Join(t.TempDir(), "нет-такой-программы.exe")) {
		t.Fatal("несуществующая программа считается запущенной")
	}
}
