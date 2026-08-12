//go:build windows

package devserver

import "os"

// terminate завершает процесс сервера. На Windows «мягкого» сигнала для
// дочернего процесса без своей консоли нет: os.Interrupt там не реализован
// (Signal вернёт ошибку и процесс останется жить), а GenerateConsoleCtrlEvent
// требует общей группы консоли, которой у GUI-сборки нет. Поэтому сразу Kill —
// в dev-режиме это безопасно: незавершённых записей не остаётся, транзакции
// БД атомарны, а следующая сборка поднимет сервер заново.
func terminate(p *os.Process) {
	if err := p.Kill(); err != nil {
		supervisorLog().Debug("не удалось завершить процесс сервера", "err", err)
	}
}
