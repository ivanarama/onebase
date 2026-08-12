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

// Имена служебных событий dev-режима. Русские, как и остальные события шины:
// клиент их не согласовывает заранее, а раздаёт как onebase:<имя>.
const (
	// DevGenerationEvent — метка запуска процесса, уходит клиенту первым кадром
	// при каждом подключении к шине.
	DevGenerationEvent = "dev.поколение"
	// DevReloadEvent — конфигурация перечитана, странице пора обновиться.
	DevReloadEvent = "dev.перезагрузка"
)

// PublishEvent отправляет событие подписчикам шины: target — логин, роль,
// идентификатор пользователя или «*» (всем). Нужен вызывающим за пределами
// пакета: dev-сервер публикует «конфигурация перезагрузилась» после hot reload.
func (s *Server) PublishEvent(target, name string, data any) {
	if s == nil || s.hub == nil {
		return
	}
	s.hub.Publish(target, realtime.Event{Name: name, Data: data})
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
		frame, err := json.Marshal(map[string]any{"name": DevGenerationEvent, "data": s.devGeneration})
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
			frame, err := json.Marshal(map[string]any{"name": ev.Name, "data": ev.Data})
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
