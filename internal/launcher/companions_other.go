//go:build !windows

package launcher

import "os/exec"

// Окно Предприятия с запуском сопутствующих программ есть только в
// GUI-сборке под Windows; здесь проверка «уже запущена» не нужна.
func companionProcessRunning(string) bool { return false }

func detachCompanion(*exec.Cmd) {}

// companionFileVersion — версии из ресурса exe вне Windows нет: обновление не
// сравнивает и ничего не ставит.
func companionFileVersion(string) string { return "" }
