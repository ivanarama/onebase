package scheduler

// Административное включение/выключение заданий (#991): гейт на тике читает
// решение из _settings свежо, поэтому тумблер подхватывается без пересборки
// cron. Тесты гоняют настоящий тик robfig/cron (@every 1s), а не дёргают
// колбэк напрямую: проверяем публичный путь — от расписания до записи в
// _scheduled_runs.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/assert"
)

// runCount считает записи прогонов задания в журнале.
func runCount(t *testing.T, db *storage.DB, job string) int {
	t.Helper()
	runs, err := db.ScheduledRuns(context.Background(), job, 100)
	if err != nil {
		t.Fatalf("ScheduledRuns(%s): %v", job, err)
	}
	return len(runs)
}

func TestТикУважаетАдминистративноеВключение(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:     "Молчун",
		Schedule: "@every 1s",
		Enabled:  false, // выключен в конфигурации
		Timeout:  10,
	}}); err != nil {
		t.Fatal(err)
	}

	runCtx, stop := context.WithCancel(ctx)
	go func() { _ = sched.Run(runCtx) }()
	defer stop()

	// Выключенное в конфигурации не тикает.
	assert.Never(t, func() bool { return runCount(t, db, "Молчун") > 0 },
		1200*time.Millisecond, 200*time.Millisecond, "выключенное в конфигурации задание запускается")

	// Администратор включил — со следующего тика пошло. Интерпретатора нет,
	// поэтому прогон завершится ошибкой процессора; для гейта важно, что
	// запись прогона вообще появилась.
	assert.NoError(t, db.SaveScheduledEnabled(ctx, "Молчун", true))
	assert.Eventually(t, func() bool { return runCount(t, db, "Молчун") >= 1 },
		5*time.Second, 200*time.Millisecond, "после включения прогон не появился")

	// Администратор выключил — прогоны прекращаются. Первый замер после
	// паузы больше периода: поздний тик успевает попасть в счётчик, второй
	// замер через ещё один период должен совпасть с первым.
	assert.NoError(t, db.SaveScheduledEnabled(ctx, "Молчун", false))
	time.Sleep(1300 * time.Millisecond)
	before := runCount(t, db, "Молчун")
	time.Sleep(1300 * time.Millisecond)
	assert.Equal(t, before, runCount(t, db, "Молчун"),
		"после выключения прогоны продолжаются")
}

func TestТикУважаетАдминистративноеВыключение(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	// Включено в конфигурации, но администратор базы выключил — решение
	// записано ДО старта, тики гейтятся с самого первого.
	assert.NoError(t, db.SaveScheduledEnabled(ctx, "АвтоОтчёт", false))
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:     "АвтоОтчёт",
		Schedule: "@every 1s",
		Enabled:  true,
		Timeout:  10,
	}}); err != nil {
		t.Fatal(err)
	}

	runCtx, stop := context.WithCancel(ctx)
	go func() { _ = sched.Run(runCtx) }()
	defer stop()

	assert.Never(t, func() bool { return runCount(t, db, "АвтоОтчёт") > 0 },
		2500*time.Millisecond, 200*time.Millisecond,
		"выключенное администратором задание запускается по расписанию")
}

func TestТикГейтитНативноеЗадание(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	assert.NoError(t, db.SaveScheduledEnabled(ctx, "НативноеЗадание", false))
	assert.NoError(t, sched.RegisterGoJob("НативноеЗадание", "", "@every 1s",
		func(context.Context) error { return nil }))

	runCtx, stop := context.WithCancel(ctx)
	go func() { _ = sched.Run(runCtx) }()
	defer stop()

	assert.Never(t, func() bool { return runCount(t, db, "НативноеЗадание") > 0 },
		2500*time.Millisecond, 200*time.Millisecond,
		"выключенное администратором нативное задание запускается")
}

func TestRunNowРаботаетДляВыключенного(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:    "РучнойПрогон",
		Enabled: false,
	}}); err != nil {
		t.Fatal(err)
	}

	// Ручной запуск — не то же самое, что работа по расписанию: доступен и
	// для выключенного (в конфигурации или администратором).
	runID, err := sched.RunNow(ctx, "РучнойПрогон")
	if err != nil {
		t.Fatalf("RunNow для выключенного: %v", err)
	}
	assert.NotEqual(t, uuid.Nil, runID)

	assert.Eventually(t, func() bool {
		runs, err := db.ScheduledRuns(ctx, "РучнойПрогон", 10)
		return err == nil && len(runs) >= 1
	}, 5*time.Second, 100*time.Millisecond, "ручной прогон не записан в журнал")
}

func TestJobStatesНакладываетРешения(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{
		{Name: "ВключеноеКонфигурацией", Enabled: true},
		{Name: "ВыключенноеКонфигурацией", Enabled: false},
	}); err != nil {
		t.Fatal(err)
	}

	// Два решения поверх двух конфигурационных дефолтов — все четыре
	// состояния источника, которые показывает админка.
	assert.NoError(t, db.SaveScheduledEnabled(ctx, "ВключеноеКонфигурацией", false))  // выключено администратором
	assert.NoError(t, db.SaveScheduledEnabled(ctx, "ВыключенноеКонфигурацией", true)) // включено администратором

	byName := map[string]JobState{}
	for _, st := range sched.JobStates(ctx) {
		byName[st.Job.Name] = st
	}

	st := byName["ВключеноеКонфигурацией"]
	assert.True(t, st.Job.Enabled)
	assert.True(t, st.OverrideSet)
	assert.False(t, st.OverrideOn)
	assert.False(t, st.EffectiveOn, "решение администратора не перекрыло конфигурацию")

	st = byName["ВыключенноеКонфигурацией"]
	assert.False(t, st.Job.Enabled)
	assert.True(t, st.OverrideSet)
	assert.True(t, st.OverrideOn)
	assert.True(t, st.EffectiveOn, "решение администратора не включило задание")

	// Точечный запрос в другом регистре — тот же расчёт.
	one := sched.JobStateByName(ctx, "  ВЫКЛЮЧЕННОЕКОНФИГУРАЦИЕЙ ")
	if assert.NotNil(t, one) {
		assert.True(t, one.OverrideSet)
		assert.True(t, one.EffectiveOn)
	}
	assert.Nil(t, sched.JobStateByName(ctx, "НетТакогоЗадания"))
}

func TestJobStatesБезБазыСледуютКонфигурации(t *testing.T) {
	sched := New(nil, nil, nil)
	if err := sched.LoadJobs([]*metadata.ScheduledJob{
		{Name: "Вкл", Enabled: true},
		{Name: "Выкл", Enabled: false},
	}); err != nil {
		t.Fatal(err)
	}

	states := sched.JobStates(context.Background())
	if len(states) != 2 {
		t.Fatalf("состояний %d, ожидалось 2", len(states))
	}
	for _, st := range states {
		assert.False(t, st.OverrideSet, "без базы не бывает решений")
		assert.Equal(t, st.Job.Enabled, st.EffectiveOn)
	}
}

func TestПустоеРасписаниеЗаданиеПоТребованию(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	// Регрессия: раньше включённое задание с пустым расписанием валило
	// LoadJobs ошибкой парсера cron, хотя metadata.ValidateSchedule обещает
	// «в cron просто не заводится» (задание только по кнопке).
	err := sched.LoadJobs([]*metadata.ScheduledJob{{
		Name:    "ТолькоКнопка",
		Enabled: true,
	}})
	if err != nil {
		t.Fatalf("LoadJobs с пустым расписанием: %v", err)
	}

	if _, err := sched.RunNow(ctx, "ТолькоКнопка"); err != nil {
		t.Fatalf("RunNow для задания без расписания: %v", err)
	}
}
