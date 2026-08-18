//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package dblock

// Отмена контекста прерывает ожидание аренды (#962, Н5).
//
// Раньше Downgrade отбрасывал контекст (`_ context.Context`) и брал
// блокирующий LOCK_SH. Если конверсионный зазор перехватывал другой процесс с
// эксклюзивной блокировкой, `onebase run` вставал на старте навсегда, и Ctrl+C
// не помогал: ожидание примитива ОС о контексте ничего не знает. Процесс просто
// молчал — диагностируемость нулевая.
//
// Тест держит файл эксклюзивно из этого же процесса (другой дескриптор — с
// точки зрения flock это другой владелец) и проверяет, что Downgrade сдаётся по
// отмене, а не виснет.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDowngrade_ОтменаКонтекстаПрерываетОжидание(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.lock")

	// Конкурент: держит файл эксклюзивно и не отпускает.
	rival, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rival.Close() }()
	rivalFD := int(rival.Fd()) //nolint:gosec // G115: round-trip of an OS descriptor, как в самой реализации
	if err := unix.Flock(rivalFD, unix.LOCK_EX); err != nil {
		t.Fatalf("конкурент не смог взять LOCK_EX: %v", err)
	}

	// Наша аренда: тот же файл, свой дескриптор.
	mine, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mine.Close() }()
	lease := &fileLease{file: mine}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- lease.Downgrade(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Downgrade вернул успех, хотя аренду держит конкурент")
		}
		if !errors.Is(err, ErrLeaseBusy) {
			t.Errorf("ошибка = %v, ожидалась ErrLeaseBusy — вызывающему нужно отличать "+
				"«база занята» от общего таймаута запуска", err)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Errorf("ожидание длилось %v — отмена почти не подействовала", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Downgrade не вернулся через 5 с после отмены контекста — " +
			"ровно то зависание старта, которое чинится: процесс молчит и не реагирует на Ctrl+C")
	}
}

// Когда никто не мешает, понижение проходит сразу — проверка, что защита от
// зависания не превратилась в отказ на пустом месте.
func TestDowngrade_БезКонкурентаПроходитСразу(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.lock")
	lease, ok, err := tryAcquireFileLease(path, false)
	if err != nil || !ok {
		t.Fatalf("аренда не взята: ok=%v err=%v", ok, err)
	}
	defer func() { _ = lease.Close() }()

	if err := lease.Downgrade(context.Background()); err != nil {
		t.Fatalf("Downgrade без конкурента: %v", err)
	}
	if !lease.shared {
		t.Error("аренда не помечена разделяемой")
	}
}
