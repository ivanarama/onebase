package dblock

import (
	"context"
	"errors"
	"time"
)

// downgradeRetryDelay — пауза между попытками перехватить аренду. Значение
// подобрано так, чтобы не жечь процессор и при этом не задерживать старт
// заметно: конверсионный зазор у конкурента измеряется миллисекундами.
const downgradeRetryDelay = 25 * time.Millisecond

// ErrLeaseBusy — аренду держит другой процесс, и дождаться её не удалось.
// Отдельная ошибка, а не обёрнутый context.DeadlineExceeded: вызывающему важно
// отличить «база занята» от «истёк общий таймаут запуска».
var ErrLeaseBusy = errors.New("dblock: база занята другим процессом")

// waitBeforeRetry выдерживает паузу перед следующей попыткой, но прерывается
// отменой контекста. Без этого ожидание блокировки ОС переживало и отмену, и
// Ctrl+C — процесс молча висел на старте (#962, Н5).
func waitBeforeRetry(ctx context.Context) error {
	timer := time.NewTimer(downgradeRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.Join(ErrLeaseBusy, ctx.Err())
	case <-timer.C:
		return nil
	}
}
