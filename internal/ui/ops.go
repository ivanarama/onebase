package ui

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivantit66/onebase/internal/auth"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

const (
	opReportRun      = "report.run"
	opReportExport   = "report.export"
	opProcessorRun   = "processor.run"
	opHTTPServiceRun = "http_service.run"
	// opEntitySave — запись/проведение объекта вместе с прикладными хуками, и
	// opFormEvent — обработчик события управляемой формы. Оба входа исполняют
	// DSL по HTTP-запросу и до #865 шли без предела времени вовсе.
	opEntitySave = "entity.save"
	opFormEvent  = "form.event"
)

// RuntimeLimits contains optional guardrails for heavy runtime operations.
// Zero values mean disabled to keep existing apps compatible.
type RuntimeLimits struct {
	RequestTimeoutSec      int
	ReportTimeoutSec       int
	ReportMaxRows          int
	ReportConcurrency      int
	ExportTimeoutSec       int
	ExportMaxRows          int
	ExportConcurrency      int
	ProcessorTimeoutSec    int
	ProcessorConcurrency   int
	HTTPServiceTimeoutSec  int
	HTTPServiceConcurrency int
	SlowOperationMS        int
}

type operationLimiter struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func newOperationLimiter() *operationLimiter {
	return &operationLimiter{slots: make(map[string]chan struct{})}
}

func (l *operationLimiter) tryAcquire(kind string, limit int) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	ch := l.slots[kind]
	if ch == nil || cap(ch) != limit {
		ch = make(chan struct{}, limit)
		l.slots[kind] = ch
	}
	l.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

func (l *operationLimiter) acquire(ctx context.Context, kind string, limit int) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	ch := l.slots[kind]
	if ch == nil || cap(ch) != limit {
		ch = make(chan struct{}, limit)
		l.slots[kind] = ch
	}
	l.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-ctx.Done():
		return nil, false
	}
}

type operationFinish func(status string, rows int, truncated bool, extra ...slog.Attr)

func (s *Server) beginOperation(r *http.Request, kind, name string) (context.Context, operationFinish, bool) {
	if s.ops == nil {
		s.ops = newOperationLimiter()
	}
	limit := s.operationConcurrency(kind)
	release, ok := s.ops.tryAcquire(operationConcurrencyKey(kind), limit)
	if !ok {
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.OperationLimited(kind, "concurrency")
		}
		return r.Context(), nil, false
	}

	ctx := r.Context()
	cancel := func() {}
	if timeout := s.operationTimeout(kind); timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}

	start := time.Now()
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.OperationStart(kind)
	}
	return ctx, func(status string, rows int, truncated bool, extra ...slog.Attr) {
		cancel()
		release()
		d := time.Since(start)
		slow := s.isSlowOperation(d)
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.OperationFinish(kind, status, d, slow)
		}
		if slow {
			s.logSlowOperation(r, kind, name, status, d, rows, truncated, extra...)
		}
	}, true
}

func (s *Server) beginQueuedOperation(r *http.Request, kind, name string) (context.Context, operationFinish, bool) {
	if s.ops == nil {
		s.ops = newOperationLimiter()
	}
	ctx, cancelLifecycle := s.backgroundRequestContext(r.Context())
	cancelTimeout := func() {}
	if timeout := s.operationTimeout(kind); timeout > 0 {
		ctx, cancelTimeout = context.WithTimeout(ctx, timeout)
	}

	limit := s.operationConcurrency(kind)
	release, ok := s.ops.acquire(ctx, operationConcurrencyKey(kind), limit)
	if !ok {
		cancelTimeout()
		cancelLifecycle()
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.OperationLimited(kind, "concurrency")
		}
		return ctx, nil, false
	}

	start := time.Now()
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.OperationStart(kind)
	}
	logReq := r.WithContext(ctx)
	return ctx, func(status string, rows int, truncated bool, extra ...slog.Attr) {
		cancelTimeout()
		cancelLifecycle()
		release()
		d := time.Since(start)
		slow := s.isSlowOperation(d)
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.OperationFinish(kind, status, d, slow)
		}
		if slow {
			s.logSlowOperation(logReq, kind, name, status, d, rows, truncated, extra...)
		}
	}, true
}

func (s *Server) operationTimeout(kind string) time.Duration {
	l := s.cfg.Limits
	sec := 0
	switch kind {
	case opReportRun:
		sec = firstPositive(l.ReportTimeoutSec, l.RequestTimeoutSec)
	case opReportExport:
		sec = firstPositive(l.ExportTimeoutSec, l.ReportTimeoutSec, l.RequestTimeoutSec)
	case opProcessorRun:
		sec = firstPositive(l.ProcessorTimeoutSec, l.RequestTimeoutSec)
	case opHTTPServiceRun:
		sec = firstPositive(l.HTTPServiceTimeoutSec, l.RequestTimeoutSec)
	case opEntitySave, opFormEvent:
		// Отдельной настройки нет намеренно: и запись объекта, и событие формы
		// живут ровно столько, сколько живёт запрос.
		sec = l.RequestTimeoutSec
	default:
		sec = l.RequestTimeoutSec
	}
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec) * time.Second
}

func (s *Server) operationConcurrency(kind string) int {
	l := s.cfg.Limits
	switch kind {
	case opReportRun:
		return l.ReportConcurrency
	case opReportExport:
		return l.ExportConcurrency
	case opProcessorRun:
		return l.ProcessorConcurrency
	case opHTTPServiceRun:
		return l.HTTPServiceConcurrency
	case opFormEvent:
		// Событие формы — тот же прикладной DSL, что и обработка, и приходит
		// оно так же по клику пользователя. Предел общий с обработками:
		// заводить для него отдельную настройку значило бы просить
		// администратора угадать второе число для той же нагрузки.
		return l.ProcessorConcurrency
	default:
		return 0
	}
}

// operationConcurrencyKey separates the observable operation kind from the
// capacity budget it consumes. Managed entity-form events and processors are
// the same user-triggered application DSL workload and therefore share one
// processor_concurrency pool, while keeping distinct metrics/log kinds.
func operationConcurrencyKey(kind string) string {
	if kind == opFormEvent {
		return opProcessorRun
	}
	return kind
}

func (s *Server) isSlowOperation(d time.Duration) bool {
	ms := s.cfg.Limits.SlowOperationMS
	if ms <= 0 {
		return false
	}
	return d >= time.Duration(ms)*time.Millisecond
}

func (s *Server) logSlowOperation(r *http.Request, kind, name, status string, d time.Duration, rows int, truncated bool, extra ...slog.Attr) {
	attrs := []slog.Attr{
		slog.String("kind", kind),
		slog.String("name", name),
		slog.String("status", status),
		slog.Int64("duration_ms", d.Milliseconds()),
		slog.Int("rows", rows),
		slog.Bool("truncated", truncated),
		slog.String("route", chi.RouteContext(r.Context()).RoutePattern()),
		slog.String("request_id", middleware.GetReqID(r.Context())),
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		attrs = append(attrs, slog.String("user_login", u.Login))
	}
	attrs = append(attrs, extra...)
	oblog.Component("runtime_ops").LogAttrs(r.Context(), slog.LevelWarn, "slow operation", attrs...)
}

func sqlHash(sql string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sql))
	return fmt.Sprintf("%016x", h.Sum64())
}

func firstPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func operationStatus(ctx context.Context, err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "error"
}
