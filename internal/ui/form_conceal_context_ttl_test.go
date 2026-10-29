package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the HTTP entry points with time controlled only at the server clock.
// Do not inspect or modify the internal instance/grant ledger: successful later
// disclosure and persisted values must demonstrate the lifetime contract.
func concealTTLFixture(t *testing.T, db *storage.DB, fresh bool) (concealTestClient, string, string) {
	t.Helper()
	c, id := concealFixture(t, db)
	key, secret := id.String(), "секрет"
	if fresh {
		key, secret = "", "серверное умолчание"
		for i := range c.entity.Fields {
			if c.entity.Fields[i].Name == "Секрет" {
				c.entity.Fields[i].Default = secret
			}
		}
	}
	return c, key, secret
}

func assertConcealTTLResponse(t *testing.T, r formEventResponse, secret string, revealed bool) {
	t.Helper()
	if !r.OK {
		t.Fatalf("form event failed: %+v", r)
	}
	want := ""
	if revealed {
		want = secret
	}
	if got := anyString(valueCI(r.Values, "Секрет")); got != want || (len(r.Revealed) == 1) != revealed {
		t.Fatalf("disclosure: secret=%q revealed=%v, want %q revealed=%v", got, r.Revealed, want, revealed)
	}
}

func assertConcealTTLHiddenSubmit(t *testing.T, db *storage.DB, c concealTestClient, key, secret string) {
	t.Helper()
	name := uuid.NewString()
	rec := c.request(t, "POST", key, "", url.Values{
		"Наименование": {name}, "Секрет": {"подмена"}, "Маской": {"false"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}
	if key == "" {
		location := rec.Header().Get("Location")
		key = location[strings.LastIndex(location, "/")+1:]
	}
	id, err := uuid.Parse(key)
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
	if err != nil {
		t.Fatal(err)
	}
	if got := anyString(valueCI(row, "Секрет")); got != secret {
		t.Fatalf("hidden input persisted: secret=%q, want %q", got, secret)
	}
	if got := anyString(valueCI(row, "Наименование")); got != name {
		t.Fatalf("ordinary field was not saved: %q, want %q", got, name)
	}
}

func TestConcealContextSlidingLifetime(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "existing"
		if fresh {
			name = "new-default"
		}
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				c, key, secret := concealTTLFixture(t, db, fresh)
				t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				now := t0
				c.server.concealClock = func() time.Time { return now }
				c = c.open(t, key)
				token := c.token
				now = t0.Add(23 * time.Hour)
				assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), secret, true)
				// The first grant has expired, but the same form context can be
				// authorized again after 24 hours from the original GET.
				now = t0.Add(24*time.Hour + time.Minute)
				assertConcealTTLResponse(t, c.event(t, key, "", nil), secret, false)
				assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), secret, true)
				if c.token != token {
					t.Fatal("test replaced the form context")
				}
				// A renewed context never extends the ten-minute field grant.
				now = now.Add(10 * time.Minute)
				assertConcealTTLResponse(t, c.event(t, key, "", nil), secret, false)
				assertConcealTTLHiddenSubmit(t, db, c, key, secret)
			})
		})
	}
}

func TestConcealContextOrdinaryRequestsDoNotRenew(t *testing.T) {
	for _, activity := range []string{"read", "event", "submit"} {
		for _, fresh := range []bool{false, true} {
			name := activity + "/existing"
			if fresh {
				name = activity + "/new-default"
			}
			t.Run(name, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					c, key, secret := concealTTLFixture(t, db, fresh)
					t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
					now := t0
					c.server.concealClock = func() time.Time { return now }
					c = c.open(t, key)
					now = t0.Add(23 * time.Hour)
					switch activity {
					case "read":
						assertConcealTTLResponse(t, c.event(t, key, "", nil), secret, false)
					case "event":
						// Successful handler without a trusted assignment.
						assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Маской": {"false"}}), secret, false)
					case "submit":
						assertConcealTTLHiddenSubmit(t, db, c, key, secret)
					}
					now = t0.Add(24 * time.Hour)
					// Even a trusted handler plus forged client controller cannot
					// revive a context that has had no grant for a full day.
					assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}, "Маской": {"false"}}), secret, false)
					assertConcealTTLHiddenSubmit(t, db, c, key, secret)
					// A genuinely new GET, rather than client input, restores access.
					c.token = ""
					c = c.open(t, key)
					assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), secret, true)
				})
			})
		}
	}
}

func TestConcealGrantOrdinaryRequestsDoNotRenew(t *testing.T) {
	for _, activity := range []string{"read", "event", "submit"} {
		for _, fresh := range []bool{false, true} {
			// Saving a new object ends its unsaved identity; that is covered
			// separately, rather than confusing identity loss with grant expiry.
			if activity == "submit" && fresh {
				continue
			}
			name := activity + "/existing"
			if fresh {
				name = activity + "/new-default"
			}
			t.Run(name, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					c, key, secret := concealTTLFixture(t, db, fresh)
					t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
					now := t0
					c.server.concealClock = func() time.Time { return now }
					c = c.open(t, key)
					assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), secret, true)
					now = t0.Add(9 * time.Minute)
					switch activity {
					case "read":
						// An enabled new-object control is posted by the browser;
						// omitting it would be a legitimate edit to an empty value.
						assertConcealTTLResponse(t, c.event(t, key, "", url.Values{"Секрет": {secret}}), secret, true)
					case "event":
						assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Секрет": {secret}}), secret, true)
					case "submit":
						rec := c.request(t, "POST", key, "", url.Values{"Наименование": {"обычное"}, "Секрет": {secret}})
						if rec.Code != http.StatusSeeOther {
							t.Fatalf("authorized submit: %d %s", rec.Code, rec.Body.String())
						}
					}
					now = t0.Add(10 * time.Minute)
					assertConcealTTLResponse(t, c.event(t, key, "", nil), secret, false)
					// The context still permits a fresh, independently timed grant.
					assertConcealTTLResponse(t, c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}), secret, true)
					now = t0.Add(20 * time.Minute)
					assertConcealTTLHiddenSubmit(t, db, c, key, secret)
				})
			})
		}
	}
}
