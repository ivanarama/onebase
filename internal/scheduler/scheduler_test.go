package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/assert"
)

func openSchedulerTestDB(t *testing.T) (*storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		t.Fatalf("EnsureScheduledRunsTable: %v", err)
	}
	return db, ctx
}

func TestShutdownDrainsRunningGoJob(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	started := make(chan struct{})
	release := make(chan struct{})
	jobCtx, done, err := sched.beginJob("SlowJob")
	assert.NoError(t, err)
	go func() {
		defer done()
		sched.executeGoJob(jobCtx, "SlowJob", func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- sched.Shutdown(context.Background())
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before active job completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	assert.NoError(t, <-shutdownDone)

	runs, err := db.ScheduledRuns(ctx, "SlowJob", 1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusSuccess, runs[0].Status)
		assert.NotNil(t, runs[0].FinishedAt)
	}
}

func TestRunReturnsQuiesceTimeout(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	sched.shutdownTimeout = 20 * time.Millisecond

	_, done, err := sched.beginJob("StuckJob")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- sched.Run(ctx) }()
	cancel()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run hid the scheduler quiesce timeout")
	}

	done()
}

func TestBeginQuiescePermanentlyRejectsNewJobs(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	sched.BeginQuiesce()

	if _, _, err := sched.beginJob("LateJob"); !errors.Is(err, ErrSchedulerStopping) {
		t.Fatalf("beginJob after BeginQuiesce = %v, want ErrSchedulerStopping", err)
	}
	if err := sched.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sched.beginJob("LaterJob"); !errors.Is(err, ErrSchedulerStopping) {
		t.Fatalf("beginJob after finishShutdown = %v, want persistent ErrSchedulerStopping", err)
	}
}

func TestAcceptedGoRunNeverLeavesHistoryRunning(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	var info RunInfo

	jobCtx, done, err := sched.beginJob("DemoReset")
	if err != nil {
		t.Fatal(err)
	}
	sched.executeGoJob(jobCtx, "DemoReset", func(ctx context.Context) error {
		var ok bool
		info, ok = CurrentRun(ctx)
		if !ok {
			return errors.New("current run info missing")
		}
		return Accepted("offline reset request accepted")
	})
	done()

	runs, err := db.ScheduledRuns(ctx, "DemoReset", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != runStatusAccepted || runs[0].FinishedAt == nil ||
		runs[0].Output != "offline reset request accepted" || runs[0].ID != info.ID {
		t.Fatalf("accepted run was not durably finalized: %+v; info=%+v", runs, info)
	}

	offlineErr := errors.New("archive validation failed")
	if err := db.UpdateScheduledRun(ctx, info.ID, runStatusError, "", offlineErr.Error(), time.Since(info.StartedAt).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	runs, err = db.ScheduledRuns(ctx, "DemoReset", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != runStatusError || runs[0].Error != offlineErr.Error() || runs[0].FinishedAt == nil {
		t.Fatalf("accepted run did not publish offline failure: %+v", runs)
	}
}

func TestShutdownDeadlineMarksRunningGoJobInterrupted(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	started := make(chan struct{})
	release := make(chan struct{})
	jobDone := make(chan struct{})
	jobCtx, done, err := sched.beginJob("BlockedJob")
	assert.NoError(t, err)
	go func() {
		defer close(jobDone)
		defer done()
		sched.executeGoJob(jobCtx, "BlockedJob", func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = sched.Shutdown(shutdownCtx)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)

	runs, err := db.ScheduledRuns(ctx, "BlockedJob", 1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusInterrupted, runs[0].Status)
		assert.Equal(t, "scheduler shutdown interrupted", runs[0].Error)
	}

	close(release)
	<-jobDone

	runs, err = db.ScheduledRuns(ctx, "BlockedJob", 1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusInterrupted, runs[0].Status)
	}

	assert.Eventually(t, func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return !sched.stopping
	}, time.Second, 10*time.Millisecond)
	nextCtx, nextDone, err := sched.beginJob("AfterShutdown")
	assert.NoError(t, err)
	assert.NotNil(t, nextCtx)
	nextDone()
}

func TestRunNowGoJobIsSingleFlight(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := sched.RegisterGoJob("NativeJob", "Native", "@every 1h", func(context.Context) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	assert.NoError(t, sched.RunNow(context.Background(), "nativejob"))
	<-started
	err := sched.RunNow(context.Background(), "NativeJob")
	assert.ErrorIs(t, err, ErrJobAlreadyRunning)

	close(release)
	assert.NoError(t, sched.Shutdown(context.Background()))
	runs, err := db.ScheduledRuns(ctx, "NativeJob", 10)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusSuccess, runs[0].Status)
	}
}

func TestBeginJobSerializesNamesCaseInsensitively(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	_, done, err := sched.beginJob("Nightly")
	assert.NoError(t, err)
	_, _, err = sched.beginJob(" nightly ")
	assert.ErrorIs(t, err, ErrJobAlreadyRunning)
	done()

	_, nextDone, err := sched.beginJob("NIGHTLY")
	assert.NoError(t, err)
	nextDone()
}

func TestRunInsertedAfterCancellationIsMarkedInterrupted(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	_, done, err := sched.beginJob("LateInsert")
	assert.NoError(t, err)
	startedAt := time.Now()
	runID, err := db.InsertScheduledRun(ctx, "LateInsert", startedAt)
	assert.NoError(t, err)

	sched.cancelActiveJobs()
	sched.trackActiveRun(runID, "LateInsert", startedAt)
	assert.False(t, sched.finishActiveRun(runID))
	done()

	runs, err := db.ScheduledRuns(ctx, "LateInsert", 1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusInterrupted, runs[0].Status)
	}
}

func TestGoJobPanicIsRecordedAsError(t *testing.T) {
	db, ctx := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	if err := sched.RegisterGoJob("PanickingJob", "Panic", "@every 1h", func(context.Context) error {
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}

	assert.NoError(t, sched.RunNow(context.Background(), "PanickingJob"))
	assert.NoError(t, sched.Shutdown(context.Background()))
	runs, err := db.ScheduledRuns(ctx, "PanickingJob", 1)
	assert.NoError(t, err)
	if assert.Len(t, runs, 1) {
		assert.Equal(t, runStatusError, runs[0].Status)
		assert.True(t, strings.Contains(runs[0].Error, "panic: boom"), "error = %q", runs[0].Error)
	}
}

func TestReloadInvalidSchedulePreservesCurrentPlan(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	valid := []*metadata.ScheduledJob{{
		Name: "Valid", Schedule: "@every 1h", Enabled: true,
	}}
	assert.NoError(t, sched.LoadJobs(valid))
	assert.NoError(t, sched.RegisterGoJob("Native", "Native", "@every 2h", func(context.Context) error { return nil }))

	sched.mu.Lock()
	oldCron := sched.cron
	sched.mu.Unlock()
	err := sched.Reload([]*metadata.ScheduledJob{{
		Name: "Broken", Schedule: "definitely-not-cron", Enabled: true,
	}})
	assert.Error(t, err)

	sched.mu.Lock()
	assert.Same(t, oldCron, sched.cron)
	_, nativePreserved := sched.goJobs[jobKey("Native")]
	sched.mu.Unlock()
	assert.True(t, nativePreserved)
	jobs := sched.Jobs()
	if assert.Len(t, jobs, 2) {
		assert.Equal(t, "Valid", jobs[0].Name)
		assert.Equal(t, "Native", jobs[1].Name)
	}
}

func TestReloadProjectJobsPreservesNativeJobs(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	assert.NoError(t, sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "Before", Schedule: "@every 1h", Enabled: true,
	}}))
	nativeRan := make(chan struct{}, 1)
	assert.NoError(t, sched.RegisterGoJob("Native", "Native", "@every 2h", func(context.Context) error {
		nativeRan <- struct{}{}
		return nil
	}))

	after := []*metadata.ScheduledJob{{
		Name: "After", Schedule: "@every 3h", Enabled: true,
	}}
	assert.NoError(t, sched.ValidateProjectJobs(after))
	assert.NoError(t, sched.ReloadProjectJobs(after))

	assert.Nil(t, sched.GetJob("Before"))
	assert.NotNil(t, sched.GetJob("After"))
	assert.NotNil(t, sched.GetJob("Native"))
	assert.NoError(t, sched.RunNow(context.Background(), "native"))
	select {
	case <-nativeRan:
	case <-time.After(time.Second):
		t.Fatal("preserved native callback was not executed")
	}
	assert.NoError(t, sched.Shutdown(context.Background()))
}

func TestValidateProjectJobsRejectsNativeNameCollisionWithoutMutation(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	assert.NoError(t, sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "Before", Schedule: "@every 1h", Enabled: true,
	}}))
	assert.NoError(t, sched.RegisterGoJob("AutoBackup", "Backup", "@every 2h", func(context.Context) error {
		return nil
	}))

	err := sched.ValidateProjectJobs([]*metadata.ScheduledJob{{
		Name: "autobackup", Schedule: "@every 3h", Enabled: true,
	}})
	assert.ErrorContains(t, err, "duplicate job name")
	assert.NotNil(t, sched.GetJob("Before"))
	assert.NotNil(t, sched.GetJob("AutoBackup"))
}

func TestReloadRunningSchedulerSwapsCompletePlan(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	assert.NoError(t, sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "Before", Schedule: "@every 1h", Enabled: true,
	}}))

	runCtx, cancel := context.WithCancel(context.Background())
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		sched.Start(runCtx)
	}()
	assert.Eventually(t, func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return sched.running
	}, time.Second, 10*time.Millisecond)
	sched.mu.Lock()
	oldCron := sched.cron
	sched.mu.Unlock()

	assert.NoError(t, sched.Reload([]*metadata.ScheduledJob{{
		Name: "After", Schedule: "@every 2h", Enabled: true,
	}}))
	sched.mu.Lock()
	assert.NotSame(t, oldCron, sched.cron)
	assert.True(t, sched.running)
	sched.mu.Unlock()
	jobs := sched.Jobs()
	if assert.Len(t, jobs, 1) {
		assert.Equal(t, "After", jobs[0].Name)
	}

	cancel()
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestJobsReturnsDefensiveCopies(t *testing.T) {
	db, _ := openSchedulerTestDB(t)
	sched := New(db, nil, nil)
	assert.NoError(t, sched.LoadJobs([]*metadata.ScheduledJob{{
		Name: "Original", Schedule: "@every 1h", Params: map[string]any{"value": "original"},
	}}))

	jobs := sched.Jobs()
	jobs[0].Name = "Mutated"
	jobs[0].Params["value"] = "mutated"
	got := sched.GetJob("Original")
	if assert.NotNil(t, got) {
		assert.Equal(t, "original", got.Params["value"])
	}
}

func TestResolveTemplate_Today(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	result := resolveTemplate("{{today}}", now)
	got, ok := result.(time.Time)
	assert.True(t, ok)
	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.May, got.Month())
	assert.Equal(t, 5, got.Day())
	assert.Equal(t, 0, got.Hour())
}

func TestResolveTemplate_MinusDays(t *testing.T) {
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	result := resolveTemplate("{{today | minus_days:7}}", now)
	got, ok := result.(time.Time)
	assert.True(t, ok)
	assert.Equal(t, time.May, got.Month())
	assert.Equal(t, 3, got.Day())
}

func TestResolveTemplate_MinusMonths(t *testing.T) {
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	result := resolveTemplate("{{today | minus_months:1}}", now)
	got, ok := result.(time.Time)
	assert.True(t, ok)
	assert.Equal(t, time.April, got.Month())
}

func TestResolveTemplate_StartOfMonth(t *testing.T) {
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	result := resolveTemplate("{{today | start_of_month}}", now)
	got, ok := result.(time.Time)
	assert.True(t, ok)
	assert.Equal(t, 1, got.Day())
}

func TestResolveTemplate_NoTemplate(t *testing.T) {
	now := time.Now()
	result := resolveTemplate("просто строка", now)
	assert.Equal(t, "просто строка", result)
}

func TestResolveParamTemplates_Mixed(t *testing.T) {
	now := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	params := map[string]any{
		"Дата":     "{{today | minus_days:7}}",
		"Процент":  float64(10),
		"Название": "тест",
	}
	result := resolveParamTemplatesAt(params, now)
	got, ok := result["Дата"].(time.Time)
	assert.True(t, ok)
	assert.Equal(t, 28, got.Day()) // 2026-05-05 minus 7 days = April 28
	assert.Equal(t, time.April, got.Month())
	assert.Equal(t, float64(10), result["Процент"])
	assert.Equal(t, "тест", result["Название"])
}
