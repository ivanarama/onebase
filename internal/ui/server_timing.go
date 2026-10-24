package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	notes []string
	// counts — счётчики без длительности (попадания кэша и т. п.), в
	// заголовке идут после фаз как name;desc="N".
	countOrder []string
	counts     map[string]int
}

// serverTimingCount увеличивает именованный счётчик запроса.
func serverTimingCount(ctx context.Context, name string) {
	st, _ := ctx.Value(serverTimingKey{}).(*serverTiming)
	if st == nil {
		return
	}
	st.mu.Lock()
	if st.counts == nil {
		st.counts = map[string]int{}
	}
	if _, ok := st.counts[name]; !ok {
		st.countOrder = append(st.countOrder, name)
	}
	st.counts[name]++
	st.mu.Unlock()
}

// serverTimingNote добавляет в заголовок запись без длительности — с
// описанием (desc), закодированным для заголовка HTTP (url.QueryEscape):
// например, сколько и каких записей сделал обработчик события.
func serverTimingNote(ctx context.Context, name, desc string) {
	st, _ := ctx.Value(serverTimingKey{}).(*serverTiming)
	if st == nil {
		return
	}
	st.mu.Lock()
	st.notes = append(st.notes, fmt.Sprintf("%s;desc=%q", name, url.QueryEscape(desc)))
	st.mu.Unlock()
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
	for _, name := range st.countOrder {
		parts = append(parts, fmt.Sprintf("%s;desc=\"%d\"", name, st.counts[name]))
	}
	parts = append(parts, st.notes...)
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
