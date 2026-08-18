package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	cronlib "github.com/robfig/cron/v3"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

var (
	// ErrNetworkLocked — отказ предохранителя сети (план 62) для DSL заданий.
	// «Что сделать» — из storage, общее с ui.ErrNetworkLocked: путь в
	// конфигураторе должен быть один и тот же во всех отказах.
	ErrNetworkLocked = errors.New("сетевые возможности отключены предохранителем — " + storage.NetworkEnabledHint)
	// ErrJobAlreadyRunning is returned when a cron tick or manual start tries
	// to overlap another execution of the same logical job.
	ErrJobAlreadyRunning = errors.New("scheduled job is already running")
	ErrSchedulerStopping = errors.New("scheduler is stopping")
)

// RunInfo identifies the durable history row of the currently executing
// native Go job. It lets a job that hands work to an offline phase later refine
// an already-finalized "accepted" result without ever leaving a row running.
type RunInfo struct {
	ID        uuid.UUID
	StartedAt time.Time
}

type runInfoContextKey struct{}

func CurrentRun(ctx context.Context) (RunInfo, bool) {
	if ctx == nil {
		return RunInfo{}, false
	}
	info, ok := ctx.Value(runInfoContextKey{}).(RunInfo)
	return info, ok && info.ID != uuid.Nil && !info.StartedAt.IsZero()
}

// AcceptedResult means the native job successfully handed its work to a
// lifecycle phase outside the scheduler. The row is durably finalized as
// "accepted", not "success"; that phase may later replace it with its actual
// success/error. If the later update fails, "accepted" remains truthful.
type AcceptedResult struct {
	Message string
}

func (r *AcceptedResult) Error() string {
	if r == nil || r.Message == "" {
		return "scheduled job accepted for deferred execution"
	}
	return r.Message
}

func Accepted(message string) error {
	return &AcceptedResult{Message: message}
}

type Scheduler struct {
	cron    *cronlib.Cron
	jobs    []*metadata.ScheduledJob
	goJobs  map[string]func(context.Context) error
	db      *storage.DB
	reg     *runtime.Registry
	interp  *interpreter.Interpreter
	log     *slog.Logger
	mailer  *mailer.Mailer
	msgSink func(userID, text string)
	// varsBuilder — внешний сборщик полного DSL-окружения заданий (обычно
	// ui.Server.BuildJobDSLVars): даёт заданиям Справочники/Документы/вложения/
	// транзакции наравне с обработками. nil → базовый набор dslvars.Common.
	varsBuilder VarsBuilder

	mu         sync.Mutex
	running    bool
	stopping   bool
	sealed     bool
	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup
	activeRuns map[uuid.UUID]*activeRun
	activeJobs map[string]struct{}
	// shutdownTimeout is configurable only inside package tests; production
	// schedulers always use defaultShutdownTimeout via New.
	shutdownTimeout time.Duration
}

const (
	defaultShutdownTimeout = 30 * time.Second
	interruptUpdateTimeout = 5 * time.Second

	runStatusSuccess     = "success"
	runStatusAccepted    = "accepted"
	runStatusError       = "error"
	runStatusTimeout     = "timeout"
	runStatusInterrupted = "interrupted"
)

type activeRun struct {
	id        uuid.UUID
	jobName   string
	startedAt time.Time
	finalized bool
}

// SetMessageSink hooks Сообщить() output into an external store (e.g. UI message panel).
// userID is empty string for scheduler context (anonymous/system).
func (s *Scheduler) SetMessageSink(f func(userID, text string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgSink = f
}

// VarsBuilder строит DSL-окружение и возвращает его transaction state.
// messages — общий для задания collector сообщений из вложенных DSL-хуков.
// Scheduler владеет state до конца одного запуска и гарантирует cleanup.
type VarsBuilder func(ctx context.Context, mc *runtime.MovementsCollector, messages *[]string) (map[string]any, *interpreter.TxState)

// SetVarsBuilder подключает внешний сборщик DSL-окружения (см. поле varsBuilder).
func (s *Scheduler) SetVarsBuilder(b VarsBuilder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.varsBuilder = b
}

func New(db *storage.DB, reg *runtime.Registry, interp *interpreter.Interpreter) *Scheduler {
	return &Scheduler{
		cron:            cronlib.New(),
		goJobs:          make(map[string]func(context.Context) error),
		db:              db,
		reg:             reg,
		interp:          interp,
		log:             oblog.Component("scheduler"),
		activeRuns:      make(map[uuid.UUID]*activeRun),
		activeJobs:      make(map[string]struct{}),
		shutdownTimeout: defaultShutdownTimeout,
	}
}

func (s *Scheduler) SetMailer(m *mailer.Mailer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mailer = m
}

// RegisterGoJob добавляет нативное Go-задание в планировщик.
// Результат записывается в _scheduled_runs как обычное задание.
func (s *Scheduler) RegisterGoJob(name, title, schedule string, fn func(ctx context.Context) error) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("scheduler: Go job name is required")
	}
	if fn == nil {
		return fmt.Errorf("scheduler: Go job %s has no function", name)
	}
	key := jobKey(name)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.sealed {
		return ErrSchedulerStopping
	}
	if s.jobByKeyLocked(key) != nil {
		return fmt.Errorf("scheduler: duplicate job name %q", name)
	}
	_, err := s.cron.AddFunc(schedule, s.goJobCallback(name, fn))
	if err != nil {
		return fmt.Errorf("scheduler: RegisterGoJob %s: %w", name, err)
	}
	s.goJobs[key] = fn
	s.jobs = append(s.jobs, cloneScheduledJob(&metadata.ScheduledJob{
		Name:     name,
		Title:    title,
		Schedule: schedule,
		Enabled:  true,
	}))
	return nil
}

func (s *Scheduler) LoadJobs(jobs []*metadata.ScheduledJob) error {
	return s.replaceJobs(jobs)
}

// Reload validates and builds the complete replacement before publishing it.
// If validation fails, the running cron and visible job list stay untouched.
// Native Go jobs are intentionally replaced too; callers register the native
// jobs for the new application configuration after a successful reload.
func (s *Scheduler) Reload(jobs []*metadata.ScheduledJob) error {
	return s.replaceJobs(jobs)
}

// ValidateProjectJobs checks a project-owned job set together with the
// currently registered native Go jobs. It does not change the running
// scheduler. Hot reload uses this before publishing a new metadata registry,
// so an invalid cron or a name collision cannot leave a partial generation.
func (s *Scheduler) ValidateProjectJobs(jobs []*metadata.ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.sealed {
		return ErrSchedulerStopping
	}
	combined, native := s.projectJobsWithNativeLocked(jobs)
	candidate, _, err := s.buildCron(combined, native)
	if candidate != nil {
		candidate.Stop()
	}
	return err
}

// ReloadProjectJobs replaces project-owned scheduled jobs while preserving
// native Go jobs registered by runtime features such as automatic backup and
// demo reset.
func (s *Scheduler) ReloadProjectJobs(jobs []*metadata.ScheduledJob) error {
	s.mu.Lock()
	if s.stopping || s.sealed {
		s.mu.Unlock()
		return ErrSchedulerStopping
	}
	combined, native := s.projectJobsWithNativeLocked(jobs)
	nextCron, nextJobs, err := s.buildCron(combined, native)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	oldCron := s.cron
	running := s.running
	s.cron = nextCron
	s.jobs = nextJobs
	s.goJobs = native
	if running {
		nextCron.Start()
	}
	s.mu.Unlock()

	if oldCron != nil {
		oldCron.Stop()
	}
	return nil
}

func (s *Scheduler) replaceJobs(jobs []*metadata.ScheduledJob) error {
	s.mu.Lock()
	if s.stopping || s.sealed {
		s.mu.Unlock()
		return ErrSchedulerStopping
	}
	nextCron, nextJobs, err := s.buildCron(jobs, nil)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	oldCron := s.cron
	running := s.running
	s.cron = nextCron
	s.jobs = nextJobs
	s.goJobs = make(map[string]func(context.Context) error)
	if running {
		nextCron.Start()
	}
	s.mu.Unlock()

	if oldCron != nil {
		oldCron.Stop()
	}
	return nil
}

func (s *Scheduler) projectJobsWithNativeLocked(jobs []*metadata.ScheduledJob) ([]*metadata.ScheduledJob, map[string]func(context.Context) error) {
	combined := make([]*metadata.ScheduledJob, 0, len(jobs)+len(s.goJobs))
	combined = append(combined, jobs...)
	native := make(map[string]func(context.Context) error, len(s.goJobs))
	for key, fn := range s.goJobs {
		native[key] = fn
	}
	for _, job := range s.jobs {
		if _, ok := native[jobKey(job.Name)]; ok {
			combined = append(combined, job)
		}
	}
	return combined, native
}

func (s *Scheduler) buildCron(jobs []*metadata.ScheduledJob, goJobs map[string]func(context.Context) error) (*cronlib.Cron, []*metadata.ScheduledJob, error) {
	nextCron := cronlib.New()
	nextJobs := make([]*metadata.ScheduledJob, 0, len(jobs))
	names := make(map[string]struct{}, len(jobs))

	for _, source := range jobs {
		if source == nil {
			return nil, nil, errors.New("scheduler: nil job")
		}
		job := cloneScheduledJob(source)
		job.Name = strings.TrimSpace(job.Name)
		if job.Name == "" {
			return nil, nil, errors.New("scheduler: job name is required")
		}
		key := jobKey(job.Name)
		if _, duplicate := names[key]; duplicate {
			return nil, nil, fmt.Errorf("scheduler: duplicate job name %q", job.Name)
		}
		names[key] = struct{}{}
		nextJobs = append(nextJobs, job)
		if !job.Enabled {
			continue
		}
		callback := s.scheduledJobCallback(job)
		if fn, native := goJobs[key]; native {
			callback = s.goJobCallback(job.Name, fn)
		}
		if _, err := nextCron.AddFunc(job.Schedule, callback); err != nil {
			return nil, nil, fmt.Errorf("scheduler: invalid schedule for %s: %w", job.Name, err)
		}
	}
	return nextCron, nextJobs, nil
}

func (s *Scheduler) scheduledJobCallback(job *metadata.ScheduledJob) func() {
	return func() {
		ctx, done, err := s.beginJob(job.Name)
		if err != nil {
			s.logSkippedJob(job.Name, err)
			return
		}
		defer done()
		startedAt := time.Now()
		runID, err := s.beginRun(ctx, job.Name, startedAt)
		if err != nil {
			s.log.Error("scheduler: insert run", "job", job.Name, "err", err)
			return
		}
		s.runScheduledJob(ctx, job, runID, startedAt)
	}
}

func (s *Scheduler) goJobCallback(name string, fn func(context.Context) error) func() {
	return func() {
		ctx, done, err := s.beginJob(name)
		if err != nil {
			s.logSkippedJob(name, err)
			return
		}
		defer done()
		startedAt := time.Now()
		runID, err := s.beginRun(ctx, name, startedAt)
		if err != nil {
			s.log.Error("scheduler: insert go run", "job", name, "err", err)
			return
		}
		s.executeGoJob(ctx, name, fn, runID, startedAt)
	}
}

// beginRun заводит запись прогона и берёт его на учёт.
//
// Вынесено из executeJob/executeGoJob наружу ради #742 (план 123): запуск из
// DSL обязан вернуть идентификатор прогона вызывающему, а значит запись должна
// появиться ДО старта горутины. Побочно это чинит давнюю неприятность —
// раньше сбой вставки логировался и проглатывался, и «запустил» не означало
// «запустилось».
func (s *Scheduler) beginRun(ctx context.Context, jobName string, startedAt time.Time) (uuid.UUID, error) {
	id := uuid.New()
	if err := s.db.InsertScheduledRun(ctx, id, jobName, startedAt); err != nil {
		return uuid.Nil, err
	}
	s.trackActiveRun(id, jobName, startedAt)
	return id, nil
}

func (s *Scheduler) logSkippedJob(name string, err error) {
	if errors.Is(err, ErrJobAlreadyRunning) {
		s.log.Warn("scheduler: overlapping run skipped", "job", name)
		return
	}
	s.log.Debug("scheduler: trigger ignored", "job", name, "err", err)
}

func jobKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cloneScheduledJob(job *metadata.ScheduledJob) *metadata.ScheduledJob {
	if job == nil {
		return nil
	}
	cloned := *job
	if job.Titles != nil {
		cloned.Titles = make(map[string]string, len(job.Titles))
		for key, value := range job.Titles {
			cloned.Titles[key] = value
		}
	}
	if job.Params != nil {
		cloned.Params = make(map[string]any, len(job.Params))
		for key, value := range job.Params {
			cloned.Params[key] = cloneScheduledValue(value)
		}
	}
	return &cloned
}

func cloneScheduledValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneScheduledValue(item)
		}
		return cloned
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneScheduledValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

// Run starts cron, blocks until ctx is cancelled, and returns the result of
// the bounded graceful shutdown. Callers that need to prove all jobs are
// quiescent before an offline database operation must check this error.
func (s *Scheduler) Run(ctx context.Context) error {
	return s.run(ctx, nil)
}

// RunReady is Run with an explicit startup acknowledgement. The ready channel
// is closed after cron and admission are live, so a server cannot race an
// immediate shutdown against a scheduler goroutine that has not started yet.
func (s *Scheduler) RunReady(ctx context.Context, ready chan<- struct{}) error {
	return s.run(ctx, ready)
}

func (s *Scheduler) run(ctx context.Context, ready chan<- struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.stopping || s.sealed {
		s.mu.Unlock()
		return ErrSchedulerStopping
	}
	s.ensureRootLocked()
	s.running = true
	cron := s.cron
	cron.Start()
	s.mu.Unlock()
	s.tidyRunHistory(ctx)
	if ready != nil {
		close(ready)
	}

	<-ctx.Done()

	shutdownTimeout := s.shutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return s.Shutdown(shutdownCtx)
}

// RunHistoryMaxAge — сколько хранится журнал прогонов. Не настройка базы
// намеренно: значение по умолчанию должно быть разумным без вмешательства, а
// тем, кому нужен другой срок, проще будет добавить настройку тогда, когда
// появится живой запрос.
const RunHistoryMaxAge = 90 * 24 * time.Hour

// tidyRunHistory приводит журнал прогонов в порядок при старте: помечает
// брошенные прогоны и подрезает старые (#966).
//
// Делается один раз при запуске, а не по расписанию: обе операции дешёвые, но
// частить ими незачем — брошенные прогоны появляются только после жёсткого
// завершения процесса, то есть ровно перед следующим стартом.
//
// Сбой уборки не должен мешать работе планировщика: журнал — вспомогательная
// вещь, и отказываться стартовать из-за него значило бы разменять работающие
// задания на аккуратность истории.
func (s *Scheduler) tidyRunHistory(ctx context.Context) {
	if s.db == nil {
		return
	}
	if n, err := s.db.SweepStaleScheduledRuns(ctx); err != nil {
		s.log.Warn("scheduler: не удалось пометить брошенные прогоны", "err", err)
	} else if n > 0 {
		s.log.Info("scheduler: брошенные прогоны помечены прерванными", "count", n)
	}
	if n, err := s.db.PruneScheduledRuns(ctx, RunHistoryMaxAge); err != nil {
		s.log.Warn("scheduler: не удалось подрезать журнал прогонов", "err", err)
	} else if n > 0 {
		s.log.Info("scheduler: журнал прогонов подрезан", "removed", n, "older_than", RunHistoryMaxAge)
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	if err := s.Run(ctx); err != nil {
		s.log.Warn("scheduler: shutdown timed out", "err", err)
	}
}

// BeginQuiesce permanently closes job admission for this scheduler instance.
// It is deliberately separate from Shutdown: a server generation can seal the
// scheduler before draining existing jobs while its HTTP/UI dependencies are
// still alive. The instance is discarded after shutdown, so the seal is never
// reopened by finishShutdown.
func (s *Scheduler) BeginQuiesce() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sealed = true
	s.mu.Unlock()
}

func (s *Scheduler) Stop() {
	if err := s.Shutdown(context.Background()); err != nil {
		s.log.Warn("scheduler: stop failed", "err", err)
	}
}

// Shutdown stops cron triggers and waits until already-started jobs finish. If
// ctx expires first, all scheduler job contexts are cancelled and active runs
// are marked as interrupted in _scheduled_runs.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.stopping && !s.running && len(s.activeRuns) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	cron := s.cron
	s.mu.Unlock()

	stopCtx := cron.Stop()
	done := make(chan struct{})
	go func() {
		<-stopCtx.Done()
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.finishShutdown()
		return nil
	case <-ctx.Done():
		s.cancelActiveJobs()
		s.interruptActiveRuns("scheduler shutdown interrupted")
		// Shutdown has already stopped future cron triggers. Let the final
		// active job transition the scheduler out of stopping asynchronously,
		// so a timed-out shutdown does not poison all later manual starts.
		go func() {
			<-done
			s.finishShutdown()
		}()
		return ctx.Err()
	}
}

func (s *Scheduler) ensureRootLocked() {
	if s.rootCtx != nil && s.rootCtx.Err() == nil {
		return
	}
	s.rootCtx, s.rootCancel = context.WithCancel(context.Background())
	if s.activeRuns == nil {
		s.activeRuns = make(map[uuid.UUID]*activeRun)
	}
	if s.activeJobs == nil {
		s.activeJobs = make(map[string]struct{})
	}
}

func (s *Scheduler) beginJob(jobName string) (context.Context, func(), error) {
	s.mu.Lock()
	if s.stopping || s.sealed {
		s.mu.Unlock()
		return nil, nil, ErrSchedulerStopping
	}
	key := jobKey(jobName)
	if key == "" {
		s.mu.Unlock()
		return nil, nil, errors.New("scheduled job name is required")
	}
	if _, running := s.activeJobs[key]; running {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: %s", ErrJobAlreadyRunning, jobName)
	}
	s.ensureRootLocked()
	ctx, cancel := context.WithCancel(s.rootCtx)
	s.activeJobs[key] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()

	var once sync.Once
	done := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			delete(s.activeJobs, key)
			s.mu.Unlock()
			s.wg.Done()
		})
	}
	return ctx, done, nil
}

func (s *Scheduler) finishShutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopping {
		return
	}
	if s.rootCancel != nil {
		s.rootCancel()
	}
	s.rootCtx = nil
	s.rootCancel = nil
	s.running = false
	s.stopping = false
}

func (s *Scheduler) cancelActiveJobs() {
	s.mu.Lock()
	cancel := s.rootCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Scheduler) trackActiveRun(id uuid.UUID, jobName string, startedAt time.Time) {
	s.mu.Lock()
	if s.activeRuns == nil {
		s.activeRuns = make(map[uuid.UUID]*activeRun)
	}
	run := &activeRun{
		id:        id,
		jobName:   jobName,
		startedAt: startedAt,
	}
	// A forced shutdown can race with the INSERT that creates the run row.
	// If cancellation won before this row became visible in activeRuns, mark
	// it interrupted here so it cannot remain "running" forever.
	if s.rootCtx != nil && s.rootCtx.Err() != nil {
		run.finalized = true
	}
	s.activeRuns[id] = run
	interrupted := run.finalized
	interruptedRun := *run
	s.mu.Unlock()

	if interrupted {
		s.markRunInterrupted(interruptedRun, "scheduler shutdown interrupted")
	}
}

// finishActiveRun removes an active run and reports whether the caller should
// write the final status. It returns false when shutdown has already marked
// this run as interrupted.
func (s *Scheduler) finishActiveRun(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.activeRuns[id]
	if run == nil {
		return true
	}
	delete(s.activeRuns, id)
	return !run.finalized
}

func (s *Scheduler) interruptActiveRuns(reason string) {
	s.mu.Lock()
	runs := make([]activeRun, 0, len(s.activeRuns))
	for _, run := range s.activeRuns {
		if run.finalized {
			continue
		}
		run.finalized = true
		runs = append(runs, *run)
	}
	s.mu.Unlock()

	for _, run := range runs {
		s.markRunInterrupted(run, reason)
	}
}

func (s *Scheduler) markRunInterrupted(run activeRun, reason string) {
	durationMs := time.Since(run.startedAt).Milliseconds()
	ctx, cancel := context.WithTimeout(context.Background(), interruptUpdateTimeout)
	err := s.db.UpdateScheduledRun(ctx, run.id, runStatusInterrupted, "", reason, durationMs)
	cancel()
	if err != nil {
		s.log.Warn("scheduler: mark interrupted run failed", "job", run.jobName, "run_id", run.id.String(), "err", err)
	}
	s.log.Warn("scheduler: active job interrupted", "job", run.jobName, "run_id", run.id.String())
}

func (s *Scheduler) Jobs() []*metadata.ScheduledJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*metadata.ScheduledJob, len(s.jobs))
	for i, job := range s.jobs {
		result[i] = cloneScheduledJob(job)
	}
	return result
}

// ActiveRunCount returns the number of scheduled jobs currently executing.
func (s *Scheduler) ActiveRunCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeRuns)
}

func (s *Scheduler) GetJob(name string) *metadata.ScheduledJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobByKeyLocked(jobKey(name)); job != nil {
		return cloneScheduledJob(job)
	}
	return nil
}

func (s *Scheduler) jobByKeyLocked(key string) *metadata.ScheduledJob {
	for _, job := range s.jobs {
		if jobKey(job.Name) == key {
			return job
		}
	}
	return nil
}

// RunNow запускает задание немедленно и возвращает идентификатор прогона.
// Управление возвращается сразу — задание работает в фоне.
//
// Контекст вызывающего не используется намеренно (был `_` и остаётся им по
// смыслу): и само задание, и запись его прогона живут на контексте
// планировщика. Причин две. Первая известна давно: HTTP-запрос админки
// отменяется сразу после редиректа, и задание умирало бы вместе с ним.
// Вторая появилась вместе с синхронной вставкой (#742): `storage.DB.Exec`
// подхватывает транзакцию из контекста, поэтому запуск из кода, идущего
// внутри транзакции, уложил бы строку прогона в чужую транзакцию — задание её
// не увидит, финальный UPDATE не найдёт строку, а откат инициатора сотрёт
// запись уже отработавшего задания.
func (s *Scheduler) RunNow(_ context.Context, jobName string) (uuid.UUID, error) {
	key := jobKey(jobName)
	s.mu.Lock()
	job := cloneScheduledJob(s.jobByKeyLocked(key))
	goJob := s.goJobs[key]
	s.mu.Unlock()
	if job == nil {
		return uuid.Nil, fmt.Errorf("job not found: %s", jobName)
	}
	jobCtx, done, err := s.beginJob(job.Name)
	if err != nil {
		return uuid.Nil, err
	}
	startedAt := time.Now()
	runID, err := s.beginRun(jobCtx, job.Name, startedAt)
	if err != nil {
		// Замок обязан освободиться здесь же: иначе имя задания останется
		// занятым в activeJobs навсегда, и все следующие запуски — включая
		// cron-тик — будут вечно получать «уже выполняется».
		done()
		return uuid.Nil, err
	}
	go func() {
		defer done()
		if goJob != nil {
			s.executeGoJob(jobCtx, job.Name, goJob, runID, startedAt)
			return
		}
		s.runScheduledJob(jobCtx, job, runID, startedAt)
	}()
	return runID, nil
}

// RunByID возвращает прогон по идентификатору или nil, если журнал такого не
// помнит. Нужен запуску из DSL: по возвращённому Запустить() идентификатору
// прикладной код спрашивает статус.
func (s *Scheduler) RunByID(ctx context.Context, id uuid.UUID) (*storage.ScheduledRun, error) {
	return s.db.ScheduledRunByID(ctx, id)
}

func (s *Scheduler) Runs(ctx context.Context, jobName string, limit int) ([]storage.ScheduledRun, error) {
	return s.db.ScheduledRuns(ctx, jobName, limit)
}

func (s *Scheduler) runScheduledJob(ctx context.Context, job *metadata.ScheduledJob, runID uuid.UUID, startedAt time.Time) {
	if job.Timeout <= 0 {
		s.executeJob(ctx, job, runID, startedAt)
		return
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
	defer cancel()
	s.executeJob(timeoutCtx, job, runID, startedAt)
}

// executeJob исполняет задание по уже заведённой записи прогона: id и время
// старта приходят снаружи (см. beginRun).
func (s *Scheduler) executeJob(ctx context.Context, job *metadata.ScheduledJob, runID uuid.UUID, startedAt time.Time) {
	var output, status, errText string
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr := fmt.Errorf("panic: %v", recovered)
			status, errText = scheduledRunStatus(ctx, runErr)
			s.log.Error("scheduler: job panic", "job", job.Name, "panic", recovered, "stack", string(debug.Stack()))
		}
		durationMs := time.Since(startedAt).Milliseconds()
		if s.finishActiveRun(runID) {
			s.updateRun(ctx, runID, status, output, errText, durationMs)
		}
		s.log.Info("scheduler: job finished", "job", job.Name, "status", status, "duration_ms", durationMs)
	}()

	var runErr error
	output, runErr = s.runProcessor(ctx, job)
	status, errText = scheduledRunStatus(ctx, runErr)
}

func (s *Scheduler) executeGoJob(ctx context.Context, name string, fn func(ctx context.Context) error, runID uuid.UUID, startedAt time.Time) {
	var status, output, errText string
	var runErr error
	var acceptedResult *AcceptedResult
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("panic: %v", recovered)
			acceptedResult = nil
			s.log.Error("scheduler: Go job panic", "job", name, "panic", recovered, "stack", string(debug.Stack()))
		}
		if acceptedResult != nil && runErr == nil {
			status = runStatusAccepted
			output = acceptedResult.Message
		} else {
			status, errText = scheduledRunStatus(ctx, runErr)
		}
		durationMs := time.Since(startedAt).Milliseconds()
		if s.finishActiveRun(runID) {
			s.updateRun(ctx, runID, status, output, errText, durationMs)
		}
		if runErr != nil {
			s.log.Error("scheduler: Go job failed", "job", name, "status", status, "err", runErr, "duration_ms", durationMs)
			return
		}
		s.log.Info("scheduler: Go job done", "job", name, "status", status, "duration_ms", durationMs)
	}()
	jobCtx := context.WithValue(ctx, runInfoContextKey{}, RunInfo{ID: runID, StartedAt: startedAt})
	runErr = fn(jobCtx)
	if accepted, ok := runErr.(*AcceptedResult); ok && accepted != nil {
		acceptedResult = accepted
		runErr = nil
	}
}

func scheduledRunStatus(ctx context.Context, runErr error) (status, errText string) {
	if ctx.Err() == context.DeadlineExceeded {
		return runStatusTimeout, "timeout exceeded"
	}
	if ctx.Err() == context.Canceled {
		if runErr != nil {
			return runStatusInterrupted, runErr.Error()
		}
		return runStatusInterrupted, "scheduler shutdown interrupted"
	}
	if runErr != nil {
		return runStatusError, runErr.Error()
	}
	return runStatusSuccess, ""
}

func (s *Scheduler) updateRun(ctx context.Context, runID uuid.UUID, status, output, errText string, durationMs int64) {
	if err := s.db.UpdateScheduledRun(ctx, runID, status, output, errText, durationMs); err != nil {
		// Use a bounded background context in case the job context was
		// cancelled; run finalization must not hang scheduler shutdown forever.
		bgCtx, cancel := context.WithTimeout(context.Background(), interruptUpdateTimeout)
		retryErr := s.db.UpdateScheduledRun(bgCtx, runID, status, output, errText, durationMs)
		cancel()
		if retryErr != nil {
			s.log.Error("scheduler: update run status failed", "run_id", runID.String(), "status", status, "err", retryErr)
		}
	}
}

func (s *Scheduler) runProcessor(ctx context.Context, job *metadata.ScheduledJob) (output string, runErr error) {
	proc := s.reg.GetProcessor(job.Processor)
	if proc == nil {
		return "", fmt.Errorf("processor not found: %s", job.Processor)
	}

	procDecl := s.reg.GetProcedure(proc.Name, "Выполнить")
	if procDecl == nil {
		return "", fmt.Errorf("procedure Выполнить not found in processor %s", proc.Name)
	}

	resolvedParams := resolveParamTemplates(job.Params)
	s.mu.Lock()
	msgSink := s.msgSink
	varsBuilder := s.varsBuilder
	s.mu.Unlock()

	var messages []string
	msgFunc := interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
		if len(args) > 0 {
			text := fmt.Sprintf("%v", args[0])
			messages = append(messages, text)
			if msgSink != nil {
				msgSink("", text)
			}
		}
		return nil, nil
	})

	paramValues := make(map[string]any)
	for _, p := range proc.Params {
		if v, ok := resolvedParams[p.Name]; ok {
			paramValues[p.Name] = v
		}
	}

	paramsThis := &interpreter.MapThis{M: paramValues}
	mc := runtime.NewMovementsCollector("scheduler", uuid.Nil)
	// Полное DSL-окружение (Справочники/Документы/вложения/транзакции) строит
	// внешний VarsBuilder (ui), если подключён; иначе — базовый набор Common.
	var dslVars map[string]any
	var txState *interpreter.TxState
	if varsBuilder != nil {
		dslVars, txState = varsBuilder(ctx, mc, &messages)
	} else {
		dslVars = s.buildDSLVars(ctx, mc)
	}
	defer interpreter.RollbackTxExecution(txState)
	if dslVars == nil {
		dslVars = make(map[string]any)
	}
	if documents, ok := dslVars["Документы"].(interface{ SetDSLMessageCollector(*[]string) }); ok {
		documents.SetDSLMessageCollector(&messages)
	}
	dslVars["Параметры"] = paramsThis
	dslVars["Сообщить"] = msgFunc
	dslVars["Message"] = msgFunc
	interpreter.InjectMaket(dslVars, proc.Layout)

	// Параметры задания связываем и с одноимёнными аргументами Выполнить:
	// объявленный параметр процедуры иначе затеняет инжектированный и приходит
	// пустым — молча (#706). Тот же вызов, что в UI и procrun.
	_, err := s.interp.Call(procDecl, paramsThis, interpreter.BindNamedArgs(procDecl, paramValues), dslVars)
	err = interpreter.FinishTxExecution(txState, err)
	output = strings.Join(messages, "\n")
	return output, err
}

func (s *Scheduler) buildDSLVars(ctx context.Context, mc *runtime.MovementsCollector) map[string]any {
	s.mu.Lock()
	schedulerMailer := s.mailer
	s.mu.Unlock()
	// Базовый набор переменных совпадает с тем, что UI handlers инжектируют
	// для обработчиков OnWrite/OnPost. Caller-specific переменные (Параметры,
	// Сообщить с привязкой к log задания) добавляются в runScheduledJob сверху.
	return dslvars.Common{
		Ctx:       ctx,
		Reg:       s.reg,
		Store:     s.db,
		Mailer:    schedulerMailer,
		Movements: mc,
		Interp:    s.interp, // hook-правило конфликта в ПланыОбмена.ЗагрузитьПакет
		// Предохранитель сети (план 62): регламентные задания тоже инициируют
		// HTTP/email из конфигурации — гейтим тем же флагом.
		NetGuard: func() error {
			if s.db.GetNetworkEnabled(ctx) {
				return nil
			}
			return ErrNetworkLocked
		},
		// Команды ОС (план 67): тем же флагом базы exec.enabled. nil-guard в
		// dslvars означал бы запрет, но регламентные задания — доверенный
		// серверный код, поэтому гейтим по настройке, как сеть.
		ExecGuard: func() error {
			if s.db.GetExecEnabled(ctx) {
				return nil
			}
			return errors.New("выполнение команд ОС отключено")
		},
	}.Build()
}

// resolveParamTemplates replaces template expressions like {{today}} with actual values.
func resolveParamTemplates(params map[string]any) map[string]any {
	return resolveParamTemplatesAt(params, time.Now())
}

// ResolveParamTemplates is the exported entry point used by other subsystems
// (widgets, ad-hoc query callers) that need the same {{today|...}} grammar
// as scheduled jobs.
func ResolveParamTemplates(params map[string]any) map[string]any {
	return resolveParamTemplatesAt(params, time.Now())
}

func resolveParamTemplatesAt(params map[string]any, now time.Time) map[string]any {
	if len(params) == 0 {
		return params
	}
	result := make(map[string]any, len(params))
	for k, v := range params {
		if s, ok := v.(string); ok {
			result[k] = resolveTemplate(s, now)
		} else {
			result[k] = v
		}
	}
	return result
}

func resolveTemplate(s string, now time.Time) any {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return s
	}
	expr := strings.TrimSpace(s[2 : len(s)-2])
	parts := strings.SplitN(expr, "|", 2)
	base := strings.TrimSpace(parts[0])

	var t time.Time
	switch base {
	case "now":
		t = now
	case "today":
		t = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	default:
		return s
	}

	if len(parts) == 1 {
		return t
	}

	transform := strings.TrimSpace(parts[1])
	tparts := strings.SplitN(transform, ":", 2)
	op := strings.TrimSpace(tparts[0])
	var n int
	if len(tparts) == 2 {
		// Неразбираемое значение оставляет 0 — прежний контракт: шаблон
		// вроде minus_days:abc трактуется как отсутствие сдвига.
		if v, err := strconv.Atoi(strings.TrimSpace(tparts[1])); err == nil {
			n = v
		}
	}

	switch op {
	case "minus_days":
		return t.AddDate(0, 0, -n)
	case "minus_hours":
		return t.Add(-time.Duration(n) * time.Hour)
	case "minus_months":
		return t.AddDate(0, -n, 0)
	case "start_of_month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "end_of_month":
		return time.Date(t.Year(), t.Month()+1, 0, 23, 59, 59, 0, t.Location())
	}
	return t
}
