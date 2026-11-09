//go:build windows

package launcher

import (
	"fmt"
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

// companionFileVersion — строка FileVersion из ресурса версии exe. У Callista
// это полный semver (0.7.0-preview.6), а ProductVersion у предварительных
// сборок одинаковый (0.7.0.0) — сравнивать по нему нельзя. "" — ресурса нет.
func companionFileVersion(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return ""
	}
	var trans *[2]uint16
	var transLen uint32
	block := `\VarFileInfo\Translation`
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), block, unsafe.Pointer(&trans), &transLen); err != nil || transLen < 4 || trans == nil {
		return ""
	}
	sub := fmt.Sprintf(`\StringFileInfo\%04x%04x\FileVersion`, trans[0], trans[1])
	var val *uint16
	var valLen uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), sub, unsafe.Pointer(&val), &valLen); err != nil || valLen == 0 || val == nil {
		return ""
	}
	return strings.TrimSpace(windows.UTF16PtrToString(val))
}
