package realtime

import (
	"testing"
	"time"
)

// recv ждёт событие из канала подписчика с таймаутом, чтобы тест не висел.
func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev := <-ch:
		return ev, true
	case <-time.After(time.Second):
		return Event{}, false
	}
}

// empty проверяет, что в канал НИЧЕГО не пришло за короткий интервал.
func empty(t *testing.T, ch <-chan Event) bool {
	t.Helper()
	select {
	case ev := <-ch:
		t.Logf("неожиданное событие: %+v", ev)
		return false
	case <-time.After(100 * time.Millisecond):
		return true
	}
}

func TestHub_PublishToLogin_DeliversToMatchingSubscriber(t *testing.T) {
	h := NewHub()
	_, ch, cancel := h.Subscribe("u1", "ivan", []string{"Оператор"})
	defer cancel()

	h.Publish("ivan", Event{Name: "звонок.входящий", Data: "+79990001122"})

	ev, ok := recv(t, ch)
	if !ok {
		t.Fatal("событие не доставлено подписчику с совпадающим логином")
	}
	if ev.Name != "звонок.входящий" || ev.Data != "+79990001122" {
		t.Fatalf("неожиданное событие: %+v", ev)
	}
}

func TestHub_PublishToRole_DeliversToRoleMembers(t *testing.T) {
	h := NewHub()
	_, ch, cancel := h.Subscribe("u1", "ivan", []string{"Оператор", "Кассир"})
	defer cancel()

	h.Publish("роль:Оператор", Event{Name: "очередь.переполнена", Data: 7})

	ev, ok := recv(t, ch)
	if !ok {
		t.Fatal("событие не доставлено подписчику с совпадающей ролью")
	}
	if ev.Name != "очередь.переполнена" {
		t.Fatalf("неожиданное событие: %+v", ev)
	}
}

func TestHub_Broadcast_DeliversToAll(t *testing.T) {
	h := NewHub()
	_, ch1, c1 := h.Subscribe("u1", "ivan", nil)
	defer c1()
	_, ch2, c2 := h.Subscribe("u2", "petr", []string{"Кассир"})
	defer c2()

	h.Publish("*", Event{Name: "уведомление", Data: "перезагрузка в 18:00"})

	if _, ok := recv(t, ch1); !ok {
		t.Fatal("широковещание не дошло до первого подписчика")
	}
	if _, ok := recv(t, ch2); !ok {
		t.Fatal("широковещание не дошло до второго подписчика")
	}
}

func TestHub_SubscribeSince_ReplaysRecentMatchingEvents(t *testing.T) {
	h := NewHub()
	h.Publish("*", Event{Name: "старое", Data: 1})
	lastID := h.recent[0].ev.ID
	h.Publish("*", Event{Name: "уведомление", Data: "пока соединение восстанавливалось"})

	_, ch, cancel := h.SubscribeSince("u1", "ivan", nil, lastID)
	defer cancel()

	ev, ok := recv(t, ch)
	if !ok {
		t.Fatal("недавнее широковещательное событие не переиграно новому подписчику")
	}
	if ev.ID == 0 || ev.Name != "уведомление" || ev.Data != "пока соединение восстанавливалось" {
		t.Fatalf("неожиданное replay-событие: %+v", ev)
	}
}

func TestHub_NewPageDoesNotReplayRecentEvents(t *testing.T) {
	h := NewHub()
	h.Publish("*", Event{Name: "до загрузки страницы"})
	_, ch, cancel := h.SubscribeSince("u1", "ivan", nil, 0)
	defer cancel()
	if !empty(t, ch) {
		t.Fatal("новая страница не должна получать события из прошлого")
	}
}

func TestHub_SubscribeSince_SkipsAlreadySeenEvents(t *testing.T) {
	h := NewHub()
	_, ch, cancel := h.Subscribe("u1", "ivan", nil)
	h.Publish("ivan", Event{Name: "личное", Data: 1})
	ev, ok := recv(t, ch)
	if !ok {
		t.Fatal("первое событие не доставлено")
	}
	cancel()

	_, ch2, cancel2 := h.SubscribeSince("u1", "ivan", nil, ev.ID)
	defer cancel2()
	if !empty(t, ch2) {
		t.Fatal("событие с Last-Event-ID не должно переигрываться повторно")
	}
}

func TestHub_PublishToLogin_SkipsOtherUsers(t *testing.T) {
	h := NewHub()
	_, chIvan, c1 := h.Subscribe("u1", "ivan", nil)
	defer c1()
	_, chPetr, c2 := h.Subscribe("u2", "petr", nil)
	defer c2()

	h.Publish("ivan", Event{Name: "личное", Data: "секрет"})

	if _, ok := recv(t, chIvan); !ok {
		t.Fatal("адресат не получил событие")
	}
	if !empty(t, chPetr) {
		t.Fatal("чужому подписчику доставлено адресное событие")
	}
}

func TestHub_Cancel_StopsDelivery(t *testing.T) {
	h := NewHub()
	_, ch, cancel := h.Subscribe("u1", "ivan", nil)
	cancel()

	h.Publish("ivan", Event{Name: "после.отписки", Data: 1})

	// Канал закрыт отпиской: чтение даёт zero-value c ok=false из закрытого
	// канала, новых событий не приходит.
	if ev, open := <-ch; open {
		t.Fatalf("после отписки доставлено событие: %+v", ev)
	}
}

func TestHub_SubscriberCount(t *testing.T) {
	h := NewHub()
	if got := h.SubscriberCount(); got != 0 {
		t.Fatalf("пустая шина: ожидалось 0, получено %d", got)
	}
	_, _, cancel := h.Subscribe("u1", "ivan", nil)
	if got := h.SubscriberCount(); got != 1 {
		t.Fatalf("после подписки: ожидалось 1, получено %d", got)
	}
	cancel()
	if got := h.SubscriberCount(); got != 0 {
		t.Fatalf("после отписки: ожидалось 0, получено %d", got)
	}
}

func TestHub_Publish_NonBlockingWhenBufferFull(t *testing.T) {
	h := NewHub()
	// Подписчик не читает канал. Публикуем больше, чем буфер: лишние кадры
	// должны дропаться, а Publish — возвращаться, не блокируясь.
	_, _, cancel := h.Subscribe("u1", "ivan", nil)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*4; i++ {
			h.Publish("ivan", Event{Name: "поток", Data: i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish заблокировался на медленном (не читающем) подписчике")
	}
}

func TestHubCloseDisconnectsAndRejectsSubscribers(t *testing.T) {
	h := NewHub()
	_, ch, cancel := h.Subscribe("u1", "ivan", nil)

	h.Close()
	h.Close()
	cancel()

	if _, open := <-ch; open {
		t.Fatal("existing subscriber channel remained open")
	}
	if got := h.SubscriberCount(); got != 0 {
		t.Fatalf("subscriber count after close = %d", got)
	}
	_, after, afterCancel := h.Subscribe("u2", "petr", nil)
	defer afterCancel()
	if _, open := <-after; open {
		t.Fatal("subscription after close returned an open channel")
	}
	h.Publish("*", Event{Name: "ignored"})
}
