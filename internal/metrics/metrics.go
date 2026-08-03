// Package metrics реализует минимальный, без внешних зависимостей, сбор
// HTTP-метрик и их отдачу в текстовом формате Prometheus exposition.
//
// Зачем своё, а не github.com/prometheus/client_golang: go.mod проекта намеренно
// лёгкий, а нам нужно ровно три вещи — счётчик запросов, гистограмма латентности
// и несколько gauge для пула БД. Всё это умещается в пару сотен строк и не тянет
// десяток транзитивных зависимостей.
//
// Конкурентность (план 111, P2-2): раньше все карты метрик защищал один
// глобальный sync.Mutex, который брался эксклюзивно на КАЖДОМ HTTP-запросе —
// единая точка сериализации на горячем пути. Теперь каждая серия — отдельный
// объект с атомарными счётчиками (в духе prometheus/client_golang): горячий путь
// берёт лишь разделяемый RLock, чтобы найти серию в карте, а сам инкремент
// атомарный. Маршруты/операции низкой кардинальности, поэтому после прогрева
// новые серии не создаются и запись становится безлоковой. Эксклюзивный Lock
// нужен только на создание новой серии (double-check) и на регистрацию
// func-метрик при старте.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

// DefaultBuckets — границы гистограммы латентности HTTP-запросов в секундах.
// Подобраны под web-нагрузку: от 5 мс до 10 с.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type reqKey struct{ method, route, status string }
type routeKey struct{ method, route string }
type opKey struct{ kind, status string }
type limitedOpKey struct{ kind, reason string }
type funcMetric struct {
	name       string
	help       string
	metricType string
	value      func() float64
}

// histogram — потокобезопасная гистограмма с атомарными корзинами. Число
// наблюдений (count) НЕ хранится отдельно, а выводится как сумма корзин: так
// инвариант «корзина +Inf == count» держится без общего лока, даже когда
// наблюдения приходят конкурентно со скрейпом.
type histogram struct {
	// counts[i] — число наблюдений в корзине i (значение <= buckets[i]).
	// Последний элемент (индекс len(buckets)) — корзина +Inf.
	counts  []atomic.Uint64
	sumBits atomic.Uint64 // биты float64 суммы; обновляются CAS-петлёй
}

func newHistogram(nBuckets int) *histogram {
	return &histogram{counts: make([]atomic.Uint64, nBuckets+1)}
}

// observe атомарно учитывает наблюдение sec (в секундах). buckets неизменяемы
// после New, поэтому читаются без лока.
func (h *histogram) observe(buckets []float64, sec float64) {
	idx := sort.SearchFloat64s(buckets, sec) // первый bucket >= sec
	h.counts[idx].Add(1)
	for {
		old := h.sumBits.Load()
		if h.sumBits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+sec)) {
			return
		}
	}
}

func (h *histogram) sum() float64 { return math.Float64frombits(h.sumBits.Load()) }

// Registry хранит HTTP-метрики процесса. Потокобезопасен.
type Registry struct {
	mu                 sync.RWMutex // защищает структуру карт и funcMetrics, НЕ значения счётчиков
	buckets            []float64    // неизменяем после New — читается без лока
	requests           map[reqKey]*atomic.Uint64
	durations          map[routeKey]*histogram
	operations         map[opKey]*atomic.Uint64
	operationDurations map[string]*histogram
	activeOperations   map[string]*atomic.Int64
	slowOperations     map[string]*atomic.Uint64
	limitedOperations  map[limitedOpKey]*atomic.Uint64
	funcMetrics        []funcMetric
}

// New создаёт реестр с корзинами гистограммы по умолчанию.
func New() *Registry {
	return &Registry{
		buckets:            DefaultBuckets,
		requests:           make(map[reqKey]*atomic.Uint64),
		durations:          make(map[routeKey]*histogram),
		operations:         make(map[opKey]*atomic.Uint64),
		operationDurations: make(map[string]*histogram),
		activeOperations:   make(map[string]*atomic.Int64),
		slowOperations:     make(map[string]*atomic.Uint64),
		limitedOperations:  make(map[limitedOpKey]*atomic.Uint64),
	}
}

// getOrCreate возвращает указатель на серию по ключу, создавая её через mk при
// отсутствии. Быстрый путь — разделяемый RLock (несколько горутин параллельно) +
// атомарный инкремент вызывающим кодом. Медленный путь (создание) — эксклюзивный
// Lock с повторной проверкой, чтобы две горутины не завели дубликат.
func getOrCreate[K comparable, V any](reg *Registry, m map[K]*V, key K, mk func() *V) *V {
	reg.mu.RLock()
	p := m[key]
	reg.mu.RUnlock()
	if p != nil {
		return p
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if p = m[key]; p == nil {
		p = mk()
		m[key] = p
	}
	return p
}

func newCounter() *atomic.Uint64 { return new(atomic.Uint64) }
func newGauge() *atomic.Int64    { return new(atomic.Int64) }

// RegisterGaugeFunc registers a gauge whose value is sampled on scrape.
// Callbacks must not mutate this Registry and should use low-latency reads.
func (reg *Registry) RegisterGaugeFunc(name, help string, value func() float64) {
	reg.registerFuncMetric(name, help, "gauge", value)
}

// RegisterCounterFunc registers a monotonically increasing counter sampled on
// scrape. The callback is responsible for returning a cumulative value.
func (reg *Registry) RegisterCounterFunc(name, help string, value func() float64) {
	reg.registerFuncMetric(name, help, "counter", value)
}

func (reg *Registry) registerFuncMetric(name, help, metricType string, value func() float64) {
	if reg == nil || name == "" || help == "" || value == nil {
		return
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.funcMetrics = append(reg.funcMetrics, funcMetric{
		name:       name,
		help:       help,
		metricType: metricType,
		value:      value,
	})
}

func (reg *Registry) observe(method, route string, status int, d time.Duration) {
	getOrCreate(reg, reg.requests, reqKey{method, route, strconv.Itoa(status)}, newCounter).Add(1)
	getOrCreate(reg, reg.durations, routeKey{method, route}, reg.newHist).observe(reg.buckets, d.Seconds())
}

func (reg *Registry) newHist() *histogram { return newHistogram(len(reg.buckets)) }

// OperationStart increments active operation gauge for kind. kind must be a
// low-cardinality value such as "report.run" or "http_service.run".
func (reg *Registry) OperationStart(kind string) {
	if reg == nil || kind == "" {
		return
	}
	getOrCreate(reg, reg.activeOperations, kind, newGauge).Add(1)
}

// OperationFinish records duration/status and decrements the active operation
// gauge. status must be low-cardinality: ok/error/timeout/limited/canceled.
func (reg *Registry) OperationFinish(kind, status string, d time.Duration, slow bool) {
	if reg == nil || kind == "" {
		return
	}
	if status == "" {
		status = "unknown"
	}
	// Уменьшаем счётчик активных, не уходя ниже нуля (защита от непарного Finish).
	g := getOrCreate(reg, reg.activeOperations, kind, newGauge)
	for {
		v := g.Load()
		if v <= 0 || g.CompareAndSwap(v, v-1) {
			break
		}
	}
	getOrCreate(reg, reg.operations, opKey{kind, status}, newCounter).Add(1)
	getOrCreate(reg, reg.operationDurations, kind, reg.newHist).observe(reg.buckets, d.Seconds())
	if slow {
		getOrCreate(reg, reg.slowOperations, kind, newCounter).Add(1)
	}
}

// OperationLimited records a backpressure/limit hit without starting work.
func (reg *Registry) OperationLimited(kind, reason string) {
	if reg == nil || kind == "" {
		return
	}
	if reason == "" {
		reason = "unknown"
	}
	getOrCreate(reg, reg.limitedOperations, limitedOpKey{kind, reason}, newCounter).Add(1)
}

// statusRecorder перехватывает HTTP-код ответа для метрик. Своя обёртка, чтобы
// не зависеть от деталей chi middleware.WrapResponseWriter.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wroteHeader {
		sr.status = code
		sr.wroteHeader = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.wroteHeader {
		sr.status = http.StatusOK
		sr.wroteHeader = true
	}
	return sr.ResponseWriter.Write(b)
}

// Flush проксирует http.Flusher к обёрнутому writer'у. Без него обёртка
// «съедала» бы интерфейс Flusher (проверка w.(http.Flusher) в SSE-эндпоинтах
// возвращала бы false), и потоковые ответы падали бы с «стриминг не
// поддерживается сервером»: real-time-уведомления (/ui/events, план 74) и поток
// сканера ШК. Проявлялось только на базах, запущенных с ONEBASE_DEBUG_TOKEN
// (лаунчер/GUI включает этот middleware), а плоский `onebase run` — нет.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap открывает нижележащий writer для http.ResponseController (Go 1.20+),
// чтобы прочие возможности (Hijack, SetWriteDeadline и т.п.) оставались
// доступны сквозь обёртку.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// Middleware записывает по каждому запросу счётчик и латентность. Метку route
// берём из chi RoutePattern (доступен после маршрутизации) — это шаблон вида
// «/documents/{entity}/{id}/post», что держит кардинальность меток низкой
// (а не плодит серию на каждый id). Незаматченные пути группируются как "other".
func (reg *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "other"
		}
		reg.observe(r.Method, route, sr.status, time.Since(start))
	})
}

// kv — снимок одной серии карты: ключ + указатель на её (атомарное) значение.
// Указатели стабильны (серии не удаляются), поэтому значение можно безопасно
// прочитать уже после освобождения лока.
type kv[K comparable, V any] struct {
	key K
	val *V
}

func snapshotMap[K comparable, V any](m map[K]*V) []kv[K, V] {
	out := make([]kv[K, V], 0, len(m))
	for k, v := range m {
		out = append(out, kv[K, V]{k, v})
	}
	return out
}

// promWriter копит первую ошибку записи экспозиции.
//
// Обрыв здесь не безобиден: Prometheus разбирает то, что успело прийти, и
// частичный ответ выглядит для него как «эти серии исчезли». Дальше срабатывают
// алерты на пропавшие метрики либо, наоборот, молчат те, что считались по
// недошедшим сериям. Поэтому ошибка доходит до обработчика, а он решает, что с
// ней делать (заголовки уже отправлены — только журнал).
type promWriter struct {
	w   io.Writer
	err error
}

func (p *promWriter) printf(format string, a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, a...)
}

func (p *promWriter) println(a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintln(p.w, a...)
}

// WritePrometheus печатает накопленные метрики в формате Prometheus exposition.
// Под коротким RLock снимает снимок указателей на серии, затем пишет уже без
// лока (значения читаются атомарно) — скрейп не блокирует горячий путь.
func (reg *Registry) WritePrometheus(w io.Writer) error {
	pw := &promWriter{w: w}
	reg.mu.RLock()
	funcMetrics := append([]funcMetric(nil), reg.funcMetrics...)
	reqs := snapshotMap(reg.requests)
	durs := snapshotMap(reg.durations)
	ops := snapshotMap(reg.operations)
	opDurs := snapshotMap(reg.operationDurations)
	active := snapshotMap(reg.activeOperations)
	slow := snapshotMap(reg.slowOperations)
	limited := snapshotMap(reg.limitedOperations)
	reg.mu.RUnlock()

	// ── counter: onebase_http_requests_total ──────────────────────────────
	pw.println("# HELP onebase_http_requests_total Общее число обработанных HTTP-запросов.")
	pw.println("# TYPE onebase_http_requests_total counter")
	sort.Slice(reqs, func(i, j int) bool {
		a, b := reqs[i].key, reqs[j].key
		if a.route != b.route {
			return a.route < b.route
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.status < b.status
	})
	for _, e := range reqs {
		pw.printf("onebase_http_requests_total{method=%q,route=%q,status=%q} %d\n",
			e.key.method, e.key.route, e.key.status, e.val.Load())
	}

	// ── histogram: onebase_http_request_duration_seconds ──────────────────
	pw.println("# HELP onebase_http_request_duration_seconds Латентность HTTP-запросов в секундах.")
	pw.println("# TYPE onebase_http_request_duration_seconds histogram")
	sort.Slice(durs, func(i, j int) bool {
		a, b := durs[i].key, durs[j].key
		if a.route != b.route {
			return a.route < b.route
		}
		return a.method < b.method
	})
	for _, e := range durs {
		cum := writeHistogramBuckets(reg.buckets, e.val, func(le string, c uint64) {
			pw.printf("onebase_http_request_duration_seconds_bucket{method=%q,route=%q,le=%q} %d\n",
				e.key.method, e.key.route, le, c)
		})
		pw.printf("onebase_http_request_duration_seconds_sum{method=%q,route=%q} %s\n",
			e.key.method, e.key.route, strconv.FormatFloat(e.val.sum(), 'g', -1, 64))
		pw.printf("onebase_http_request_duration_seconds_count{method=%q,route=%q} %d\n",
			e.key.method, e.key.route, cum)
	}

	// ── operation counters/gauges: reports/export/processors/http services ──
	pw.println("# HELP onebase_operation_total Общее число тяжёлых runtime-операций.")
	pw.println("# TYPE onebase_operation_total counter")
	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i].key, ops[j].key
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.status < b.status
	})
	for _, e := range ops {
		pw.printf("onebase_operation_total{kind=%q,status=%q} %d\n", e.key.kind, e.key.status, e.val.Load())
	}

	pw.println("# HELP onebase_active_operations Активные тяжёлые runtime-операции.")
	pw.println("# TYPE onebase_active_operations gauge")
	sort.Slice(active, func(i, j int) bool { return active[i].key < active[j].key })
	for _, e := range active {
		pw.printf("onebase_active_operations{kind=%q} %d\n", e.key, e.val.Load())
	}

	pw.println("# HELP onebase_operation_duration_seconds Длительность тяжёлых runtime-операций в секундах.")
	pw.println("# TYPE onebase_operation_duration_seconds histogram")
	sort.Slice(opDurs, func(i, j int) bool { return opDurs[i].key < opDurs[j].key })
	for _, e := range opDurs {
		cum := writeHistogramBuckets(reg.buckets, e.val, func(le string, c uint64) {
			pw.printf("onebase_operation_duration_seconds_bucket{kind=%q,le=%q} %d\n", e.key, le, c)
		})
		pw.printf("onebase_operation_duration_seconds_sum{kind=%q} %s\n",
			e.key, strconv.FormatFloat(e.val.sum(), 'g', -1, 64))
		pw.printf("onebase_operation_duration_seconds_count{kind=%q} %d\n", e.key, cum)
	}

	pw.println("# HELP onebase_slow_operation_total Тяжёлые операции дольше slow_operation_ms.")
	pw.println("# TYPE onebase_slow_operation_total counter")
	sort.Slice(slow, func(i, j int) bool { return slow[i].key < slow[j].key })
	for _, e := range slow {
		pw.printf("onebase_slow_operation_total{kind=%q} %d\n", e.key, e.val.Load())
	}

	pw.println("# HELP onebase_limited_operation_total Операции, отклонённые лимитами/backpressure.")
	pw.println("# TYPE onebase_limited_operation_total counter")
	sort.Slice(limited, func(i, j int) bool {
		a, b := limited[i].key, limited[j].key
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.reason < b.reason
	})
	for _, e := range limited {
		pw.printf("onebase_limited_operation_total{kind=%q,reason=%q} %d\n",
			e.key.kind, e.key.reason, e.val.Load())
	}

	writeFuncMetrics(pw, funcMetrics)
	return pw.err
}

// writeHistogramBuckets печатает кумулятивные корзины гистограммы (включая +Inf)
// через emit(le, cumulative) и возвращает итоговое число наблюдений (== корзина
// +Inf). Значение count берётся отсюда же, поэтому «+Inf == count» соблюдается
// по построению даже при конкурентных наблюдениях.
func writeHistogramBuckets(buckets []float64, h *histogram, emit func(le string, cum uint64)) uint64 {
	var cum uint64
	for i, ub := range buckets {
		cum += h.counts[i].Load()
		emit(strconv.FormatFloat(ub, 'g', -1, 64), cum)
	}
	cum += h.counts[len(buckets)].Load() // +Inf
	emit("+Inf", cum)
	return cum
}

func writeFuncMetrics(pw *promWriter, metrics []funcMetric) {
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].name < metrics[j].name })
	for _, m := range metrics {
		pw.printf("# HELP %s %s\n", m.name, m.help)
		pw.printf("# TYPE %s %s\n", m.name, m.metricType)
		pw.printf("%s %s\n", m.name, strconv.FormatFloat(safeMetricValue(m.value), 'g', -1, 64))
	}
}

func safeMetricValue(value func() float64) (out float64) {
	defer func() {
		if recover() != nil {
			out = 0
		}
	}()
	return value()
}
