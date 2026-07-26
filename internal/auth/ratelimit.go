package auth

// Rate-limiting логина (план 53, этап 2). Брутфорс пароля ограничивается
// in-memory лимитером по ключу (IP, login): после maxFails неудач в окне —
// блокировка до конца окна. Без внешних зависимостей; состояние теряется при
// рестарте процесса — для защиты от перебора этого достаточно.

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginBucket struct {
	fails        int
	windowStart  time.Time
	blockedUntil time.Time
}

type LoginLimiter struct {
	mu       sync.Mutex
	maxFails int
	window   time.Duration
	attempts map[string]*loginBucket
}

func NewLoginLimiter(maxFails int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{maxFails: maxFails, window: window, attempts: make(map[string]*loginBucket)}
}

// Allow сообщает, разрешена ли попытка входа для ключа. При блокировке
// возвращает время до следующей разрешённой попытки.
func (l *LoginLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	now := time.Now()
	if now.Before(b.blockedUntil) {
		return false, time.Until(b.blockedUntil)
	}
	return true, 0
}

// Fail регистрирует неудачную попытку входа.
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.attempts[key]
	if !ok || now.Sub(b.windowStart) > l.window {
		b = &loginBucket{windowStart: now}
		l.attempts[key] = b
	}
	b.fails++
	if b.fails >= l.maxFails {
		b.blockedUntil = now.Add(l.window)
		b.fails = 0
		b.windowStart = now
	}
	l.purgeLocked(now)
}

// Reset сбрасывает счётчик для ключа (вызывается при успешном входе).
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// purgeLocked лениво вычищает неактуальные записи, чтобы map не рос бесконечно.
func (l *LoginLimiter) purgeLocked(now time.Time) {
	if len(l.attempts) < 10000 {
		return
	}
	for k, b := range l.attempts {
		if now.After(b.blockedUntil) && now.Sub(b.windowStart) > l.window {
			delete(l.attempts, k)
		}
	}
}

// LoginKey строит ключ лимитера (IP, login). X-Forwarded-For намеренно не
// используется: без доверенного прокси заголовок подделывается и позволил бы
// обходить лимит (или блокировать чужие IP).
func LoginKey(r *http.Request, login string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host + "|" + strings.ToLower(strings.TrimSpace(login))
}
