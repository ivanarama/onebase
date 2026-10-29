package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
)

// Оповестить(ИмяСобытия, Параметр, Источник) — как Оповестить/
// ОбработкаОповещения 1С: серверный код (обработчик кнопки обработки, событие
// формы) оповещает ОТКРЫТЫЕ формы своего пользователя, и каждая форма с
// обработчиком ОбработкаОповещения выполняет его на своём текущем состоянии —
// с несохранёнными правками, без записи и без закрытия.
//
// Состояние открытой формы есть только в браузере, поэтому путь двухходовой:
//  1. Оповестить кладёт запись {пользователь, событие, параметр, источник} в
//     память сервера и шлёт формам пользователя по SSE только её
//     идентификатор (событие ui.оповестить);
//  2. каждая открытая форма с обработчиком отправляет обычное событие формы
//     ОбработкаОповещения со своими значениями и этим идентификатором, сервер
//     достаёт запись и кладёт в обработчик ИмяСобытия/Параметр/Источник.
//
// Параметр не ездит через браузер: он остаётся значением DSL (ссылка остаётся
// ссылкой), а подделать его запросом нельзя — браузер называет только
// идентификатор, и тот принимается лишь от того же пользователя.

// formNotifyEvent — имя SSE-события, по которому открытые формы запрашивают
// ОбработкаОповещения. Данные — {id, событие}: имя события открыто, чтобы на
// него могли подписаться и собственные скрипты страниц (onebase:ui.оповестить).
const formNotifyEvent = "ui.оповестить"

// formNotifyTTL — сколько живёт запись оповещения. Формы запрашивают её сразу
// по получении; запас — на форму, занятую долгим событием (очередь событий
// формы отправит ОбработкаОповещения после него).
const formNotifyTTL = 5 * time.Minute

// formNotifyMax — предел записей: цикл с Оповестить не должен съесть память.
const formNotifyMax = 1024

type formNotification struct {
	login   string
	event   string
	param   any
	source  any
	created time.Time
}

type formNotifyStore struct {
	mu    sync.Mutex
	items map[string]*formNotification
	order []string
	now   func() time.Time
}

func newFormNotifyStore() *formNotifyStore {
	return &formNotifyStore{items: make(map[string]*formNotification), now: time.Now}
}

func (st *formNotifyStore) put(n *formNotification) string {
	id := newFormNotifyID()
	st.mu.Lock()
	defer st.mu.Unlock()
	n.created = st.now()
	st.pruneLocked(n.created)
	for len(st.order) >= formNotifyMax {
		delete(st.items, st.order[0])
		st.order = st.order[1:]
	}
	st.items[id] = n
	st.order = append(st.order, id)
	return id
}

// get возвращает запись для этого пользователя. Запись не расходуется: одно
// оповещение получают все открытые формы пользователя, каждая своим запросом.
func (st *formNotifyStore) get(id, login string) (*formNotification, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pruneLocked(st.now())
	n, ok := st.items[id]
	if !ok || n.login != login {
		return nil, false
	}
	return n, true
}

func (st *formNotifyStore) pruneLocked(now time.Time) {
	i := 0
	for ; i < len(st.order); i++ {
		n := st.items[st.order[i]]
		if n != nil && now.Sub(n.created) < formNotifyTTL {
			break
		}
		delete(st.items, st.order[i])
	}
	st.order = st.order[i:]
}

func newFormNotifyID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// formNotifies — хранилище оповещений сервера (создаётся при первом вызове).
func (s *Server) formNotifies() *formNotifyStore {
	s.formNotifyOnce.Do(func() { s.formNotifyStore = newFormNotifyStore() })
	return s.formNotifyStore
}

// NotifyForms — interpreter.FormNotifier: записывает оповещение и шлёт его
// идентификатор открытым формам пользователя. Без шины (тесты, CLI) — no-op.
// На базе без пользователей логин пуст: оповещение получают анонимные окна,
// а это и есть все окна такой базы.
func (n hubNotifier) NotifyForms(eventName string, param, source any) {
	if n.hub == nil || n.forms == nil {
		return
	}
	id := n.forms.put(&formNotification{login: n.login, event: eventName, param: param, source: source})
	// Без replay-окна: оповещение — мгновенное событие, переподключившееся
	// после разрыва окно не должно получить устаревшее.
	n.hub.PublishEphemeralToLogins(realtime.Event{
		Name: formNotifyEvent,
		Data: map[string]any{"id": id, "событие": eventName},
	}, []string{n.login})
}

var errFormNotifyUnknown = errors.New("оповещение формы устарело или адресовано другому пользователю")

// addFormNotifyContext кладёт в обработчик ОбработкаОповещения переменные
// ИмяСобытия, Параметр, Источник по идентификатору из запроса. Чужой,
// выдуманный или истёкший идентификатор — отказ, а не пустые переменные.
func (s *Server) addFormNotifyContext(ctx context.Context, eventName, id string, vars map[string]any) error {
	if eventName != string(metadata.FormEventNotify) {
		return nil
	}
	login := ""
	if u := auth.UserFromContext(ctx); u != nil {
		login = u.Login
	}
	if id == "" {
		return errFormNotifyUnknown
	}
	n, ok := s.formNotifies().get(id, login)
	if !ok {
		return errFormNotifyUnknown
	}
	vars["ИмяСобытия"] = n.event
	vars["EventName"] = n.event
	vars["Параметр"] = n.param
	vars["Parameter"] = n.param
	vars["Источник"] = n.source
	vars["Source"] = n.source
	return nil
}
