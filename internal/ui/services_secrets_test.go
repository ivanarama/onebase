package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/httpservice"
)

// Секрет сервиса задан ссылкой на окружение: разыменовывается при проверке
// вызова, в конфигурации значения нет (план 83).
func TestService_TokenAuthResolvesSecretRef(t *testing.T) {
	t.Setenv("OB_TEST_SVC_TOKEN", "s3cret-из-окружения")
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "token", Secret: "env:OB_TEST_SVC_TOKEN",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})

	// Ссылка сама по себе токеном не является.
	r := httptest.NewRequest("GET", "/hs/t/", nil)
	r.Header.Set("X-Webhook-Token", "env:OB_TEST_SVC_TOKEN")
	w := httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ссылка вместо значения должна давать 401, получен %d", w.Code)
	}

	// Значение из окружения — проходит.
	r = httptest.NewRequest("GET", "/hs/t/", nil)
	r.Header.Set("X-Webhook-Token", "s3cret-из-окружения")
	w = httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("с верным токеном ожидался 200, получен %d (%s)", w.Code, w.Body.String())
	}
}

// Секрет зашифрован, мастер-ключа нет — сервис не пускает никого (fail-closed),
// а не пускает всех. Ответ при этом не рассказывает вызывающему, что именно
// не так с секретом.
func TestService_TokenAuthFailsClosedWhenSecretUnresolvable(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "token", Secret: "enc:AQIDBAUGBwgJCgsMDQ4PEBESExQV",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})

	for _, token := range []string{"", "что угодно", "enc:AQIDBAUGBwgJCgsMDQ4PEBESExQV"} {
		r := httptest.NewRequest("GET", "/hs/t/", nil)
		r.Header.Set("X-Webhook-Token", token)
		w := httptest.NewRecorder()
		s.serviceDispatch(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("токен %q: ожидался 401, получен %d (%s)", token, w.Code, w.Body.String())
		}
	}
}
