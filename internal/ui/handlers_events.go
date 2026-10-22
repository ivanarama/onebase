package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/realtime"
)

// hubNotifier адаптирует *realtime.Hub к interpreter.Notifier: DSL-функция
// ОтправитьУведомление(Кому, Событие, Данные) → Hub.Publish(Кому, Event{...}).
//
// Команды формы самому себе (ui.открытьФорму / ui.закрытьФорму из обработчика
// формы) уходят только в оболочку, откуда пришло событие. Иначе у
// пользователя с двумя вкладками браузера форма открывалась копией и во
// второй — с повторным ПриОткрытии, — а закрыть копию в фоновой вкладке
// команде было нечем, и пользователь работал в незакрытой форме.
type hubNotifier struct {
	hub   *realtime.Hub
	shell string
	login string
}

func (n hubNotifier) Publish(target, name string, data any) {
	if n.hub == nil {
		return
	}
	ev := realtime.Event{Name: name, Data: data}
	if n.shell != "" && n.login != "" && target == n.login && shellScopedEvent(name) {
		ev.Shell = n.shell
	}
	n.hub.Publish(target, ev)
}

// shellScopedEvent — команды, которые относятся к оболочке-источнику, а не ко
// всем сеансам пользователя. Тосты и обновление списков по-прежнему идут во
// все оболочки: там они уместны.
func shellScopedEvent(name string) bool {
	return name == "ui.открытьФорму" || name == "ui.закрытьФорму"
}

// notifier возвращает издателя уведомлений для инжекции в DSL-переменные.
// Адрес оболочки и логин берутся из контекста запроса: у фоновых заданий их
// нет, и уведомление уходит всем сеансам, как раньше.
func (s *Server) notifier(ctx context.Context) interpreter.Notifier {
	n := hubNotifier{hub: s.hub, shell: shellIDFrom(ctx)}
	if u := auth.UserFromContext(ctx); u != nil {
		n.login = u.Login
	}
	return n
}

// shellHeader — адрес оболочки в запросах формы (managed.js). Адрес — id
// верхнего окна браузера, тот же, с которым оно подписано на /ui/events.
const shellHeader = "X-OneBase-Shell"

type shellIDKey struct{}

// withShellID кладёт в контекст адрес оболочки из запроса. Адрес — случайный
// id окна; всё, что на него не похоже, отбрасывается.
func withShellID(ctx context.Context, raw string) context.Context {
	if id := validShellID(raw); id != "" {
		return context.WithValue(ctx, shellIDKey{}, id)
	}
	return ctx
}

func shellIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(shellIDKey{}).(string)
	return id
}

func validShellID(raw string) string {
	if raw == "" || len(raw) > 64 {
		return ""
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return ""
		}
	}
	return raw
}

const (
	devGenerationSystem = "dev-generation"
	devReloadSystem     = "dev-reload"
)

// PublishDevReload emits a trusted system frame. The DSL notifier only fills
// Name/Data, so user code cannot forge browser reloads by choosing a reserved
// notification name.
func (s *Server) PublishDevReload() {
	if s == nil || s.hub == nil {
		return
	}
	s.hub.Publish("*", realtime.Event{System: devReloadSystem})
}

// eventsStream — SSE-эндпоинт GET /ui/events: подписывает текущего пользователя
// на real-time-шину и стримит адресованные ему события кадрами
// «event: <имя>\ndata: <json>\n\n». Монтируется в защищённой группе, поэтому
// неаутентифицированный доступ на базе с пользователями отсекает middleware;
// на базе без пользователей подписка анонимна (получает только широковещание «*»).
func (s *Server) eventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "стриминг не поддерживается сервером", http.StatusInternalServerError)
		return
	}

	var userID, login string
	var roles []string
	if u := auth.UserFromContext(r.Context()); u != nil {
		userID, login = u.ID, u.Login
		for _, role := range u.Roles {
			if role != nil {
				roles = append(roles, role.Name)
			}
		}
	}
	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	_, ch, cancel := s.hub.SubscribeShellSince(userID, login, roles, validShellID(r.URL.Query().Get("shell")), lastID)
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // не буферизировать за reverse-proxy (nginx)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Dev-режим: сервер представляется меткой своего запуска. Клиент сравнивает
	// её с прошлой и, увидев другую, перечитывает страницу — так браузер
	// подхватывает пересборку платформы, при которой SSE-соединение просто
	// рвётся и переподключается, а сказать «я уже другой» иначе нечем.
	if s.devGeneration != "" {
		frame, err := json.Marshal(map[string]any{"system": devGenerationSystem, "data": s.devGeneration})
		if err == nil {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
				return
			}
			flusher.Flush()
		}
	}

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			// Кадр — дефолтное message-событие с {name,data} в JSON: клиент один
			// раз парсит и ретранслирует в window CustomEvent('onebase:<name>'),
			// поэтому имена событий не нужно согласовывать с сервером заранее.
			frame, err := json.Marshal(map[string]any{"name": ev.Name, "data": ev.Data, "system": ev.System})
			if err != nil {
				continue
			}
			if ev.ID > 0 {
				if _, err := fmt.Fprintf(w, "id: %d\n", ev.ID); err != nil {
					return
				}
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
