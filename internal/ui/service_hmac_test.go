package ui

// INT-01 / issue #785: подпись generic HTTP-сервиса — freshness + replay.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signV1(secret, ts, method, path string, body []byte) string {
	h := sha256.Sum256(body)
	canonical := "v1:" + ts + ":" + strings.ToUpper(method) + ":" + path + ":" + hex.EncodeToString(h[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyServiceHMAC_OldSchemeCompat(t *testing.T) {
	secret, body := "s3cr3t", []byte(`{"a":1}`)
	r := httptest.NewRequest("POST", "/hs/svc/x", nil)
	r.Header.Set("X-Webhook-Signature", signBody(secret, body))
	if ok, reason := verifyServiceHMAC(secret, "svc", r, body, newReplayCache(5*time.Minute)); !ok {
		t.Fatalf("старая схема (подпись тела) должна проходить: %s", reason)
	}
}

func TestVerifyServiceHMAC_V1AndReplay(t *testing.T) {
	secret, body := "s3cr3t", []byte(`{"a":1}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	cache := newReplayCache(5 * time.Minute)
	newReq := func() *http.Request {
		r := httptest.NewRequest("POST", "/hs/svc/x", nil)
		r.Header.Set("X-Webhook-Timestamp", ts)
		r.Header.Set("X-Webhook-Signature", signV1(secret, ts, "POST", "/hs/svc/x", body))
		return r
	}
	if ok, reason := verifyServiceHMAC(secret, "svc", newReq(), body, cache); !ok {
		t.Fatalf("v1-подпись должна проходить: %s", reason)
	}
	if ok, reason := verifyServiceHMAC(secret, "svc", newReq(), body, cache); ok {
		t.Fatal("повтор той же v1-подписи должен отклоняться (replay)")
	} else if !strings.Contains(reason, "replay") {
		t.Fatalf("ожидалась причина replay, получено %q", reason)
	}
}

func TestVerifyServiceHMAC_StaleTimestamp(t *testing.T) {
	secret, body := "s3cr3t", []byte(`{}`)
	oldTs := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	r := httptest.NewRequest("POST", "/hs/svc/x", nil)
	r.Header.Set("X-Webhook-Timestamp", oldTs)
	r.Header.Set("X-Webhook-Signature", signV1(secret, oldTs, "POST", "/hs/svc/x", body))
	if ok, reason := verifyServiceHMAC(secret, "svc", r, body, newReplayCache(5*time.Minute)); ok {
		t.Fatal("устаревшая метка времени должна отклоняться")
	} else if !strings.Contains(reason, "окн") {
		t.Fatalf("ожидалась причина про окно свежести, получено %q", reason)
	}
}

func TestVerifyServiceHMAC_TamperedPath(t *testing.T) {
	secret, body := "s3cr3t", []byte(`{}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	// Подпись для одного пути, запрос на другой — v1 связывает путь → отказ.
	r := httptest.NewRequest("POST", "/hs/svc/OTHER", nil)
	r.Header.Set("X-Webhook-Timestamp", ts)
	r.Header.Set("X-Webhook-Signature", signV1(secret, ts, "POST", "/hs/svc/x", body))
	if ok, _ := verifyServiceHMAC(secret, "svc", r, body, newReplayCache(5*time.Minute)); ok {
		t.Fatal("подпись для другого пути должна отклоняться")
	}
}

func TestVerifyServiceHMAC_TamperedQuery(t *testing.T) {
	secret, body := "s3cr3t", []byte(`{}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r := httptest.NewRequest("POST", "/hs/svc/x?account=B", nil)
	r.Header.Set("X-Webhook-Timestamp", ts)
	r.Header.Set("X-Webhook-Signature", signV1(secret, ts, "POST", "/hs/svc/x?account=A", body))
	if ok, _ := verifyServiceHMAC(secret, "svc", r, body, newReplayCache(5*time.Minute)); ok {
		t.Fatal("подпись для другой query-строки должна отклоняться")
	}
}

func TestReplayCache_FailsClosedAtLimit(t *testing.T) {
	cache := newReplayCacheWithLimit(time.Minute, 1)
	now := time.Now()
	if replay, saturated := cache.seenBefore("first", now); replay || saturated {
		t.Fatalf("первая подпись отклонена: replay=%v saturated=%v", replay, saturated)
	}
	if replay, saturated := cache.seenBefore("second", now); replay || !saturated {
		t.Fatalf("переполненный кэш не отказал fail-closed: replay=%v saturated=%v", replay, saturated)
	}
	if len(cache.seen) != 1 {
		t.Fatalf("размер кэша = %d, ожидался жёсткий предел 1", len(cache.seen))
	}
}
