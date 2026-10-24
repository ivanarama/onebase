package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Server-Timing для формы: из чего складывается время ответа открытия формы и
// события формы — обработчик, списки выбора, состояния элементов, отрисовка.
// Без этого замер видит одно число «ответ 0,7 с» и не говорит, что ужимать.
// Включается переменной окружения ONEBASE_SERVER_TIMING=1 (по умолчанию
// выключено: длительности — служебные сведения), заголовок виден в
// DevTools → Network → Timing и в замерах браузером.

type serverTimingKey struct{}

type serverTiming struct {
	mu    sync.Mutex
	start time.Time
	order []string
	spans map[string]time.Duration
}

func serverTimingOn() bool { return os.Getenv("ONEBASE_SERVER_TIMING") == "1" }

// serverTimingSpan отмечает начало фазы; вызов возвращённой функции — конец.
// Повторные фазы с тем же именем суммируются. Без включённого замера —
// пустая функция без аллокаций.
func serverTimingSpan(ctx context.Context, name string) func() {
	st, _ := ctx.Value(serverTimingKey{}).(*serverTiming)
	if st == nil {
		return func() {}
	}
	begin := time.Now()
	return func() {
		d := time.Since(begin)
		st.mu.Lock()
		if _, ok := st.spans[name]; !ok {
			st.order = append(st.order, name)
		}
		st.spans[name] += d
		st.mu.Unlock()
	}
}

func (st *serverTiming) header() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	parts := make([]string, 0, len(st.order)+1)
	for _, name := range st.order {
		parts = append(parts, fmt.Sprintf("%s;dur=%.1f", name, float64(st.spans[name].Microseconds())/1000))
	}
	parts = append(parts, fmt.Sprintf("total;dur=%.1f", float64(time.Since(st.start).Microseconds())/1000))
	return strings.Join(parts, ", ")
}

// withServerTiming оборачивает обработчик формы: кладёт сборщик в контекст,
// буферизует ответ и ставит заголовок, когда обработчик закончил, — иначе
// отрисовка страницы, которая пишет тело по ходу, не попала бы в замер.
// Буфер — только при включённом замере.
func withServerTiming(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !serverTimingOn() {
			next(w, r)
			return
		}
		st := &serverTiming{start: time.Now(), spans: map[string]time.Duration{}}
		tw := &serverTimingWriter{ResponseWriter: w, code: http.StatusOK}
		next(tw, r.WithContext(context.WithValue(r.Context(), serverTimingKey{}, st)))
		w.Header().Set("Server-Timing", st.header())
		w.WriteHeader(tw.code)
		_, _ = w.Write(tw.buf.Bytes())
	}
}

type serverTimingWriter struct {
	http.ResponseWriter
	code int
	buf  bytes.Buffer
}

func (w *serverTimingWriter) WriteHeader(code int) { w.code = code }

func (w *serverTimingWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
