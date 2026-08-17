package scheduler

// Уборка журнала прогонов при старте планировщика (#966).
//
// Тест идёт публичным путём — через запуск планировщика, а не вызовом
// tidyRunHistory напрямую: смысл правки в том, что уборка происходит САМА при
// старте. Тест на приватной функции доказал бы только её работоспособность и
// пропустил бы ровно тот дефект, который чинится (правило #611).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestСтартПланировщика_ПомечаетБрошенныеПрогоны(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	// Прогон, оставшийся от процесса, который умер, не дописав результат.
	stale := uuid.New()
	assert.NoError(t, db.InsertScheduledRun(ctx, stale, "Обмен", time.Now().Add(-48*time.Hour)))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(runCtx) }()

	// Даём планировщику встать и прибраться, затем гасим.
	assert.Eventually(t, func() bool {
		run, err := db.ScheduledRunByID(ctx, stale)
		return err == nil && run != nil && run.Status == runStatusInterrupted
	}, 5*time.Second, 20*time.Millisecond,
		"брошенный прогон остался в running — в админке задание вечно «выполняется», "+
			"а цикл ожидания терминального статуса не закончится никогда")

	cancel()
	<-done
}

func TestСтартПланировщика_ПодрезаетСтарыйЖурнал(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	ancient := uuid.New()
	assert.NoError(t, db.InsertScheduledRun(ctx, ancient, "Обмен", time.Now().Add(-200*24*time.Hour)))
	assert.NoError(t, db.UpdateScheduledRun(ctx, ancient, runStatusSuccess, "", "", 5))

	fresh := uuid.New()
	assert.NoError(t, db.InsertScheduledRun(ctx, fresh, "Обмен", time.Now()))
	assert.NoError(t, db.UpdateScheduledRun(ctx, fresh, runStatusSuccess, "", "", 5))

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(runCtx) }()

	assert.Eventually(t, func() bool {
		run, err := db.ScheduledRunByID(ctx, ancient)
		return err == nil && run == nil
	}, 5*time.Second, 20*time.Millisecond, "старый прогон не подрезан — журнал растёт без границ")

	// Свежий обязан пережить уборку: иначе история исчезает вместе с мусором.
	run, err := db.ScheduledRunByID(ctx, fresh)
	assert.NoError(t, err)
	assert.NotNil(t, run, "свежий прогон удалён вместе со старыми")

	cancel()
	<-done
}
