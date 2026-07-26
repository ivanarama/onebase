package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metrics"
	"github.com/ivantit66/onebase/internal/scheduler"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/ivantit66/onebase/internal/webhook"
)

// mountMetrics вешает /metrics, отдающий HTTP-метрики (reg) и gauges пула
// соединений (store). Эндпоинт закрыт тем же токеном, что pprof и остальной
// debug API: токен в заголовке X-OneBase-Debug-Token или в query ?token=…
// (последнее удобно для prometheus scrape-конфига через params).
func mountMetrics(r chi.Router, token string, reg *metrics.Registry, store *storage.DB) {
	r.With(pprofTokenMiddleware(token)).Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		reg.WritePrometheus(w)
		writePoolStats(w, store)
	})
}

func registerRuntimeMetrics(reg *metrics.Registry, authRepo *auth.Repo, uiSrv *ui.Server, sched *scheduler.Scheduler, hooks *webhook.Dispatcher) {
	reg.RegisterGaugeFunc("onebase_active_sessions", "Активные пользовательские сессии.", func() float64 {
		if authRepo == nil {
			return 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		n, err := authRepo.ActiveSessionCount(ctx)
		if err != nil {
			return 0
		}
		return float64(n)
	})
	reg.RegisterGaugeFunc("onebase_sse_subscribers", "Активные SSE-подписчики.", func() float64 {
		return float64(uiSrv.SSESubscriberCount())
	})
	reg.RegisterGaugeFunc("onebase_active_scheduled_jobs", "Активные регламентные задания.", func() float64 {
		return float64(sched.ActiveRunCount())
	})
	if hooks != nil {
		reg.RegisterGaugeFunc("onebase_webhook_inflight", "Webhook-вызовы в очереди или выполнении.", func() float64 {
			return float64(hooks.Metrics().Inflight)
		})
		reg.RegisterCounterFunc("onebase_webhook_dispatched_total", "Всего поставленных в выполнение webhook-вызовов.", func() float64 {
			return float64(hooks.Metrics().Dispatched)
		})
		reg.RegisterCounterFunc("onebase_webhook_retry_total", "Всего повторных webhook-попыток.", func() float64 {
			return float64(hooks.Metrics().Retries)
		})
		reg.RegisterCounterFunc("onebase_webhook_failed_total", "Webhook-вызовы, завершившиеся ошибкой.", func() float64 {
			return float64(hooks.Metrics().Failed)
		})
		reg.RegisterCounterFunc("onebase_webhook_dropped_total", "Webhook-вызовы, не принятые из-за переполнения очереди или остановки.", func() float64 {
			return float64(hooks.Metrics().Dropped)
		})
	}
}

// writePoolStats дописывает gauges пула соединений PostgreSQL. Для SQLite пул
// отсутствует — метрики просто опускаются.
func writePoolStats(w http.ResponseWriter, store *storage.DB) {
	st := store.PoolStats()
	if st == nil {
		return
	}
	g := func(name, help string, val int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, val)
	}
	g("onebase_db_pool_acquired_conns", "Занятые соединения пула.", int64(st.AcquiredConns))
	g("onebase_db_pool_constructing_conns", "Соединения в процессе установки.", int64(st.ConstructingConns))
	g("onebase_db_pool_idle_conns", "Свободные соединения пула.", int64(st.IdleConns))
	g("onebase_db_pool_total_conns", "Всего соединений в пуле.", int64(st.TotalConns))
	g("onebase_db_pool_max_conns", "Максимум соединений пула.", int64(st.MaxConns))

	c := func(name, help string, val int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, val)
	}
	c("onebase_db_pool_acquire_total", "Всего успешных Acquire.", st.AcquireCount)
	c("onebase_db_pool_empty_acquire_total", "Acquire, ждавшие свободного соединения.", st.EmptyAcquireCount)
	c("onebase_db_pool_canceled_acquire_total", "Acquire, отменённые контекстом.", st.CanceledAcquireCount)
	c("onebase_db_pool_new_conns_total", "Всего созданных соединений.", st.NewConnsCount)
}
