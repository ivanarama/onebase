package auth

// Гейт выдачи QR на входе. Тест белого ящика: небезопасное состояние —
// challenge с секретом, но без разрешения на привязку — штатным путём сейчас не
// возникает, и через форму входа его не построить. Именно поэтому проверка
// нужна здесь: прежний гейт («секрет есть») держался ровно на этом совпадении,
// а не на разрешении, и первая же правка, заводящая секрет раньше, тихо
// превратила бы картинку в выдачу второго фактора по одному паролю (#615).

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func qrRequest(t *testing.T, h *Handlers, ch Challenge) *httptest.ResponseRecorder {
	t.Helper()
	token, err := h.challenges().Issue(ch)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/login/2fa/qr", nil)
	req.AddCookie(&http.Cookie{Name: challengeCookie, Value: token})
	rec := httptest.NewRecorder()
	h.TwoFactorQR(rec, req)
	return rec
}

func TestTwoFactorQR_БезРазрешенияПривязкиНеОтдаётся(t *testing.T) {
	h := &Handlers{Challenges: NewChallenges(challengeTTL)}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	rec := qrRequest(t, h, Challenge{
		UserID: "u1", Login: "admin", Enroll: true, Secret: secret,
		EnrollAuthorized: false,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("QR отдан без разрешения на привязку (код %d) — секрет TOTP утёк", rec.Code)
	}
}

func TestTwoFactorQR_СРазрешениемОтдаётся(t *testing.T) {
	h := &Handlers{Challenges: NewChallenges(challengeTTL)}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	rec := qrRequest(t, h, Challenge{
		UserID: "u1", Login: "admin", Enroll: true, Secret: secret,
		EnrollAuthorized: true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("QR не отдан разрешённой привязке (код %d): настройка 2FA сломана", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("пустая картинка QR")
	}
}

// Переход к шагу сканирования QR продлевает challenge.
//
// Отсчёт TTL шёл от ввода пароля, а впереди самый долгий шаг: поставить
// приложение, отсканировать код, дождаться следующего окна. Истёкший на этом
// месте challenge раньше означал не «начните заново», а «идите к
// администратору за новым кодом привязки» — билет к тому моменту уже был
// погашен (#615).
func TestChallenges_ПродлениеПереживаетИсходныйСрок(t *testing.T) {
	c := NewChallenges(time.Minute)
	token, err := c.Issue(Challenge{UserID: "u1", Login: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	// Состариваем challenge до порога истечения.
	c.mu.Lock()
	c.items[token].expires = time.Now().Add(5 * time.Millisecond)
	c.mu.Unlock()

	if !c.Renew(token) {
		t.Fatal("Renew не нашёл живой challenge")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get(token); !ok {
		t.Fatal("challenge истёк вопреки продлению: настройка 2FA сорвётся по времени")
	}
}
