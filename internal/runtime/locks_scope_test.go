package runtime

import (
	"context"
	"testing"
	"time"
)

// Области блокировок: ключ внутри одной операции берётся в менеджере один раз,
// а отпускается, когда его не держит ни один объект области. Другая операция
// при этом по-прежнему ждёт.

func lockKey(mgr *LockManager, c *LockCollector) *LockObject {
	lo := NewLockObjectWithCollector(mgr, c)
	el := lo.CallMethod("Добавить", []any{"Проба"}).(*LockElement)
	el.CallMethod("УстановитьЗначение", []any{"Ключ", "К1"})
	lo.CallMethod("Заблокировать", nil)
	return lo
}

// acquiredWithin — удалось ли другой операции взять ключ за d.
func acquiredWithin(mgr *LockManager, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		other := lockKey(mgr, NewLockCollector())
		other.ReleaseAll()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func TestLockCollectorReentrantWithinScope(t *testing.T) {
	mgr := NewLockManager()
	c := NewLockCollector()
	first := lockKey(mgr, c)
	second := make(chan *LockObject, 1)
	go func() { second <- lockKey(mgr, c) }()
	var again *LockObject
	select {
	case again = <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("второй Заблокировать того же ключа в той же области ждёт сам себя")
	}
	first.ReleaseAll()
	if acquiredWithin(mgr, 200*time.Millisecond) {
		t.Fatal("ключ отпущен, хотя его ещё держит второй объект области")
	}
	again.ReleaseAll()
	if !acquiredWithin(mgr, 2*time.Second) {
		t.Fatal("ключ не отпущен после последнего держателя")
	}
}

func TestLockCollectorInheritsParentKeys(t *testing.T) {
	mgr := NewLockManager()
	parent := NewLockCollector()
	outer := lockKey(mgr, parent)

	ctx := ContextWithExecutionLockCollector(context.Background(), parent)
	child := NewLockCollectorIn(ctx)
	inner := make(chan *LockObject, 1)
	go func() { inner <- lockKey(mgr, child) }()
	select {
	case <-inner:
	case <-time.After(2 * time.Second):
		t.Fatal("вложенная область ждёт ключ, который держит внешняя")
	}
	child.ReleaseAll()
	if acquiredWithin(mgr, 200*time.Millisecond) {
		t.Fatal("вложенная область отпустила ключ внешней")
	}
	outer.ReleaseAll()
	if !acquiredWithin(mgr, 2*time.Second) {
		t.Fatal("ключ не отпущен после внешней области")
	}
}

func TestLockCollectorScopesStillExcludeEachOther(t *testing.T) {
	mgr := NewLockManager()
	held := lockKey(mgr, NewLockCollector())
	if acquiredWithin(mgr, 200*time.Millisecond) {
		t.Fatal("другая операция взяла занятый ключ")
	}
	held.ReleaseAll()
	if !acquiredWithin(mgr, 2*time.Second) {
		t.Fatal("ключ не освободился")
	}
}
