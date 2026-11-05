package ui

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/httpservice"
)

// Callista подписывает GET /hs/telephony/line?did=… схемой v1 секундной меткой
// без nonce: два звонка на одну линию в одну секунду дают ОДИНАКОВУЮ подпись.
// Это не повтор атаки — справка ничего не меняет. С hmac_allow_repeated_get
// второй такой GET проходит; окно свежести и сама подпись проверяются как
// всегда, POST по-прежнему отвергает повтор. Путь — публичный /hs/*.
func TestService_HMACAllowRepeatedGET(t *testing.T) {
	const secret = "fast-channel"
	newServer := func(name string, allow bool) *Server {
		return newSecuredServiceServer(t, &httpservice.Service{
			Name: name, RootURL: strings.ToLower(name), Auth: "hmac", Secret: secret, HMACAllowRepeatedGET: allow,
			Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень", "POST": "Эхо"}}},
		})
	}
	send := func(s *Server, method, target, ts string, body string) int {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("X-Webhook-Timestamp", ts)
		r.Header.Set("X-Webhook-Signature", signV1(secret, ts, method, target, []byte(body)))
		w := httptest.NewRecorder()
		s.serviceDispatch(w, r)
		return w.Code
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)

	s := newServer("LineAllow", true)
	target := "/hs/lineallow/?did=4951983100"
	for i := 1; i <= 2; i++ {
		if code := send(s, http.MethodGet, target, now, ""); code != http.StatusOK {
			t.Fatalf("GET №%d с той же подписью: %d, ждали 200", i, code)
		}
	}
	stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if code := send(s, http.MethodGet, target, stale, ""); code != http.StatusUnauthorized {
		t.Fatalf("метка вне окна свежести: %d, ждали 401", code)
	}
	if code := send(s, http.MethodPost, "/hs/lineallow/", now, `{"x":1}`); code != http.StatusOK {
		t.Fatalf("первый POST: %d", code)
	}
	if code := send(s, http.MethodPost, "/hs/lineallow/", now, `{"x":1}`); code != http.StatusUnauthorized {
		t.Fatalf("повтор POST при ключе для GET: %d, ждали 401", code)
	}

	// Без ключа — прежняя защита: одинаковая подпись GET отвергается.
	strict := newServer("LineStrict", false)
	target = "/hs/linestrict/?did=4951983100"
	if code := send(strict, http.MethodGet, target, now, ""); code != http.StatusOK {
		t.Fatalf("первый GET без ключа: %d", code)
	}
	if code := send(strict, http.MethodGet, target, now, ""); code != http.StatusUnauthorized {
		t.Fatalf("повтор GET без ключа: %d, ждали 401", code)
	}
}
