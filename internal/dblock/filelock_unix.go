//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package dblock

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/ivantit66/onebase/internal/fsmode"
	"golang.org/x/sys/unix"
)

type fileLease struct {
	mu     sync.Mutex
	file   *os.File
	shared bool
}

func tryAcquireFileLease(path string, shared bool) (*fileLease, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.SecretFile) //nolint:gosec // G703: sole caller appends a fixed lock suffix to the absolute, clean, symlink-resolved SQLite target after validatedSQLiteTargetParent rejects roots and malformed paths
	if err != nil {
		return nil, false, err
	}
	fd := int(f.Fd()) //nolint:gosec // round-trip of an OS descriptor
	mode := unix.LOCK_EX
	if shared {
		mode = unix.LOCK_SH
	}
	if err := unix.Flock(fd, mode|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, false, f.Close()
		}
		return nil, false, errors.Join(err, f.Close())
	}
	return &fileLease{file: f, shared: shared}, true, nil
}

// Downgrade переводит эксклюзивную аренду в разделяемую, уважая отмену
// контекста (#962, Н5).
//
// Раньше здесь стоял блокирующий unix.Flock(LOCK_SH), а контекст отбрасывался
// (`_ context.Context`). Если в конверсионный зазор — тот самый, что описан в
// комментарии у вызывающего, — влезал другой процесс с эксклюзивной
// блокировкой, `onebase run` вставал на старте навсегда, и Ctrl+C не помогал:
// отмена до блокировки ОС просто не доходит. Диагностируемость нулевая —
// процесс молчит.
//
// Проверено: с LOCK_SH ожидание переживает отмену контекста, с LOCK_NB тот же
// вызов сразу возвращает EWOULDBLOCK. Поэтому берём неблокирующий захват в
// цикле с паузой и проверкой ctx — молчаливое зависание превращается во
// внятный отказ «база занята другим процессом».
func (l *fileLease) Downgrade(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.shared {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fd := int(l.file.Fd()) //nolint:gosec // G115: round-trip of an OS descriptor returned by os.File.Fd
	for {
		err := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err == nil:
			l.shared = true
			return nil
		case errors.Is(err, unix.EWOULDBLOCK):
			// Занято другим процессом — ждём, но не насмерть.
		default:
			return err
		}
		if err := waitBeforeRetry(ctx); err != nil {
			return err
		}
	}
}

func (l *fileLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	fd := int(f.Fd()) //nolint:gosec // round-trip of an OS descriptor
	err := unix.Flock(fd, unix.LOCK_UN)
	return errors.Join(err, f.Close())
}
