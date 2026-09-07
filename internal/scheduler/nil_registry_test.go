package scheduler

// Планировщик без Registry (#1202). Тик задания-обработки доходил до
// s.reg.GetProcessor на нулевом приёмнике и паниковал; панику ловил recover в
// executeJob, поэтому снаружи она видна только текстом ошибки прогона —
// проверяем публичным путём, от расписания до записи в _scheduled_runs, а не
// вызовом runProcessor напрямую.

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/assert"
)

// finishedRun ждёт завершённый прогон задания в журнале: вставка помечает
// запись как running, статус и текст ошибки дописываются после исполнения.
func finishedRun(t *testing.T, db *storage.DB, job string) storage.ScheduledRun {
	t.Helper()
	var run storage.ScheduledRun
	if !assert.Eventually(t, func() bool {
		runs, err := db.ScheduledRuns(t.Context(), job, 10)
		if err != nil || len(runs) == 0 || runs[0].Status == "running" {
			return false
		}
		run = runs[0]
		return true
	}, 5*time.Second, 100*time.Millisecond, "завершённый прогон задания %s не появился в журнале", job) {
		t.FailNow()
	}
	return run
}

func TestТикБезРеестраДаётОшибкуАНеПанику(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	// Registry нет: так планировщик конструируют тесты пакета и так он живёт,
	// пока конфигурация не загружена.
	sched := New(db, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:      "БезРеестра",
		Processor: "ЛюбаяОбработка",
		Schedule:  "@every 1s",
		Enabled:   true,
		Timeout:   10,
	}}); err != nil {
		t.Fatal(err)
	}

	runSchedulerUntilCleanup(t, sched, ctx)

	run := finishedRun(t, db, "БезРеестра")
	assert.Equal(t, runStatusError, run.Status)
	assert.Equal(t, "processor not found: ЛюбаяОбработка", run.Error,
		"тик без Registry не даёт честной ошибки прогона")
	assert.NotContains(t, run.Error, "panic",
		"тик без Registry паникует на нулевом приёмнике")
}

func TestРучнойПрогонБезРеестраДаётОшибкуАНеПанику(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:      "БезРеестраПоКнопке",
		Processor: "ЛюбаяОбработка",
		Timeout:   10,
	}}); err != nil {
		t.Fatal(err)
	}

	// Второй публичный вход в ту же ветку: RunNow идёт мимо cron, но приводит
	// в тот же executeJob.
	if _, err := sched.RunNow(ctx, "БезРеестраПоКнопке"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	run := finishedRun(t, db, "БезРеестраПоКнопке")
	assert.Equal(t, runStatusError, run.Status)
	assert.Equal(t, "processor not found: ЛюбаяОбработка", run.Error,
		"ручной прогон без Registry не даёт честной ошибки")
}
