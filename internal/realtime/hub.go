// Package realtime — внутрипроцессная шина уведомлений «сервер → браузер».
// Hub маршрутизирует события подписчикам (открытым SSE-соединениям) по адресу:
// логин пользователя, "роль:<Имя>" или "*" (всем онлайн). Область действия —
// один процесс onebase (как БлокировкаДанных); горизонтальное масштабирование
// потребует внешнего брокера (Redis/NATS) — отдельный план.
package realtime

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// rolePrefix — адрес вида "роль:Оператор" доставляет событие всем подписчикам с
// этой ролью.
const rolePrefix = "роль:"

// Event — одно уведомление: имя события и произвольные данные (сериализуются в
// JSON на стороне SSE-эндпоинта).
type Event struct {
	ID     int64
	Name   string
	Data   any
	System string
}

// subscriberBuffer holds the full replay window. A slower live subscriber is
// disconnected on overflow so EventSource reconnects and resumes by event ID.
const subscriberBuffer = recentLimit

const recentTTL = 15 * time.Second

const recentLimit = 64

type subscriber struct {
	id     string
	userID string
	login  string
	roles  []string
	ch     chan Event
}

// Hub — потокобезопасный реестр подписчиков.
type Hub struct {
	mu     sync.Mutex
	subs   map[string]*subscriber
	seq    int64
	recent []recentEvent
	closed bool
}

type recentEvent struct {
	target string
	ev     Event
	at     time.Time
}

// NewHub создаёт пустую шину.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]*subscriber)}
}

// Subscribe регистрирует подписчика и возвращает его id, канал событий и функцию
// отписки. cancel закрывает канал и удаляет подписчика; вызывать при завершении
// SSE-соединения.
func (h *Hub) Subscribe(userID, login string, roles []string) (id string, ch <-chan Event, cancel func()) {
	return h.SubscribeSince(userID, login, roles, 0)
}

// SubscribeSince регистрирует подписчика и сразу отдаёт ему недавние события,
// которые он мог пропустить при автоматическом reconnect. lastID берётся из
// Last-Event-ID SSE-клиента; 0 означает новую страницу без replay.
func (h *Hub) SubscribeSince(userID, login string, roles []string, lastID int64) (id string, ch <-chan Event, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		closed := make(chan Event)
		close(closed)
		return "", closed, func() {}
	}
	h.seq++
	id = "s" + strconv.FormatInt(h.seq, 10)
	s := &subscriber{id: id, userID: userID, login: login, roles: roles, ch: make(chan Event, subscriberBuffer)}
	h.subs[id] = s
	h.replayLocked(s, lastID, time.Now())
	return id, s.ch, func() { h.unsubscribe(id) }
}

// SubscriberCount возвращает число активных подписчиков (для тестов и метрик).
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *Hub) unsubscribe(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(s.ch)
	}
}

// Publish доставляет событие всем подписчикам, чей адрес совпал с target.
// Отправка неблокирующая: переполненный подписчик отключается и восстановит
// пропущенные кадры из replay-окна после EventSource reconnect.
func (h *Hub) Publish(target string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.seq++
	ev.ID = h.seq
	now := time.Now()
	h.appendRecentLocked(target, ev, now)
	for id, s := range h.subs {
		if matches(s, target) {
			select {
			case s.ch <- ev:
			default:
				// A silent drop would leave an ID gap while the connection remains
				// open. Closing it makes EventSource reconnect with Last-Event-ID.
				delete(h.subs, id)
				close(s.ch)
			}
		}
	}
}

// Identity — снимок identity активного подписчика для внешнего RLS-резолвера
// (план 87, ступень A): по (UserID, Login) UI загружает актуальные роли/атрибуты
// и решает, кому из подключённых видна изменённая строка.
type Identity struct {
	UserID string
	Login  string
}

// ActiveIdentities возвращает уникальные identity активных подписчиков. Стоимость
// адресации живого списка пропорциональна реально подключённым пользователям, а
// Hub не начинает зависеть от auth/access.
func (h *Hub) ActiveIdentities() []Identity {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := make(map[string]struct{}, len(h.subs))
	out := make([]Identity, 0, len(h.subs))
	for _, s := range h.subs {
		key := s.userID + "\x00" + s.login
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Identity{UserID: s.userID, Login: s.login})
	}
	return out
}

// PublishEphemeralToLogins доставляет событие только подписчикам, чей логин есть
// в logins, и НЕ кладёт его в replay-окно (план 87, ступень A): «живой список»
// перечитывается по сигналу инвалидации, а при SSE reconnect клиент сам помечает
// списки dirty — переигрывать сигнал по устаревшему за время disconnect снимку
// прав небезопасно. Пустой logins — no-op.
func (h *Hub) PublishEphemeralToLogins(ev Event, logins []string) {
	if len(logins) == 0 {
		return
	}
	set := make(map[string]struct{}, len(logins))
	for _, l := range logins {
		set[l] = struct{}{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.seq++
	ev.ID = h.seq
	for id, s := range h.subs {
		if _, ok := set[s.login]; !ok {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			delete(h.subs, id)
			close(s.ch)
		}
	}
}

// Close disconnects every subscriber and prevents new subscriptions. Closing
// the SSE channels lets http.Server.Shutdown drain long-lived event streams.
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, sub := range h.subs {
		delete(h.subs, id)
		close(sub.ch)
	}
	h.recent = nil
}

func (h *Hub) appendRecentLocked(target string, ev Event, now time.Time) {
	cutoff := now.Add(-recentTTL)
	dst := h.recent[:0]
	for _, item := range h.recent {
		if item.at.After(cutoff) {
			dst = append(dst, item)
		}
	}
	h.recent = append(dst, recentEvent{target: target, ev: ev, at: now})
	if len(h.recent) > recentLimit {
		h.recent = h.recent[len(h.recent)-recentLimit:]
	}
}

func (h *Hub) replayLocked(s *subscriber, lastID int64, now time.Time) {
	if lastID <= 0 {
		return
	}
	cutoff := now.Add(-recentTTL)
	for _, item := range h.recent {
		if item.ev.ID <= lastID || item.at.Before(cutoff) || !matches(s, item.target) {
			continue
		}
		select {
		case s.ch <- item.ev:
		default:
			return
		}
	}
}

// matches решает, адресовано ли событие подписчику.
func matches(s *subscriber, target string) bool {
	if target == "*" {
		return true
	}
	if role := strings.TrimPrefix(target, rolePrefix); role != target {
		for _, r := range s.roles {
			if r == role {
				return true
			}
		}
		return false
	}
	return s.login == target
}
