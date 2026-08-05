package auth

// Проверки TOTP (план 84). Контрольные значения — из приложения B RFC 6238
// (секрет «12345678901234567890», HMAC-SHA1); шестизначный код — последние
// шесть цифр восьмизначного контрольного.

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret — ASCII-секрет RFC 6238 в base32.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		got, err := TOTPCode(rfcSecret, TOTPStep(time.Unix(c.unix, 0)))
		if err != nil {
			t.Fatalf("TOTPCode(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("TOTPCode(%d) = %s, ожидалось %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPAcceptsCurrentAndNeighbourSteps(t *testing.T) {
	now := time.Unix(1111111109, 0)
	for _, delta := range []int64{-1, 0, 1} {
		code, err := TOTPCode(rfcSecret, TOTPStep(now)+delta)
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if _, ok := VerifyTOTP(rfcSecret, code, now, 0); !ok {
			t.Errorf("код шага %+d отвергнут, а должен приниматься", delta)
		}
	}
}

func TestVerifyTOTPRejectsExpiredCode(t *testing.T) {
	now := time.Unix(1111111109, 0)
	stale, err := TOTPCode(rfcSecret, TOTPStep(now)-3)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if _, ok := VerifyTOTP(rfcSecret, stale, now, 0); ok {
		t.Fatal("код трёхшаговой давности принят — окно допуска слишком широкое")
	}
}

func TestVerifyTOTPRejectsReplayedStep(t *testing.T) {
	now := time.Unix(1111111109, 0)
	step := TOTPStep(now)
	code, err := TOTPCode(rfcSecret, step)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if _, ok := VerifyTOTP(rfcSecret, code, now, step); !ok {
		t.Fatal("код текущего шага должен приниматься, пока шаг не израсходован")
	}
	// После использования минимально допустимый шаг сдвигается за текущий —
	// тот же код второй раз не проходит.
	if _, ok := VerifyTOTP(rfcSecret, code, now, step+1); ok {
		t.Fatal("переигранный код принят повторно")
	}
}

func TestGenerateTOTPSecretIsUsable(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if NormalizeTOTPSecret(secret) != secret {
		t.Errorf("секрет %q не в каноническом виде", secret)
	}
	now := time.Now()
	code, err := TOTPCode(secret, TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if _, ok := VerifyTOTP(secret, code, now, 0); !ok {
		t.Fatal("свежесгенерированный секрет не проверяется собственным кодом")
	}
}

func TestOTPAuthURIContainsSecretAndIssuer(t *testing.T) {
	uri := OTPAuthURI("OneBase — Торговля", "ivan", rfcSecret)
	for _, want := range []string{"otpauth://totp/", "secret=" + rfcSecret, "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("в ссылке %q нет %q", uri, want)
		}
	}
}
