//go:build !windows

package devserver

import "os"

// terminate просит сервер завершиться штатно: по SIGTERM он закрывает
// соединения и останавливает планировщик (см. runDev). Kill остаётся запасным
// вариантом в supervisor.stop, если процесс не отреагировал.
func terminate(p *os.Process) {
	if err := p.Signal(os.Interrupt); err != nil {
		// Процесс мог завершиться сам между проверкой и сигналом — это не сбой:
		// результат остановки подтверждает не сигнал, а завершение процесса.
		supervisorLog().Debug("сигнал завершения не доставлен", "err", err)
	}
}
