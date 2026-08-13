package ui

// Проверка подписи HMAC для generic HTTP-сервисов и защита от повторов (#785).
//
// Старая схема подписывала только тело (X-Webhook-Signature =
// hex(HMAC-SHA256(тело, secret))): подпись не связывала запрос с методом,
// путём и временем, поэтому перехваченный запрос можно было воспроизвести. Новая
// (versioned) схема — opt-in — подписывает timestamp+method+path+хэш тела,
// требует свежую метку времени и отклоняет повтор той же подписи. Старая схема
// принимается для совместимости, но без freshness/replay — клиентам стоит
// перейти на v1.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hmacFreshnessWindow — допустимое расхождение X-Webhook-Timestamp с текущим
// временем. Запрос за пределами окна отклоняется; его подпись столько же
// хранится в кэше повторов.
const hmacFreshnessWindow = 5 * time.Minute

// serviceReplay — общий кэш повторов подписей generic HTTP-сервисов.
var serviceReplay = newReplayCache(hmacFreshnessWindow)

type replayCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	seen      map[string]time.Time
	lastPurge time.Time
}

func newReplayCache(ttl time.Duration) *replayCache {
	return &replayCache{ttl: ttl, seen: map[string]time.Time{}}
}

// seenBefore возвращает true, если ключ уже встречался в пределах TTL (повтор);
// иначе запоминает ключ и возвращает false.
func (c *replayCache) seenBefore(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked(now)
	if t, ok := c.seen[key]; ok && now.Sub(t) <= c.ttl {
		return true
	}
	c.seen[key] = now
	return false
}

// purgeLocked чистит протухшие записи не чаще раза за TTL — чтобы не сканировать
// map под мьютексом на каждый запрос.
func (c *replayCache) purgeLocked(now time.Time) {
	if !c.lastPurge.IsZero() && now.Sub(c.lastPurge) < c.ttl {
		return
	}
	c.lastPurge = now
	for k, t := range c.seen {
		if now.Sub(t) > c.ttl {
			delete(c.seen, k)
		}
	}
}

// verifyServiceHMAC проверяет подпись запроса к generic HTTP-сервису.
// Возвращает (true, "") при успехе или (false, причина) при отказе.
func verifyServiceHMAC(secret, svcName string, r *http.Request, body []byte, cache *replayCache) (ok bool, reason string) {
	sig := strings.TrimSpace(r.Header.Get("X-Webhook-Signature"))
	tsRaw := strings.TrimSpace(r.Header.Get("X-Webhook-Timestamp"))
	lowSig := strings.ToLower(sig)

	// Новая схема: включается заголовком X-Webhook-Timestamp или префиксом v1=.
	if tsRaw != "" || strings.HasPrefix(lowSig, "v1=") {
		ts, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil {
			return false, "неверная или отсутствующая метка времени"
		}
		now := time.Now()
		window := int64(cache.ttl.Seconds())
		if d := now.Unix() - ts; d > window || d < -window {
			return false, "метка времени вне окна свежести"
		}
		got := strings.TrimPrefix(lowSig, "v1=")
		bodyHash := sha256.Sum256(body)
		canonical := "v1:" + tsRaw + ":" + strings.ToUpper(r.Method) + ":" + r.URL.Path + ":" + hex.EncodeToString(bodyHash[:])
		if !hmacEqualHex(secret, []byte(canonical), got) {
			return false, "неверная подпись"
		}
		if cache.seenBefore(svcName+"|"+got, now) {
			return false, "повтор запроса (replay)"
		}
		return true, ""
	}

	// Старая схема (совместимость): подпись только тела, без freshness/replay.
	got := strings.TrimPrefix(lowSig, "sha256=")
	if !hmacEqualHex(secret, body, got) {
		return false, "неверная подпись"
	}
	return true, ""
}

// hmacEqualHex сравнивает hex-подпись gotHex с HMAC-SHA256(msg, secret)
// constant-time.
func hmacEqualHex(secret string, msg []byte, gotHex string) bool {
	if gotHex == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(msg)
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(gotHex), []byte(want)) == 1
}
