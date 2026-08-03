package runtime

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// два параллельных вызова с одним и тем же набором
// ключей должны сериализоваться.
func TestLockManager_SerializesSameKey(t *testing.T) {
	mgr := NewLockManager()
	var counter int32
	var maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.Acquire([]string{"reg|номенклатура=Тумбочка"})
			defer mgr.Release([]string{"reg|номенклатура=Тумбочка"})
			cur := atomic.AddInt32(&counter, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur > old {
					if atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
						break
					}
				} else {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&counter, -1)
		}()
	}
	wg.Wait()
	if maxConcurrent > 1 {
		t.Errorf("одинаковый ключ должен сериализоваться, max concurrent = %d", maxConcurrent)
	}
}

// Разные ключи не блокируют друг друга.
func TestLockManager_ParallelDifferentKeys(t *testing.T) {
	mgr := NewLockManager()
	var counter int32
	var maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := []string{"reg|номенклатура=item" + string(rune('A'+idx))} //nolint:gosec // G115: значение приходит из проверенной модели и заведомо укладывается в целевой тип
			mgr.Acquire(key)
			defer mgr.Release(key)
			cur := atomic.AddInt32(&counter, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur > old {
					if atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
						break
					}
				} else {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&counter, -1)
		}(i)
	}
	wg.Wait()
	if maxConcurrent < 2 {
		t.Errorf("разные ключи должны идти параллельно, max concurrent = %d", maxConcurrent)
	}
}

// Несколько ключей за раз — sort обеспечивает безопасный порядок,
// нет deadlock'а если два потока запросили {A,B} и {B,A}.
func TestLockManager_NoDeadlockOnDifferentOrder(t *testing.T) {
	mgr := NewLockManager()
	var wg sync.WaitGroup
	// Add до запуска Wait-горутины: конкурентные Add/Wait — гонка по контракту
	// WaitGroup (ловится -race; см. план 56, CI).
	wg.Add(2)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			var keys []string
			if i == 0 {
				keys = []string{"A", "B"}
			} else {
				keys = []string{"B", "A"}
			}
			for j := 0; j < 10; j++ {
				mgr.Acquire(keys)
				time.Sleep(1 * time.Millisecond)
				mgr.Release(keys)
			}
		}(i)
	}
	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("deadlock — обнаружен timeout")
	}
}

func TestLockManager_DeduplicatesKeys(t *testing.T) {
	mgr := NewLockManager()
	keys := []string{"A", "A", "", "B", "A"}
	mgr.Acquire(keys)
	mgr.Release(keys)
	mgr.mu.Lock()
	n := len(mgr.locks)
	mgr.mu.Unlock()
	if n != 0 {
		t.Fatalf("ожидали пустую карту после Release дубликатов, осталось %d записей", n)
	}
}

// LockObject — DSL-сценарий: Добавить, УстановитьЗначение, Заблокировать,
// Разблокировать.
func TestLockObject_DSLScenario(t *testing.T) {
	mgr := NewLockManager()
	lo := NewLockObject(mgr)

	el := lo.CallMethod("добавить", []any{"РегистрНакопления.ОстаткиТоваров"})
	if el == nil {
		t.Fatal("Добавить вернул nil")
	}
	le, ok := el.(*LockElement)
	if !ok {
		t.Fatalf("Добавить вернул %T, ожидался *LockElement", el)
	}
	le.CallMethod("установитьзначение", []any{"Номенклатура", "Тумбочка"})
	le.CallMethod("установитьзначение", []any{"Склад", "Основной"})

	lo.CallMethod("заблокировать", nil)
	if len(lo.held) != 1 {
		t.Errorf("ожидался 1 удерживаемый ключ, %d", len(lo.held))
	}
	lo.CallMethod("разблокировать", nil)
	if len(lo.held) != 0 {
		t.Errorf("после Разблокировать должно быть 0 ключей, %d", len(lo.held))
	}
}

// Идемпотентность: повторный Release не должен паниковать.
func TestLockObject_ReleaseAllIdempotent(t *testing.T) {
	mgr := NewLockManager()
	lo := NewLockObject(mgr)
	lo.CallMethod("добавить", []any{"X"})
	lo.CallMethod("заблокировать", nil)
	lo.ReleaseAll()
	lo.ReleaseAll() // не должен паниковать
}

// После Release карта мьютексов должна очищаться — утечки памяти нет.
func TestLockManager_ReleaseCleansUp(t *testing.T) {
	mgr := NewLockManager()
	keys := []string{"reg|номенклатура=Тумбочка", "reg|номенклатура=Стул"}
	mgr.Acquire(keys)
	mgr.Release(keys)
	mgr.mu.Lock()
	n := len(mgr.locks)
	mgr.mu.Unlock()
	if n != 0 {
		t.Errorf("ожидали пустую карту после Release, осталось %d записей", n)
	}
}

// refStub имитирует ссылку (interpreter.Ref): String() — отображаемое имя,
// GetRefUUID() — стабильный идентификатор.
type refStub struct{ name, uuid string }

func (r refStub) String() string     { return r.name }
func (r refStub) GetRefUUID() string { return r.uuid }

// Ключ блокировки для ссылочного значения строится по UUID, а не по имени:
// имя ссылки в разных путях проведения бывает то заполненным, то пустым —
// ключи не пересекались бы и блокировка не срабатывала (issue #458).
func TestLockObject_RefValuesKeyedByUUID(t *testing.T) {
	mgr := NewLockManager()
	buildFor := func(v any) string {
		lo := NewLockObject(mgr)
		le := lo.CallMethod("добавить", []any{"ПартииТоваров"}).(*LockElement)
		le.CallMethod("установитьзначение", []any{"Номенклатура", v})
		return lo.buildKeys()[0]
	}
	withName := buildFor(refStub{name: "Тумбочка", uuid: "u-1"})
	withoutName := buildFor(refStub{uuid: "u-1"})
	if withName != withoutName {
		t.Fatalf("ключи должны совпадать независимо от имени ссылки: %q vs %q", withName, withoutName)
	}
	if withName != "ПартииТоваров|номенклатура=u-1" {
		t.Fatalf("ключ = %q, ожидался UUID в значении", withName)
	}
	// Пустой UUID — откат к строковому представлению.
	if k := buildFor(refStub{name: "Тумбочка"}); k != "ПартииТоваров|номенклатура=Тумбочка" {
		t.Fatalf("ключ без UUID = %q", k)
	}
}

// Заблокировать() вызывает advisory-функцию с нормализованными ключами:
// БД-блокировка берётся в момент DSL-вызова, ДО чтения остатков (issue #458).
func TestLockObject_AdvisoryCalledOnLock(t *testing.T) {
	mgr := NewLockManager()
	var got [][]string
	lo := NewLockObject(mgr).WithAdvisory(func(keys []string) {
		got = append(got, keys)
	})
	le := lo.CallMethod("добавить", []any{"Партии"}).(*LockElement)
	le.CallMethod("установитьзначение", []any{"Склад", "Основной"})
	lo.CallMethod("заблокировать", nil)
	defer lo.ReleaseAll()
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "Партии|склад=Основной" {
		t.Fatalf("advisory получил %v", got)
	}
}

// Паника advisory-функции должна сама отпускать внутрипроцессные мьютексы.
// В произвольной DSL-транзакции LockCollector может отсутствовать, поэтому
// полагаться только на внешний defer нельзя: следующий запрос к ключу зависнет.
func TestLockObject_AdvisoryPanicReleasesWithoutCollector(t *testing.T) {
	mgr := NewLockManager()
	lo := NewLockObject(mgr).WithAdvisory(func([]string) {
		panic("лок-таймаут")
	})
	lo.CallMethod("добавить", []any{"X"})
	func() {
		defer func() { _ = recover() }()
		lo.CallMethod("заблокировать", nil)
	}()

	done := make(chan struct{})
	go func() {
		mgr.Acquire([]string{"X|"})
		mgr.Release([]string{"X|"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("мьютекс не освобождён после паники advisory без LockCollector")
	}
}

// Внешний LockCollector после аварийной самоочистки объекта остаётся безопасно
// вызывать из defer Save/postDocument: повторный ReleaseAll идемпотентен.
func TestLockObject_CollectorCleanupAfterAdvisoryPanic(t *testing.T) {
	mgr := NewLockManager()
	collector := NewLockCollector()
	lo := NewLockObjectWithCollector(mgr, collector).WithAdvisory(func([]string) {
		panic("лок-таймаут")
	})
	lo.CallMethod("добавить", []any{"X"})
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		lo.CallMethod("заблокировать", nil)
	}()
	if !panicked {
		t.Fatal("ожидалась паника advisory-функции")
	}
	collector.ReleaseAll()
	mgr.mu.Lock()
	n := len(mgr.locks)
	mgr.mu.Unlock()
	if n != 0 {
		t.Fatalf("после повторной очистки осталось %d блокировок", n)
	}
}

func TestLockCollectorTracksKeysAndReleasesHeldObjects(t *testing.T) {
	mgr := NewLockManager()
	collector := NewLockCollector()
	lo := NewLockObjectWithCollector(mgr, collector)
	el := lo.CallMethod("добавить", []any{"РегистрНакопления.Остатки"})
	le, ok := el.(*LockElement)
	if !ok {
		t.Fatalf("Добавить вернул %T", el)
	}
	le.CallMethod("установитьзначение", []any{"Номенклатура", "Тумбочка"})
	lo.CallMethod("заблокировать", nil)

	keys := collector.Keys()
	if len(keys) != 1 || keys[0] != "РегистрНакопления.Остатки|номенклатура=Тумбочка" {
		t.Fatalf("collector keys = %v", keys)
	}
	collector.ReleaseAll()
	mgr.mu.Lock()
	n := len(mgr.locks)
	mgr.mu.Unlock()
	if n != 0 {
		t.Fatalf("collector.ReleaseAll did not release process locks, left %d", n)
	}
}
