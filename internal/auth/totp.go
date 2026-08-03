package auth

// TOTP (RFC 6238) — второй фактор входа (план 84). Реализация на stdlib:
// алгоритм — HMAC-SHA1 от номера 30-секундного шага, шесть цифр; ровно то, что
// умеют Google Authenticator, Aegis, 1Password и совместимые. Внешняя
// библиотека сюда не заводится: весь алгоритм — три десятка строк, а
// зависимость в контуре аутентификации пришлось бы отдельно сопровождать под
// vuln-скан.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 здесь не криптостойкость, а требование RFC 6238 — другой хэш не поймёт ни одно приложение-аутентификатор
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// totpPeriod — длина шага в секундах. 30 — значение по умолчанию во всех
	// приложениях-аутентификаторах; менять его нельзя, не сломав совместимость.
	totpPeriod = 30
	// totpDigits — длина кода.
	totpDigits = 6
	// totpSecretBytes — длина секрета. RFC 4226 требует не меньше 128 бит и
	// рекомендует 160 — ровно размер блока HMAC-SHA1.
	totpSecretBytes = 20
	// totpSkew — сколько соседних шагов принимается кроме текущего. Один шаг в
	// каждую сторону — компромисс между расхождением часов телефона и окном для
	// подбора: кодов всё равно 10^6, а попытки ограничены лимитером входа.
	totpSkew = 1
)

// totpEncoding — base32 без выравнивания: именно в таком виде секрет печатают
// в otpauth-ссылке и вводят руками, когда QR отсканировать нечем.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret возвращает новый секрет в base32 (то, что показывается
// пользователю и кладётся в otpauth://-ссылку).
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: totp secret: %w", err)
	}
	return totpEncoding.EncodeToString(buf), nil
}

// NormalizeTOTPSecret приводит введённый вручную секрет к каноническому виду:
// без пробелов, в верхнем регистре, без выравнивающих «=».
func NormalizeTOTPSecret(secret string) string {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return strings.TrimRight(s, "=")
}

// TOTPStep — номер шага, которому принадлежит момент времени. Он же служит
// защитой от повторного предъявления кода: шаг использованного кода
// запоминается, и код того же (или более раннего) шага второй раз не проходит.
func TOTPStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// TOTPCode вычисляет код для заданного шага.
func TOTPCode(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(NormalizeTOTPSecret(secret))
	if err != nil {
		return "", fmt.Errorf("auth: totp secret: %w", err)
	}
	if len(key) == 0 {
		return "", fmt.Errorf("auth: totp secret: пустой секрет")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step)) //nolint:gosec // G115: step — номер 30-секундного шага от эпохи, отрицательным быть не может
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// VerifyTOTP проверяет код на момент now с допуском ±totpSkew шагов и
// возвращает номер шага, которому код принадлежит. minStep отсекает уже
// использованные шаги: код, предъявленный второй раз (или код более раннего
// шага), не принимается — иначе подсмотренный код работал бы все 30 секунд у
// того, кто успел его перехватить.
func VerifyTOTP(secret, code string, now time.Time, minStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	current := TOTPStep(now)
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		step := current + int64(delta)
		if step < minStep {
			continue
		}
		want, err := TOTPCode(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// OTPAuthURI собирает otpauth://-ссылку для QR-кода. issuer попадает и в путь,
// и в параметр — так требует де-факто стандарт (Key URI Format): приложения
// читают разные его части.
func OTPAuthURI(issuer, account, secret string) string {
	if strings.TrimSpace(issuer) == "" {
		issuer = "OneBase"
	}
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", NormalizeTOTPSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// FormatTOTPSecret разбивает секрет на группы по 4 символа — так его реально
// ввести руками, когда камеры под рукой нет.
func FormatTOTPSecret(secret string) string {
	s := NormalizeTOTPSecret(secret)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(ch)
	}
	return b.String()
}
