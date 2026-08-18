package jobqueue_test

// Пул исполнителей очереди (план 130, issue #848).
//
// Тесты идут публичным путём — Enqueue/Run/Cancel/Task, — а не по внутренностям
// пула: очередь наблюдают ровно этими методами и DSL, и админка.
//
// Параллелизм проверяется матрицей диалектов намеренно. На SQLite исполнитель
// один по построению (одно соединение на базу), на PostgreSQL — сколько
// настроено; ожидание в тесте берётся из самого пула, поэтому «сколько
// обещали, столько и работает» доказывается на обоих бэкендах, а не только там,
// где удобно.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/jobqueue"
	"github.com/ivantit66/onebase/internal/storage"
)

// fakeExec — исполнитель-заглушка: считает одновременно работающих и отдаёт
// управление телу теста.
type fakeExec struct {
	mu         sync.Mutex
	running    int
	maxRunning int
	calls      int

	started chan struct{}
	run     func(ctx context.Context, params map[string]any) (string, error)
}

func newFakeExec(run func(ctx context.Context, params map[string]any) (string, error)) *fakeExec {
	return &fakeExec{started: make(chan struct{}, 64), run: run}
}

func (f *fakeExec) ExecuteJobOnce(ctx context.Context, jobName string, params map[string]any) (string, error) {
	f.mu.Lock()
	f.running++
	f.calls++
	if f.running > f.maxRunning {
		f.maxRunning = f.running
	}
	f.mu.Unlock()
	f.started <- struct{}{}
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	return f.run(ctx, params)
}

func (f *fakeExec) peak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxRunning
}

func (f *fakeExec) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// startPool поднимает пул и гасит его по завершении теста.
func startPool(t *testing.T, db *storage.DB, exec jobqueue.Executor, cfg jobqueue.Config) (*jobqueue.Pool, func() error) {
	t.Helper()
	if err := db.EnsureJobQueueSchema(context.Background()); err != nil {
		t.Fatalf("EnsureJobQueueSchema: %v", err)
	}
	pool := jobqueue.New(db, exec, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			t.Fatal("пул не остановился за 30 с")
			return nil
		}
	}
	t.Cleanup(func() { _ = stop() })
	return pool, stop
}

func testConfig(workers int) jobqueue.Config {
	cfg := jobqueue.DefaultConfig()
	cfg.Workers = workers
	cfg.PollInterval = 20 * time.Millisecond
	cfg.Lease = 3 * time.Second
	cfg.RetryBackoff = 10 * time.Millisecond
	cfg.DrainTimeout = 5 * time.Second
	return cfg
}

// waitStatus опрашивает задачу до нужного статуса.
func waitStatus(t *testing.T, pool *jobqueue.Pool, id uuid.UUID, want string) storage.JobTask {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		task, err := pool.Task(context.Background(), id)
		if err != nil {
			t.Fatalf("Task: %v", err)
		}
		if task != nil {
			last = task.Status
			if task.Status == want {
				return *task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("задача %s не дошла до статуса %q, последний = %q", id, want, last)
	return storage.JobTask{}
}

func waitStarts(t *testing.T, exec *fakeExec, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-exec.started:
		case <-time.After(15 * time.Second):
			t.Fatalf("дождались только %d стартов из %d", i, n)
		}
	}
}

func TestPool_ИсполняетРовноСтолькоЗадачСколькоИсполнителей(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		release := make(chan struct{})
		exec := newFakeExec(func(ctx context.Context, _ map[string]any) (string, error) {
			select {
			case <-release:
				return "готово", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
		pool, _ := startPool(t, db, exec, testConfig(3))
		workers := pool.Workers()
		if workers < 1 {
			t.Fatalf("исполнителей %d — пул не поднялся", workers)
		}

		total := workers + 2
		for i := 0; i < total; i++ {
			if _, _, err := pool.Enqueue(context.Background(), "ОбменСУзлом",
				map[string]any{"Узел": i}, ""); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}

		// Столько, сколько исполнителей, стартует; следующая ждёт освобождения
		// слота — иначе пул не ограничивал бы параллелизм вовсе.
		waitStarts(t, exec, workers)
		select {
		case <-exec.started:
			t.Fatalf("стартовало больше задач, чем исполнителей (%d)", workers)
		case <-time.After(300 * time.Millisecond):
		}
		if got := exec.peak(); got != workers {
			t.Fatalf("одновременно работало %d задач, ожидалось %d", got, workers)
		}

		close(release)
		deadline := time.Now().Add(20 * time.Second)
		for {
			stats, err := pool.Stats(context.Background())
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats[storage.JobTaskDone] == int64(total) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("выполнено %d задач из %d", stats[storage.JobTaskDone], total)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if got := exec.peak(); got > workers {
			t.Fatalf("пик параллелизма %d превысил число исполнителей %d", got, workers)
		}
	})
}

func TestPool_ДваПулаНаОднойБазеНеБерутЗадачуДважды(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		var mu sync.Mutex
		seen := map[string]int{}
		record := func(_ context.Context, params map[string]any) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			seen[params["Узел"].(string)]++
			return "готово", nil
		}
		first := newFakeExec(record)
		second := newFakeExec(record)

		// Два пула на одной базе — это и есть проверка захвата: на PostgreSQL
		// строки расходятся через FOR UPDATE SKIP LOCKED, на SQLite их
		// сериализует единственный писатель. Двойное исполнение здесь означало
		// бы двойной обмен с узлом в бою.
		poolA, _ := startPool(t, db, first, testConfig(3))
		poolB, _ := startPool(t, db, second, testConfig(3))

		const total = 12
		for i := 0; i < total; i++ {
			node := "N-" + strconv.Itoa(i)
			if _, _, err := poolA.Enqueue(context.Background(), "ОбменСУзлом",
				map[string]any{"Узел": node}, node); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}

		deadline := time.Now().Add(30 * time.Second)
		for {
			stats, err := poolB.Stats(context.Background())
			if err != nil {
				t.Fatalf("Stats: %v", err)
			}
			if stats[storage.JobTaskDone] == total {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("выполнено %d задач из %d", stats[storage.JobTaskDone], total)
			}
			time.Sleep(20 * time.Millisecond)
		}

		mu.Lock()
		defer mu.Unlock()
		if len(seen) != total {
			t.Fatalf("исполнено %d разных задач из %d", len(seen), total)
		}
		for node, count := range seen {
			if count != 1 {
				t.Fatalf("задача узла %s исполнена %d раз", node, count)
			}
		}
	})
}

func TestPool_УпавшаяЗадачаПовторяетсяИУходитВКарантин(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		exec := newFakeExec(func(context.Context, map[string]any) (string, error) {
			return "", errors.New("узел не ответил")
		})
		cfg := testConfig(1)
		cfg.MaxAttempts = 2
		pool, _ := startPool(t, db, exec, cfg)

		task, _, err := pool.Enqueue(context.Background(), "ОбменСУзлом", nil, "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		dead := waitStatus(t, pool, task.ID, storage.JobTaskDead)
		if dead.Attempts != 2 {
			t.Fatalf("попыток до карантина %d, ожидалось 2", dead.Attempts)
		}
		if dead.Error != "узел не ответил" {
			t.Fatalf("причина падения потеряна: %q", dead.Error)
		}
		if got := exec.attempts(); got != 2 {
			t.Fatalf("обработка вызвана %d раз, ожидалось 2", got)
		}
	})
}

func TestPool_ОтменаСнимаетОжидающуюИПрерываетИсполняемую(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		exec := newFakeExec(func(ctx context.Context, _ map[string]any) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
		pool, _ := startPool(t, db, exec, testConfig(1))
		ctx := context.Background()

		busy, _, err := pool.Enqueue(ctx, "ОбменСУзлом", nil, "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		waitStarts(t, exec, 1)

		// Ожидающая задача снимается сразу и исполнителя не увидит вовсе.
		waiting, _, err := pool.Enqueue(ctx, "ОбменСУзлом", nil, "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		state, err := pool.Cancel(ctx, waiting.ID)
		if err != nil {
			t.Fatalf("Cancel (ожидающая): %v", err)
		}
		if state != storage.JobTaskCancelled {
			t.Fatalf("ожидающая отменилась как %q, ожидалось cancelled", state)
		}

		state, err = pool.Cancel(ctx, busy.ID)
		if err != nil {
			t.Fatalf("Cancel (исполняемая): %v", err)
		}
		if state != "cancelling" {
			t.Fatalf("исполняемая отменилась как %q, ожидалось cancelling", state)
		}
		cancelled := waitStatus(t, pool, busy.ID, storage.JobTaskCancelled)
		if cancelled.Attempts != 1 {
			t.Fatalf("попыток у отменённой %d, ожидалась 1", cancelled.Attempts)
		}
		if got := exec.attempts(); got != 1 {
			t.Fatalf("обработка вызвана %d раз — снятая из очереди задача не должна исполняться", got)
		}
	})
}

func TestPool_ОстановкаДожидаетсяВзятыхЗадач(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		exec := newFakeExec(func(context.Context, map[string]any) (string, error) {
			time.Sleep(300 * time.Millisecond)
			return "готово", nil
		})
		pool, stop := startPool(t, db, exec, testConfig(1))

		task, _, err := pool.Enqueue(context.Background(), "ОбменСУзлом", nil, "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		waitStarts(t, exec, 1)
		if err := stop(); err != nil {
			t.Fatalf("дренаж вернул ошибку: %v", err)
		}
		// Дренаж обязан дождаться взятой задачи: иначе остановка сервера
		// превращала бы каждую активную задачу в повтор.
		done, err := pool.Task(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("Task: %v", err)
		}
		if done.Status != storage.JobTaskDone {
			t.Fatalf("после дренажа статус %q, ожидался done", done.Status)
		}
	})
}

func TestPool_НеуспевшаяЗадачаВозвращаетсяВОчередьАНеЗависает(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		release := make(chan struct{})
		defer close(release)
		exec := newFakeExec(func(ctx context.Context, _ map[string]any) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
				return "готово", nil
			}
		})
		cfg := testConfig(1)
		cfg.DrainTimeout = 100 * time.Millisecond
		pool, stop := startPool(t, db, exec, cfg)

		task, _, err := pool.Enqueue(context.Background(), "ОбменСУзлом", nil, "")
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		waitStarts(t, exec, 1)
		if err := stop(); err == nil {
			t.Fatal("дренаж по таймауту обязан сообщить об этом ошибкой")
		}
		// Прерванная задача не остаётся running: иначе её пришлось бы ждать до
		// истечения аренды на ровном месте.
		back := waitStatus(t, pool, task.ID, storage.JobTaskPending)
		if back.Attempts != 1 {
			t.Fatalf("попыток у возвращённой задачи %d, ожидалась 1", back.Attempts)
		}
	})
}

func TestPool_ВыключеннаяОчередьОтказываетВПостановке(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if err := db.EnsureJobQueueSchema(context.Background()); err != nil {
			t.Fatalf("EnsureJobQueueSchema: %v", err)
		}
		cfg := testConfig(0)
		pool := jobqueue.New(db, newFakeExec(nil), cfg)
		if pool.Enabled() {
			t.Fatal("очередь с queue.workers=0 считает себя включённой")
		}
		_, _, err := pool.Enqueue(context.Background(), "ОбменСУзлом", nil, "")
		if !errors.Is(err, jobqueue.ErrQueueDisabled) {
			t.Fatalf("постановка в выключенную очередь вернула %v, ожидался ErrQueueDisabled", err)
		}
		// Тихо копить задачи, которые никто не возьмёт, — худший вариант: в базе
		// не должно остаться следа.
		stats, err := pool.Stats(context.Background())
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if stats[storage.JobTaskPending] != 0 {
			t.Fatalf("в выключенной очереди осело %d задач", stats[storage.JobTaskPending])
		}
	})
}

func TestPool_НаSQLiteПараллелизмСрезаетсяДоОдного(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, t.TempDir()+"/queue.db")
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	pool := jobqueue.New(db, newFakeExec(nil), testConfig(16))
	if got := pool.Workers(); got != 1 {
		t.Fatalf("на SQLite поднято %d исполнителей, ожидался 1", got)
	}
	// Деградация обязана быть видимой: молча делать вид, что работают 16
	// исполнителей, значило бы врать в мониторе очереди.
	if !pool.Degraded() {
		t.Fatal("пул не сообщает о деградации параллелизма на SQLite")
	}
}

func TestPool_КлючИдемпотентностиВозвращаетЖивуюЗадачу(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		release := make(chan struct{})
		defer close(release)
		exec := newFakeExec(func(ctx context.Context, _ map[string]any) (string, error) {
			select {
			case <-release:
				return "готово", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
		pool, _ := startPool(t, db, exec, testConfig(1))
		ctx := context.Background()

		first, created, err := pool.Enqueue(ctx, "ОбменСУзлом", nil, "обмен-N-042")
		if err != nil || !created {
			t.Fatalf("первая постановка: created=%v err=%v", created, err)
		}
		second, created, err := pool.Enqueue(ctx, "ОбменСУзлом", nil, "обмен-N-042")
		if err != nil {
			t.Fatalf("вторая постановка: %v", err)
		}
		if created || second.ID != first.ID {
			t.Fatalf("ключ идемпотентности не сработал: created=%v id=%s", created, second.ID)
		}
	})
}
