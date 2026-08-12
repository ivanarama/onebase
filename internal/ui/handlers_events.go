package ui

import (
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
type hubNotifier struct{ hub *realtime.Hub }

func (n hubNotifier) Publish(target, name string, data any) {
	if n.hub == nil {
		return
	}
	n.hub.Publish(target, realtime.Event{Name: name, Data: data})
}

// notifier возвращает издателя уведомлений для инжекции в DSL-переменные.
func (s *Server) notifier() interpreter.Notifier {
	return hubNotifier{hub: s.hub}
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
	_, ch, cancel := s.hub.SubscribeSince(userID, login, roles, lastID)
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
