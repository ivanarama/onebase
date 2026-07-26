package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
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
	ErrNetworkLocked = errors.New("сетевые возможности отключены предохранителем — включите «Разрешить сетевые операции» в конфигураторе")
	// ErrJobAlreadyRunning is returned when a cron tick or manual start tries
	// to overlap another execution of the same logical job.
	ErrJobAlreadyRunning = errors.New("scheduled job is already running")
	ErrSchedulerStopping = errors.New("scheduler is stopping")
)

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
	rootCtx    context.Context
	rootCancel context.CancelFunc
	wg         sync.WaitGroup
	activeRuns map[uuid.UUID]*activeRun
	activeJobs map[string]struct{}
}

const (
	defaultShutdownTimeout = 30 * time.Second
	interruptUpdateTimeout = 5 * time.Second

	runStatusSuccess     = "success"
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

// VarsBuilder строит DSL-окружение для запуска обработки задания.
type VarsBuilder func(ctx context.Context, mc *runtime.MovementsCollector) map[string]any

// SetVarsBuilder подключает внешний сборщик DSL-окружения (см. поле varsBuilder).
func (s *Scheduler) SetVarsBuilder(b VarsBuilder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.varsBuilder = b
}

func New(db *storage.DB, reg *runtime.Registry, interp *interpreter.Interpreter) *Scheduler {
	return &Scheduler{
		cron:       cronlib.New(),
		goJobs:     make(map[string]func(context.Context) error),
		db:         db,
		reg:        reg,
		interp:     interp,
		log:        oblog.Component("scheduler"),
		activeRuns: make(map[uuid.UUID]*activeRun),
		activeJobs: make(map[string]struct{}),
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
	if s.stopping {
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

func (s *Scheduler) replaceJobs(jobs []*metadata.ScheduledJob) error {
	nextCron, nextJobs, err := s.buildCron(jobs)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return ErrSchedulerStopping
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

func (s *Scheduler) buildCron(jobs []*metadata.ScheduledJob) (*cronlib.Cron, []*metadata.ScheduledJob, error) {
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
		if _, err := nextCron.AddFunc(job.Schedule, s.scheduledJobCallback(job)); err != nil {
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
		s.runScheduledJob(ctx, job)
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
		s.executeGoJob(ctx, name, fn)
	}
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

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		s.log.Warn("scheduler: start ignored while shutdown is in progress")
		return
	}
	s.ensureRootLocked()
	s.running = true
	cron := s.cron
	cron.Start()
	s.mu.Unlock()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("scheduler: shutdown timed out", "err", err)
	}
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
	if s.stopping {
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

func (s *Scheduler) RunNow(_ context.Context, jobName string) error {
	key := jobKey(jobName)
	s.mu.Lock()
	job := cloneScheduledJob(s.jobByKeyLocked(key))
	goJob := s.goJobs[key]
	s.mu.Unlock()
	if job == nil {
		return fmt.Errorf("job not found: %s", jobName)
	}
	// Use background context: request context will be cancelled after redirect
	jobCtx, done, err := s.beginJob(job.Name)
	if err != nil {
		return err
	}
	go func() {
		defer done()
		if goJob != nil {
			s.executeGoJob(jobCtx, job.Name, goJob)
			return
		}
		s.runScheduledJob(jobCtx, job)
	}()
	return nil
}

func (s *Scheduler) Runs(ctx context.Context, jobName string, limit int) ([]storage.ScheduledRun, error) {
	return s.db.ScheduledRuns(ctx, jobName, limit)
}

func (s *Scheduler) runScheduledJob(ctx context.Context, job *metadata.ScheduledJob) {
	if job.Timeout <= 0 {
		s.executeJob(ctx, job)
		return
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
	defer cancel()
	s.executeJob(timeoutCtx, job)
}

func (s *Scheduler) executeJob(ctx context.Context, job *metadata.ScheduledJob) {
	startedAt := time.Now()
	runID, err := s.db.InsertScheduledRun(ctx, job.Name, startedAt)
	if err != nil {
		s.log.Error("scheduler: insert run", "job", job.Name, "err", err)
		return
	}
	s.trackActiveRun(runID, job.Name, startedAt)

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

func (s *Scheduler) executeGoJob(ctx context.Context, name string, fn func(ctx context.Context) error) {
	startedAt := time.Now()
	runID, err := s.db.InsertScheduledRun(ctx, name, startedAt)
	if err != nil {
		s.log.Error("scheduler: insert go run", "job", name, "err", err)
		return
	}
	s.trackActiveRun(runID, name, startedAt)

	var status, errText string
	var runErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("panic: %v", recovered)
			s.log.Error("scheduler: Go job panic", "job", name, "panic", recovered, "stack", string(debug.Stack()))
		}
		status, errText = scheduledRunStatus(ctx, runErr)
		durationMs := time.Since(startedAt).Milliseconds()
		if s.finishActiveRun(runID) {
			s.updateRun(ctx, runID, status, "", errText, durationMs)
		}
		if runErr != nil {
			s.log.Error("scheduler: Go job failed", "job", name, "status", status, "err", runErr, "duration_ms", durationMs)
			return
		}
		s.log.Info("scheduler: Go job done", "job", name, "status", status, "duration_ms", durationMs)
	}()
	runErr = fn(ctx)
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
	if varsBuilder != nil {
		dslVars = varsBuilder(ctx, mc)
	} else {
		dslVars = s.buildDSLVars(ctx, mc)
	}
	if dslVars == nil {
		dslVars = make(map[string]any)
	}
	dslVars["Параметры"] = paramsThis
	dslVars["Сообщить"] = msgFunc
	dslVars["Message"] = msgFunc
	interpreter.InjectMaket(dslVars, proc.Layout)

	err := s.interp.Run(procDecl, paramsThis, dslVars)
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
		fmt.Sscanf(strings.TrimSpace(tparts[1]), "%d", &n)
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
