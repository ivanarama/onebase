//go:build windows

package launcher

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// companionProcessRunning — работает ли уже программа с этим путём. Сравнение
// по полному пути образа; если путь чужого процесса прочитать нельзя (нет
// прав), — по имени файла: лишний «уже запущена» безопаснее второго экземпляра
// программы телефонии на рабочем месте.
func companionProcessRunning(path string) bool {
	want := strings.ToLower(filepath.Clean(path))
	wantName := strings.ToLower(filepath.Base(path))
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap) //nolint:errcheck // закрытие снимка процессов, ошибка не влияет на ответ
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name != wantName {
			continue
		}
		full, ok := processImagePath(entry.ProcessID)
		if !ok || strings.ToLower(filepath.Clean(full)) == want {
			return true
		}
	}
	return false
}

func processImagePath(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // закрытие дескриптора процесса только для чтения пути
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buf[:size]), true
}

// detachCompanion — программа не наследует консоль и группу процессов окна:
// закрытие окна Предприятия её не завершает.
func detachCompanion(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
