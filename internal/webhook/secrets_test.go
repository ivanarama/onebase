package webhook

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Токен в заголовке разыменовывается в момент доставки (план 83): в
// конфигурации лежит ссылка, наружу уходит значение.
func TestDispatcher_ResolvesSecretRefInHeader(t *testing.T) {
	t.Setenv("OB_TEST_HOOK_TOKEN", "тайный-токен")
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	d := New([]Config{{
		Name:    "tg",
		On:      "document.post",
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer ${env:OB_TEST_HOOK_TOKEN}"},
		Body:    `{"id": "{{id}}"}`,
	}}, nil)
	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("ожидался 1 запрос, получено %d", rec.count())
	}
	if got := rec.heads[0].Get("Authorization"); got != "Bearer тайный-токен" {
		t.Fatalf("заголовок = %q, ожидалось «Bearer тайный-токен»", got)
	}
}

// Токен в URL — тот же путь: ссылка раскрывается перед отправкой.
func TestDispatcher_ResolvesSecretRefInURL(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	t.Setenv("OB_TEST_HOOK_PATH", "тайный-путь")

	d := New([]Config{{
		Name: "tg",
		On:   "document.post",
		URL:  srv.URL + "/${env:OB_TEST_HOOK_PATH}",
		Body: `{}`,
	}}, nil)
	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 1 {
		t.Fatalf("ожидался 1 запрос, получено %d", rec.count())
	}
}

// enc:-значение без мастер-ключа отменяет доставку целиком — уйти к чужому
// серверу с пустым токеном нельзя (fail-closed). В журнале при этом остаётся
// причина, а сам секрет не раскрывается.
func TestDispatcher_FailsClosedWhenSecretUnresolvable(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	var entries []LogEntry
	d := New([]Config{{
		Name:    "tg",
		On:      "document.post",
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "enc:AQIDBAUGBwgJCgsMDQ4PEBESExQV"},
		Body:    `{}`,
	}}, func(e LogEntry) { entries = append(entries, e) })
	d.Dispatch(Event{Name: "document.post", Entity: "Реализация", ID: "id1"})
	d.Wait()

	if rec.count() != 0 {
		t.Fatalf("доставка должна быть отменена, а ушло запросов: %d", rec.count())
	}
	if len(entries) != 1 {
		t.Fatalf("ожидалась одна запись журнала, получено %d", len(entries))
	}
	if !strings.Contains(entries[0].Error, "секрет веб-хука не разыменован") {
		t.Fatalf("причина отказа не записана: %q", entries[0].Error)
	}
}
