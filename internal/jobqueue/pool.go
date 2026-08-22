// Package jobqueue — очередь фоновых заданий с пулом исполнителей (план 130,
// issue #848).
//
// Зачем отдельная подсистема, а не доработка планировщика: «одно задание — один
// прогон» там не случайность, а защита cron-заданий от наложения на себя
// (`scheduler.beginJob` → `ErrJobAlreadyRunning`). Сценарий, ради которого всё
// затевалось, — обход ~360 узлов обмена круглосуточно, где в среднем 12
// одновременных сеансов, — требует ровно обратного: много исполнителей одного и
// того же задания с разными параметрами.
//
// Разделение обязанностей:
//   - durable-состояние очереди живёт в `storage._job_queue` (захват, аренда,
//     попытки, отмена);
//   - этот пакет владеет ЖИЗНЕННЫМ ЦИКЛОМ: диспетчер захватывает работу на
//     свободные слоты, исполнители её выполняют, heartbeat продлевает аренду и
//     разносит отмену, развёртка возвращает зависшее;
//   - что именно исполнять — знает Executor (им работает планировщик).
//
// Контракт доставки — at-least-once. Задача, взятая процессом, который умер
// жёстко, вернётся в очередь по истечении аренды и будет исполнена ещё раз.
// Обработчик обязан быть идемпотентным.
package jobqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

// Значения по умолчанию. Пул малый намеренно: очередь включена на любой базе,
// и «из коробки» она не должна съедать соединения у веб-нагрузки. Режим 24/7 на
// 16–32 исполнителя — это осознанная настройка в app.yaml.
const (
	DefaultWorkers      = 4
	DefaultPollInterval = 2 * time.Second
	DefaultLease        = 5 * time.Minute
	DefaultMaxAttempts  = 3
	DefaultRetention    = 14 * 24 * time.Hour
	DefaultRetryBackoff = 15 * time.Second

	// maxRetryBackoff ограничивает экспоненту: без потолка задача с большим
	// пределом попыток уезжала бы в отложенный запуск на часы.
	maxRetryBackoff = 10 * time.Minute
	// pruneInterval — как часто подрезается история выполненных задач.
	pruneInterval = time.Hour
	// finalizeTimeout — предел на запись результата задачи, когда основной
	// контекст уже отменён. Финализация не должна держать остановку сервера.
	finalizeTimeout = 5 * time.Second
	// DefaultDrainTimeout — сколько остановка ждёт уже взятые задачи, прежде
	// чем прервать их и вернуть в очередь.
	DefaultDrainTimeout = 30 * time.Second
)

// ErrQueueDisabled — очередь выключена конфигурацией (`queue: {workers: 0}`).
// Постановка при этом честно отказывает: тихо копить задачи, которые никто не
// возьмёт, — худший из возможных вариантов.
var ErrQueueDisabled = errors.New("очередь фоновых заданий выключена (queue.workers = 0)")

// Executor исполняет одну задачу: обработку регламентного задания с
// параметрами задачи. Реализуется планировщиком (`scheduler.ExecuteJobOnce`).
// Интерфейс узкий намеренно — очередь не должна знать ни про DSL, ни про
// метаданные.
type Executor interface {
	ExecuteJobOnce(ctx context.Context, jobName string, params map[string]any) (string, error)
}

// Config — настройки очереди (блок `queue:` в app.yaml).
//
// Workers = 0 означает «очередь выключена», а не «возьми значение по
// умолчанию»: иначе выключить её было бы нечем, кроме отрицательного числа.
// Точка отсчёта для вызывающих — DefaultConfig().
type Config struct {
	Workers      int
	PollInterval time.Duration
	Lease        time.Duration
	MaxAttempts  int
	Retention    time.Duration
	RetryBackoff time.Duration
	DrainTimeout time.Duration
}

// DefaultConfig — настройки очереди по умолчанию (нет блока `queue:` в
// app.yaml). Пул малый: очередь работает на любой базе и не должна «из коробки»
// отъедать соединения у веб-нагрузки.
func DefaultConfig() Config {
	return Config{
		Workers:      DefaultWorkers,
		PollInterval: DefaultPollInterval,
		Lease:        DefaultLease,
		MaxAttempts:  DefaultMaxAttempts,
		Retention:    DefaultRetention,
		RetryBackoff: DefaultRetryBackoff,
		DrainTimeout: DefaultDrainTimeout,
	}
}

func (c Config) withDefaults() Config {
	if c.Workers < 0 {
		c.Workers = 0
	}
	if c.PollInterval <= 0 {
		c.PollInterval = DefaultPollInterval
	}
	if c.Lease <= 0 {
		c.Lease = DefaultLease
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	if c.Retention < 0 {
		c.Retention = 0
	}
	if c.Retention == 0 {
		c.Retention = DefaultRetention
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = DefaultRetryBackoff
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = DefaultDrainTimeout
	}
	return c
}

// Pool — пул исполнителей очереди.
type Pool struct {
	db       *storage.DB
	exec     Executor
	cfg      Config
	log      *slog.Logger
	worker   string
	degraded bool // SQLite: параллелизм срезан до одного исполнителя

	nudge chan struct{}
	sem   chan struct{}
	wg    sync.WaitGroup

	mu         sync.Mutex
	rootCtx    context.Context
	rootCancel context.CancelFunc
	inflight   map[storage.JobTaskLease]*inflightTask
	stopping   bool
}

// inflightTask — задача, взятая ЭТИМ процессом.
type inflightTask struct {
	cancel      context.CancelFunc
	cancelled   bool // отмену запросил пользователь
	interrupted bool // прервана остановкой сервера
	leaseLost   bool // попытку уже изъял и заново захватил другой исполнитель
	lease       storage.JobTaskLease
}

// New собирает пул. Обращений к базе не делает: схему заводит Run (и
// EnsureServiceSchema на общих путях).
//
// На SQLite число исполнителей срезается до одного и об этом говорится вслух:
// пул там физически один коннект (`SetMaxOpenConns(1)`), и молча делать вид,
// что работает 16 исполнителей, значило бы врать в мониторе очереди.
func New(db *storage.DB, exec Executor, cfg Config) *Pool {
	cfg = cfg.withDefaults()
	log := oblog.Component("jobqueue")
	degraded := false
	if db != nil && db.Dialect() != nil && db.Dialect().Name() == "sqlite" && cfg.Workers > 1 {
		log.Warn("очередь: SQLite — параллелизм недоступен, исполнителей срезано до 1",
			"requested", cfg.Workers, "hint", "пул на 16–32 исполнителя требует PostgreSQL")
		cfg.Workers = 1
		degraded = true
	}
	p := &Pool{
		db:       db,
		exec:     exec,
		cfg:      cfg,
		log:      log,
		worker:   workerID(),
		degraded: degraded,
		nudge:    make(chan struct{}, 1),
		inflight: make(map[storage.JobTaskLease]*inflightTask),
	}
	if cfg.Workers > 0 {
		p.sem = make(chan struct{}, cfg.Workers)
	}
	return p
}

func workerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return host + "/" + strconv.Itoa(os.Getpid())
}

// Workers — сколько исполнителей реально работает (с учётом деградации).
func (p *Pool) Workers() int {
	if p == nil {
		return 0
	}
	return p.cfg.Workers
}

// Degraded сообщает, что параллелизм срезан бэкендом (SQLite).
func (p *Pool) Degraded() bool { return p != nil && p.degraded }

// Enabled — очередь принимает задачи.
func (p *Pool) Enabled() bool { return p != nil && p.cfg.Workers > 0 }

// InFlight — сколько задач исполняется этим процессом прямо сейчас.
func (p *Pool) InFlight() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inflight)
}

// Enqueue ставит задачу в очередь и возвращает её вместе с признаком «создана
// новая». created=false — ключ идемпотентности занят живой задачей, вернулась
// она.
//
// Постановка внутри транзакции вызывающего РАЗРЕШЕНА и осмысленна: строка
// ложится в ту же транзакцию и станет видимой исполнителю после коммита, а
// откат снимет задачу вместе с данными, ради которых она ставилась. Этим
// очередь отличается от `РегламентныеЗадания.Запустить`, которому нужно, чтобы
// задание увидело данные немедленно (план 123, п. 7).
func (p *Pool) Enqueue(ctx context.Context, jobName string, params map[string]any, key string) (storage.JobTask, bool, error) {
	if p == nil || p.db == nil {
		return storage.JobTask{}, false, errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	if !p.Enabled() {
		return storage.JobTask{}, false, ErrQueueDisabled
	}
	now := nowMs()
	task, created, err := p.db.EnqueueJobTask(ctx, storage.JobTask{
		JobName:     jobName,
		Params:      params,
		Key:         key,
		MaxAttempts: p.cfg.MaxAttempts,
		AvailableAt: now,
		CreatedAt:   now,
	})
	if err != nil {
		return storage.JobTask{}, false, err
	}
	if created {
		p.wake()
	}
	return task, created, nil
}

// Task читает задачу; nil — очередь такой не помнит.
func (p *Pool) Task(ctx context.Context, id uuid.UUID) (*storage.JobTask, error) {
	if p == nil || p.db == nil {
		return nil, errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	return p.db.JobTaskByID(ctx, id)
}

// Cancel просит отменить задачу: "cancelled" — снята до исполнения,
// "cancelling" — исполнителю выставлена пометка, "" — задача терминальна или
// неизвестна.
//
// Если задачу исполняет ЭТОТ процесс, отмена доходит немедленно, не дожидаясь
// heartbeat: ждать до половины арендного такта там, где контекст под рукой,
// незачем.
func (p *Pool) Cancel(ctx context.Context, id uuid.UUID) (string, error) {
	if p == nil || p.db == nil {
		return "", errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	state, err := p.db.RequestJobTaskCancel(ctx, id, nowMs(), "отменена пользователем")
	if err != nil {
		return "", err
	}
	if state == "cancelling" {
		p.cancelLocalByID(id)
	}
	return state, nil
}

// Replay возвращает задачу из карантина в очередь.
func (p *Pool) Replay(ctx context.Context, id uuid.UUID) error {
	if p == nil || p.db == nil {
		return errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	if err := p.db.ReplayJobTask(ctx, id, nowMs()); err != nil {
		return err
	}
	p.wake()
	return nil
}

// Stats — глубина очереди по статусам.
func (p *Pool) Stats(ctx context.Context) (map[string]int64, error) {
	if p == nil || p.db == nil {
		return nil, errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	return p.db.JobQueueStats(ctx)
}

// List — задачи для монитора очереди.
func (p *Pool) List(ctx context.Context, filter storage.JobTaskFilter) ([]storage.JobTask, error) {
	if p == nil || p.db == nil {
		return nil, errors.New("очередь фоновых заданий недоступна в этом режиме")
	}
	return p.db.ListJobTasks(ctx, filter)
}

// Run поднимает пул и блокируется до отмены ctx, после чего возвращает
// результат ограниченного по времени дренажа.
//
// Контекст исполнения задач НЕ выводится из ctx намеренно: отмена ctx означает
// «перестань брать новое и дай взятому добежать», а не «убей всё немедленно».
// Тот же приём, что у планировщика (`rootCtx` + Shutdown с дедлайном).
func (p *Pool) Run(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if !p.Enabled() {
		p.log.Info("очередь фоновых заданий выключена (queue.workers = 0)")
		<-ctx.Done()
		return nil
	}
	if err := p.db.EnsureJobQueueSchema(ctx); err != nil {
		return fmt.Errorf("очередь фоновых заданий: %w", err)
	}

	p.mu.Lock()
	p.stopping = false
	p.rootCtx, p.rootCancel = context.WithCancel(context.Background())
	p.mu.Unlock()

	p.log.Info("очередь фоновых заданий запущена",
		"workers", p.cfg.Workers, "poll", p.cfg.PollInterval, "lease", p.cfg.Lease,
		"max_attempts", p.cfg.MaxAttempts, "worker", p.worker)

	// Первым делом — развёртка. Возвращает она только задачи с УЖЕ ИСТЁКШЕЙ
	// арендой: после жёсткого завершения процесса его задачи ещё какое-то время
	// выглядят живыми, и трогать их нельзя — на той же базе может работать
	// другой инстанс, которому они принадлежат по праву. Практическое
	// следствие: после падения работа возобновляется в пределах lease_sec, и
	// это тот срок, которым управляет администратор.
	p.reclaim(ctx)
	p.prune(ctx)

	// Служебные циклы живут на СВОЁМ контексте и переживают отмену ctx: аренду
	// взятых задач надо продлевать и во время дренажа. Иначе при коротком
	// lease_sec задача, которую этот процесс честно доводит до конца, успела бы
	// протухнуть, и её подобрал бы сосед — то есть штатная остановка сервера
	// сама создавала бы двойное исполнение.
	bgCtx, bgCancel := context.WithCancel(context.Background())
	var loops sync.WaitGroup
	loops.Add(2)
	go func() { defer loops.Done(); p.heartbeatLoop(bgCtx) }()
	go func() { defer loops.Done(); p.maintenanceLoop(bgCtx) }()

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		p.dispatch(ctx)
		select {
		case <-ctx.Done():
			err := p.drain()
			bgCancel()
			loops.Wait()
			return err
		case <-ticker.C:
		case <-p.nudge:
		}
	}
}

// dispatch захватывает работу на свободные слоты и раздаёт её исполнителям.
func (p *Pool) dispatch(ctx context.Context) {
	free := p.freeSlots()
	if free <= 0 || p.isStopping() {
		return
	}
	now := time.Now()
	tasks, err := p.db.ClaimJobTasks(ctx, p.worker, free, now.UnixMilli(), now.Add(p.cfg.Lease).UnixMilli())
	if err != nil {
		if ctx.Err() == nil {
			p.log.Warn("очередь: захват задач не удался", "err", err)
		}
		return
	}
	for _, task := range tasks {
		p.start(task)
	}
}

func (p *Pool) freeSlots() int {
	if p.sem == nil {
		return 0
	}
	return cap(p.sem) - len(p.sem)
}

// start запускает исполнителя. Слот занимается ДО горутины: иначе диспетчер
// успел бы захватить следующую пачку, не увидев, что места уже нет.
func (p *Pool) start(task storage.JobTask) {
	select {
	case p.sem <- struct{}{}:
	default:
		// Слотов не осталось — вернуть задачу в очередь, а не держать в running.
		p.requeueUnstarted(task, "исполнитель освободился раньше, чем задача была принята")
		return
	}
	p.mu.Lock()
	if p.stopping || p.rootCtx == nil {
		p.mu.Unlock()
		<-p.sem
		p.requeueUnstarted(task, "сервер остановлен до начала исполнения")
		return
	}
	taskCtx, cancel := context.WithCancel(p.rootCtx)
	lease := task.Lease()
	p.inflight[lease] = &inflightTask{cancel: cancel, lease: lease}
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		defer func() {
			cancel()
			<-p.sem
			p.wake() // освободился слот — дать диспетчеру шанс сразу взять ещё
		}()
		p.execute(taskCtx, task)
	}()
}

// execute исполняет задачу и записывает её судьбу.
func (p *Pool) execute(ctx context.Context, task storage.JobTask) {
	started := time.Now()
	output, err := p.exec.ExecuteJobOnce(ctx, task.JobName, task.Params)
	state := p.finishInflight(task.Lease())
	duration := time.Since(started)

	switch {
	case state.leaseLost:
		// Heartbeat proved that this execution no longer owns the row. It may
		// finish local cleanup, but any durable write would corrupt the newer
		// attempt that reclaimed the task.
		p.log.Warn("очередь: результат устаревшей попытки отброшен",
			"job", task.JobName, "task", task.ID.String(), "attempt", task.Attempts)
	case state.cancelled:
		if p.finalize(task, storage.JobTaskCancelled, output, "отменена пользователем") == storage.JobTaskCancelled {
			p.log.Info("очередь: задача отменена", "job", task.JobName, "task", task.ID.String())
		}
	case state.interrupted:
		// Прервана остановкой сервера: попытка потрачена не по вине задачи,
		// поэтому она возвращается в очередь немедленно (или уходит в карантин,
		// если это была последняя попытка).
		p.retryOrQuarantine(task, output, errors.New("прервана остановкой сервера"), 0)
	case err != nil:
		p.retryOrQuarantine(task, output, err, p.backoff(task.Attempts))
	default:
		switch p.finalize(task, storage.JobTaskDone, output, "") {
		case storage.JobTaskDone:
			p.log.Info("очередь: задача выполнена", "job", task.JobName, "task", task.ID.String(),
				"duration_ms", duration.Milliseconds(), "attempt", task.Attempts)
		case storage.JobTaskCancelled:
			p.log.Info("очередь: задача отменена до записи успешного результата",
				"job", task.JobName, "task", task.ID.String())
		}
	}
}

func (p *Pool) retryOrQuarantine(task storage.JobTask, output string, runErr error, delay time.Duration) {
	if task.Attempts < task.MaxAttempts {
		ctx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
		defer cancel()
		now := time.Now()
		actual, err := p.db.RetryJobTask(ctx, task.Lease(), now.Add(delay).UnixMilli(), now.UnixMilli(), runErr.Error())
		if err != nil {
			if errors.Is(err, storage.ErrJobLeaseLost) {
				p.log.Warn("очередь: повтор устаревшей попытки отброшен",
					"task", task.ID.String(), "attempt", task.Attempts)
				return
			}
			p.log.Error("очередь: не удалось вернуть задачу в очередь", "task", task.ID.String(), "err", err)
			return
		}
		if actual == storage.JobTaskCancelled {
			p.log.Info("очередь: отмена выиграла гонку с повтором",
				"job", task.JobName, "task", task.ID.String(), "attempt", task.Attempts)
			return
		}
		p.log.Warn("очередь: задача упала, будет повтор", "job", task.JobName, "task", task.ID.String(),
			"attempt", task.Attempts, "max_attempts", task.MaxAttempts, "retry_in", delay, "err", runErr)
		if delay == 0 {
			p.wake()
		}
		return
	}
	switch p.finalize(task, storage.JobTaskDead, output, runErr.Error()) {
	case storage.JobTaskDead:
		p.log.Error("очередь: задача в карантине — попытки исчерпаны", "job", task.JobName,
			"task", task.ID.String(), "attempts", task.Attempts, "err", runErr)
	case storage.JobTaskCancelled:
		p.log.Info("очередь: отмена выиграла гонку с карантином",
			"job", task.JobName, "task", task.ID.String(), "attempt", task.Attempts)
	}
}

// finalize пишет терминальный статус на отдельном ограниченном контексте:
// контекст задачи к этому моменту может быть уже отменён (таймаут, остановка),
// а результат обязан дойти до базы — иначе задача останется running до
// истечения аренды и будет исполнена повторно без причины.
func (p *Pool) finalize(task storage.JobTask, status, output, errText string) string {
	ctx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
	defer cancel()
	actual, err := p.db.FinishJobTask(ctx, task.Lease(), status, output, errText, nowMs())
	if err != nil {
		if errors.Is(err, storage.ErrJobLeaseLost) {
			p.log.Warn("очередь: результат устаревшей попытки отброшен",
				"task", task.ID.String(), "attempt", task.Attempts, "status", status)
			return ""
		}
		p.log.Error("очередь: не удалось записать результат задачи",
			"task", task.ID.String(), "status", status, "err", err)
		return ""
	}
	return actual
}

// requeueUnstarted возвращает в очередь задачу, которую захватили, но так и не
// начали исполнять.
//
// Это страховка, а не рабочий путь: слоты занимает и захват, и запуск в одной
// горутине диспетчера, поэтому «захватили больше, чем можем начать» получиться
// не должно. Если такое всё же случилось, задача обязана вернуться в очередь, а
// не остаться running до истечения аренды. Потраченную захватом попытку вернуть
// нечем — она честно засчитана.
func (p *Pool) requeueUnstarted(task storage.JobTask, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), finalizeTimeout)
	defer cancel()
	now := nowMs()
	if _, err := p.db.RetryJobTask(ctx, task.Lease(), now, now, reason); err != nil {
		p.log.Error("очередь: не удалось вернуть незапущенную задачу", "task", task.ID.String(), "err", err)
	}
}

// backoff — экспоненциальная отсрочка повтора с потолком.
func (p *Pool) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.cfg.RetryBackoff
	for i := 1; i < attempt && delay < maxRetryBackoff; i++ {
		delay *= 2
	}
	if delay > maxRetryBackoff {
		delay = maxRetryBackoff
	}
	return delay
}

// heartbeatLoop продлевает аренду взятых задач и разносит запрошенные отмены.
func (p *Pool) heartbeatLoop(ctx context.Context) {
	interval := p.cfg.Lease / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.heartbeat(ctx)
		}
	}
}

func (p *Pool) heartbeat(ctx context.Context) {
	leases := p.inflightLeases()
	if len(leases) == 0 {
		return
	}
	cancelled, lost, err := p.db.RenewJobLeases(ctx, leases, time.Now().Add(p.cfg.Lease).UnixMilli())
	if err != nil {
		if ctx.Err() == nil {
			p.log.Warn("очередь: не удалось продлить аренду задач", "count", len(leases), "err", err)
		}
		return
	}
	for _, lease := range cancelled {
		p.cancelLocal(lease)
	}
	for _, lease := range lost {
		p.loseLocalLease(lease)
	}
}

// maintenanceLoop изымает зависшее и подрезает историю.
func (p *Pool) maintenanceLoop(ctx context.Context) {
	reclaimEvery := p.cfg.Lease / 2
	if reclaimEvery < time.Second {
		reclaimEvery = time.Second
	}
	reclaimTicker := time.NewTicker(reclaimEvery)
	defer reclaimTicker.Stop()
	pruneTicker := time.NewTicker(pruneInterval)
	defer pruneTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-reclaimTicker.C:
			p.reclaim(ctx)
		case <-pruneTicker.C:
			p.prune(ctx)
		}
	}
}

func (p *Pool) reclaim(ctx context.Context) {
	requeued, dead, err := p.db.ReclaimExpiredJobTasks(ctx, nowMs(),
		"аренда исполнителя истекла: процесс завершился, не дописав результат")
	if err != nil {
		if ctx.Err() == nil {
			p.log.Warn("очередь: изъятие зависших задач не удалось", "err", err)
		}
		return
	}
	if requeued > 0 || dead > 0 {
		p.log.Warn("очередь: изъяты задачи с истёкшей арендой", "requeued", requeued, "quarantined", dead)
		if requeued > 0 {
			p.wake()
		}
	}
}

func (p *Pool) prune(ctx context.Context) {
	removed, err := p.db.PruneJobTasks(ctx, nowMs(), p.cfg.Retention.Milliseconds())
	if err != nil {
		if ctx.Err() == nil {
			p.log.Warn("очередь: уборка истории задач не удалась", "err", err)
		}
		return
	}
	if removed > 0 {
		p.log.Info("очередь: история задач подрезана", "removed", removed, "older_than", p.cfg.Retention)
	}
}

// drain ждёт взятые задачи в пределах DrainTimeout. Не успевшие прерываются, а
// их строки возвращаются в очередь исполнителями — оставлять их running значило
// бы ждать истечения аренды на ровном месте.
func (p *Pool) drain() error {
	p.mu.Lock()
	p.stopping = true
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.shutdownRoot()
		p.log.Info("очередь фоновых заданий остановлена: все взятые задачи добежали")
		return nil
	case <-time.After(p.cfg.DrainTimeout):
		count := p.interruptInflight()
		p.log.Warn("очередь: дренаж не уложился в срок — взятые задачи прерваны и возвращены в очередь",
			"tasks", count, "timeout", p.cfg.DrainTimeout)
		// Дальше НЕ ждём. Задача может игнорировать отмену контекста —
		// например, счётный цикл в DSL, который не обращается ни к базе, ни к
		// сети, — и такое ожидание держало бы остановку сервера вечно. Строку
		// вернёт в очередь либо сам исполнитель, когда всё-таки закончит, либо
		// развёртка по истечении аренды.
		go func() {
			<-done
			p.shutdownRoot()
		}()
		return fmt.Errorf("очередь фоновых заданий: дренаж не уложился в %s", p.cfg.DrainTimeout)
	}
}

func (p *Pool) shutdownRoot() {
	p.mu.Lock()
	cancel := p.rootCancel
	p.rootCtx, p.rootCancel = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// interruptInflight помечает взятые задачи прерванными и отменяет их контексты.
func (p *Pool) interruptInflight() int {
	p.mu.Lock()
	tasks := make([]*inflightTask, 0, len(p.inflight))
	for _, task := range p.inflight {
		task.interrupted = true
		tasks = append(tasks, task)
	}
	p.mu.Unlock()
	for _, task := range tasks {
		task.cancel()
	}
	return len(tasks)
}

func (p *Pool) cancelLocal(lease storage.JobTaskLease) {
	p.mu.Lock()
	task := p.inflight[lease]
	if task != nil {
		task.cancelled = true
	}
	p.mu.Unlock()
	if task != nil {
		task.cancel()
	}
}

func (p *Pool) cancelLocalByID(id uuid.UUID) {
	p.mu.Lock()
	tasks := make([]*inflightTask, 0, 1)
	for lease, task := range p.inflight {
		if lease.ID == id {
			task.cancelled = true
			tasks = append(tasks, task)
		}
	}
	p.mu.Unlock()
	for _, task := range tasks {
		task.cancel()
	}
}

func (p *Pool) loseLocalLease(lease storage.JobTaskLease) {
	p.mu.Lock()
	task := p.inflight[lease]
	if task != nil {
		task.leaseLost = true
	}
	p.mu.Unlock()
	if task != nil {
		task.cancel()
	}
}

// finishInflight снимает задачу с учёта и отдаёт её финальное состояние.
func (p *Pool) finishInflight(lease storage.JobTaskLease) inflightTask {
	p.mu.Lock()
	defer p.mu.Unlock()
	task := p.inflight[lease]
	delete(p.inflight, lease)
	if task == nil {
		return inflightTask{}
	}
	return *task
}

func (p *Pool) inflightLeases() []storage.JobTaskLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	leases := make([]storage.JobTaskLease, 0, len(p.inflight))
	for lease := range p.inflight {
		leases = append(leases, lease)
	}
	return leases
}

func (p *Pool) isStopping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopping
}

// wake будит диспетчер, не блокируясь: канал буферизован на единицу, и
// потерянный сигнал означает лишь «диспетчер и так вот-вот проснётся».
func (p *Pool) wake() {
	if p == nil || p.nudge == nil {
		return
	}
	select {
	case p.nudge <- struct{}{}:
	default:
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }
