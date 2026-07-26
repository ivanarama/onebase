// Package webhook — исходящие веб-хуки на события платформы (план 29):
// «документ проведён → POST на URL». Конфигурируются декларативно в
// config/app.yaml (блок webhooks), отправляются асинхронно, с retry и
// журналом _webhook_log. Превращает OneBase в источник событий для
// n8n/Make/Telegram-ботов без единой строки кода.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
)

// Config — один веб-хук из app.yaml.
type Config struct {
	Name    string            `yaml:"name"`
	On      string            `yaml:"on"`     // document.save|post|unpost|delete, catalog.save|delete
	Filter  map[string]string `yaml:"filter"` // entity: ИмяСущности (пусто = все)
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"` // по умолчанию POST
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`    // шаблон: {{id}} {{entity}} {{user}} {{timestamp}} {{Поле}}
	Timeout int               `yaml:"timeout"` // секунд, по умолчанию 10
	Retry   int               `yaml:"retry"`   // повторов при ошибке, по умолчанию 0
}

// Event — событие платформы.
type Event struct {
	Name   string // document.post, catalog.save, ...
	Entity string
	ID     string
	User   string
	Record map[string]any // поля записи для шаблона тела
}

// LogEntry — запись журнала вызова веб-хука (пишется в _webhook_log).
type LogEntry struct {
	Webhook    string
	Event      string
	Entity     string
	RecordID   string
	URL        string
	StatusCode int
	Error      string
	Duration   time.Duration
	Attempts   int
}

// Metrics is a low-cardinality snapshot for Prometheus scraping.
type Metrics struct {
	Inflight   int64
	Dispatched uint64
	Retries    uint64
	Failed     uint64
	Dropped    uint64
}

const (
	DefaultWorkers   = 4
	DefaultQueueSize = 256
)

// Options controls the bounded delivery pool. Zero values use safe defaults.
type Options struct {
	Workers   int
	QueueSize int
}

type delivery struct {
	hook  Config
	event Event
}

var errInvalidWebhookURL = errors.New("некорректный URL веб-хука")

// Dispatcher проверяет фильтры и отправляет HTTP-запросы через ограниченную
// очередь worker-ов. Close must be called during application shutdown.
type Dispatcher struct {
	hooks      []Config
	client     *http.Client
	logFn      func(LogEntry) // best-effort журнал; может быть nil
	guardMu    sync.RWMutex
	guard      func() bool   // предохранитель сети (план 62): true = сеть разрешена; nil = без ограничений
	retryBase  time.Duration // база экспоненциальной задержки (тесты ускоряют)
	ctx        context.Context
	cancel     context.CancelFunc
	queue      chan delivery
	gate       sync.RWMutex
	closed     bool
	pending    sync.WaitGroup
	workers    sync.WaitGroup
	closeOnce  sync.Once
	closeDone  chan struct{}
	inflight   atomic.Int64
	dispatched atomic.Uint64
	retries    atomic.Uint64
	failed     atomic.Uint64
	dropped    atomic.Uint64
}

// New строит диспетчер. logFn вызывается после завершения каждого вызова
// (включая неудачные) — обычно это запись в _webhook_log.
func New(hooks []Config, logFn func(LogEntry)) *Dispatcher {
	return NewWithOptions(hooks, logFn, Options{})
}

// NewWithOptions builds a dispatcher with an explicit bounded worker pool.
func NewWithOptions(hooks []Config, logFn func(LogEntry), opts Options) *Dispatcher {
	if opts.Workers <= 0 {
		opts.Workers = DefaultWorkers
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = DefaultQueueSize
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		hooks:     append([]Config(nil), hooks...),
		client:    &http.Client{},
		logFn:     logFn,
		retryBase: time.Second,
		ctx:       ctx,
		cancel:    cancel,
		queue:     make(chan delivery, opts.QueueSize),
		closeDone: make(chan struct{}),
	}
	if len(hooks) > 0 {
		d.workers.Add(opts.Workers)
		for i := 0; i < opts.Workers; i++ {
			go d.worker()
		}
	}
	return d
}

// SetGuard задаёт предохранитель сети (план 62): когда guard() == false,
// исходящие веб-хуки не отправляются, а в журнал пишется запись со статусом
// «заблокировано» — отказ виден, а не молчалив.
func (d *Dispatcher) SetGuard(guard func() bool) {
	d.guardMu.Lock()
	d.guard = guard
	d.guardMu.Unlock()
}

// Enabled сообщает, настроен ли хотя бы один веб-хук.
func (d *Dispatcher) Enabled() bool { return d != nil && len(d.hooks) > 0 }

// Metrics returns a snapshot of dispatcher activity.
func (d *Dispatcher) Metrics() Metrics {
	if d == nil {
		return Metrics{}
	}
	return Metrics{
		Inflight:   d.inflight.Load(),
		Dispatched: d.dispatched.Load(),
		Retries:    d.retries.Load(),
		Failed:     d.failed.Load(),
		Dropped:    d.dropped.Load(),
	}
}

// Dispatch ставит подходящие веб-хуки в bounded queue и не блокирует
// сохранение документа. При переполнении доставка явно журналируется как
// неудачная вместо неограниченного создания goroutine.
func (d *Dispatcher) Dispatch(e Event) {
	if d == nil {
		return
	}
	for i := range d.hooks {
		h := d.hooks[i]
		if h.On != e.Name {
			continue
		}
		if want := h.Filter["entity"]; want != "" && !strings.EqualFold(want, e.Entity) {
			continue
		}
		d.dispatched.Add(1)
		d.enqueue(delivery{hook: h, event: e})
	}
}

func (d *Dispatcher) enqueue(item delivery) {
	d.gate.RLock()
	if d.closed {
		d.gate.RUnlock()
		d.reject(item, "диспетчер веб-хуков уже остановлен")
		return
	}
	d.pending.Add(1)
	d.inflight.Add(1)
	select {
	case d.queue <- item:
		d.gate.RUnlock()
	default:
		d.inflight.Add(-1)
		d.pending.Done()
		d.gate.RUnlock()
		d.reject(item, "очередь веб-хуков переполнена")
	}
}

func (d *Dispatcher) worker() {
	defer d.workers.Done()
	for {
		select {
		case item := <-d.queue:
			d.handle(item)
		case <-d.ctx.Done():
			d.drainCanceled()
			return
		}
	}
}

func (d *Dispatcher) handle(item delivery) {
	defer d.pending.Done()
	defer d.inflight.Add(-1)
	if d.ctx.Err() != nil {
		d.reject(item, "доставка отменена при завершении работы")
		return
	}
	d.fire(d.ctx, &item.hook, item.event)
}

func (d *Dispatcher) drainCanceled() {
	for {
		select {
		case item := <-d.queue:
			d.handle(item)
		default:
			return
		}
	}
}

func (d *Dispatcher) reject(item delivery, reason string) {
	d.failed.Add(1)
	d.dropped.Add(1)
	entry := d.logEntry(item.hook, item.event)
	entry.Error = reason
	d.log(entry, time.Now())
}

// Wait дожидается всех уже поставленных доставок. Worker-ы остаются готовы
// принимать следующие события; для завершения приложения используйте Close.
func (d *Dispatcher) Wait() {
	_ = d.WaitContext(context.Background())
}

func (d *Dispatcher) WaitContext(ctx context.Context) error {
	if d == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		d.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting events, drains the queue, and terminates workers. If
// ctx expires, active HTTP requests/backoff timers are cancelled and queued
// deliveries are journalled as cancelled.
func (d *Dispatcher) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	select {
	case <-d.closeDone:
		return nil
	default:
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.closeOnce.Do(func() {
		d.gate.Lock()
		d.closed = true
		d.gate.Unlock()
		go func() {
			d.pending.Wait()
			d.cancel()
			d.workers.Wait()
			close(d.closeDone)
		}()
	})
	select {
	case <-d.closeDone:
		return nil
	case <-ctx.Done():
		d.cancel()
		<-d.closeDone
		return ctx.Err()
	}
}

// fire выполняет один веб-хук: шаблон тела → HTTP-запрос → retry → журнал.
func (d *Dispatcher) fire(ctx context.Context, h *Config, e Event) {
	start := time.Now()
	entry := d.logEntry(*h, e)

	// Предохранитель сети (план 62): не отправляем, но фиксируем отказ в журнале.
	if !d.networkAllowed() {
		entry.Error = "заблокировано предохранителем сети (net.enabled выкл.)"
		entry.Attempts = 0
		d.failed.Add(1)
		d.log(entry, start)
		return
	}

	body, err := renderBody(h.Body, e)
	if err != nil {
		entry.Error = "шаблон тела: " + err.Error()
		d.failed.Add(1)
		d.log(entry, start)
		return
	}

	method := h.Method
	if method == "" {
		method = http.MethodPost
	}
	timeout := time.Duration(h.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// Число повторов ограничено сверху и снизу: без верхнего клампа большое retry
	// из app.yaml при сдвиге d.retryBase << (try-1) переполняло бы Duration в
	// отрицательную (Sleep=0, долбёж чужого сервера), а средние значения давали
	// сон на сутки. Без нижнего клампа отрицательный retry давал attempts=0 —
	// веб-хук не отправлялся бы ни разу.
	const maxRetry = 10
	const maxBackoff = 5 * time.Minute
	retry := h.Retry
	if retry < 0 {
		retry = 0
	}
	if retry > maxRetry {
		retry = maxRetry
	}
	attempts := retry + 1
	for try := 0; try < attempts; try++ {
		if try > 0 {
			// экспоненциальная задержка с потолком: base, 2*base, 4*base, …, maxBackoff.
			delay := d.retryBase << (try - 1)
			if delay <= 0 || delay > maxBackoff {
				delay = maxBackoff
			}
			if !waitContext(ctx, delay) {
				entry.Error = "доставка отменена при завершении работы"
				break
			}
			d.retries.Add(1)
		}
		entry.Attempts = try + 1
		code, err := d.send(ctx, method, h, body, timeout)
		entry.StatusCode = code
		if err != nil {
			entry.Error = safeDeliveryError(err)
			continue
		}
		if code >= 200 && code < 300 {
			entry.Error = ""
			break
		}
		entry.Error = fmt.Sprintf("HTTP %d", code)
	}
	if entry.Error != "" {
		d.failed.Add(1)
	}
	d.log(entry, start)
}

func (d *Dispatcher) send(parent context.Context, method string, h *Config, body string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.URL, strings.NewReader(body))
	if err != nil {
		return 0, errInvalidWebhookURL
	}
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // дренируем для keep-alive
	return resp.StatusCode, nil
}

func (d *Dispatcher) networkAllowed() bool {
	d.guardMu.RLock()
	guard := d.guard
	d.guardMu.RUnlock()
	return guard == nil || guard()
}

func (d *Dispatcher) logEntry(h Config, e Event) LogEntry {
	return LogEntry{
		Webhook: h.Name, Event: e.Name, Entity: e.Entity, RecordID: e.ID,
		URL: RedactURL(h.URL),
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func safeDeliveryError(err error) string {
	switch {
	case errors.Is(err, errInvalidWebhookURL):
		return errInvalidWebhookURL.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "таймаут HTTP-запроса"
	case errors.Is(err, context.Canceled):
		return "HTTP-запрос отменён"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "ошибка HTTP-запроса к " + RedactURL(urlErr.URL)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "сетевая ошибка HTTP-запроса"
	}
	return "ошибка HTTP-запроса"
}

// RedactURL returns a diagnostic URL that cannot expose credentials expanded
// into userinfo, path, or query parameters. The webhook name remains available
// in the log for identifying the configured endpoint.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<redacted>"
	}
	out := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" {
		out += "/<redacted>"
	} else if u.Path == "/" {
		out += "/"
	}
	if u.RawQuery != "" || u.ForceQuery {
		out += "?<redacted>"
	}
	return out
}

func (d *Dispatcher) log(entry LogEntry, start time.Time) {
	entry.Duration = time.Since(start)
	if d.logFn != nil {
		d.logFn(entry)
	}
}

// identRe — подстановки вида {{Поле}} (без точки, как в app.yaml плана 29).
// Преобразуются в {{index . "Поле"}} перед text/template.
var identRe = regexp.MustCompile(`\{\{\s*([\p{L}_][\p{L}\p{N}_]*)\s*\}\}`)

// renderBody подставляет переменные события в шаблон тела. Строковые значения
// экранируются по правилам JSON-строки (без обрамляющих кавычек) — кавычки и
// переводы строк в данных не ломают JSON-тело; числа/даты подставляются как есть.
func renderBody(tpl string, e Event) (string, error) {
	if tpl == "" {
		return "", nil
	}
	// Базовые поля тоже экранируем: user — это логин (может содержать кавычку/
	// перевод строки), entity задаётся конфигурацией, id — UUID. Без экранирования
	// спецсимвол в логине ломал бы JSON-тело или инъецировал поля в payload.
	data := map[string]any{
		"id":        jsonEscape(e.ID),
		"entity":    jsonEscape(e.Entity),
		"user":      jsonEscape(e.User),
		"timestamp": time.Now().Format(time.RFC3339),
	}
	for k, v := range e.Record {
		if s, ok := v.(string); ok {
			data[k] = jsonEscape(s)
		} else {
			data[k] = v
		}
	}
	t, err := template.New("body").Option("missingkey=zero").Parse(
		identRe.ReplaceAllString(tpl, `{{index . "$1"}}`))
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return "", err
	}
	// missingkey=zero для отсутствующих ключей даёт "<no value>" — заменяем на пусто
	return strings.ReplaceAll(sb.String(), "<no value>", ""), nil
}

// jsonEscape экранирует строку для вставки внутрь JSON-строки (без кавычек).
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
