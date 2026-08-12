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
	mu        sync.Mutex
	maxFails  int
	window    time.Duration
	attempts  map[string]*loginBucket
	lastPurge time.Time
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
//
// Полный проход идёт под общим мьютексом и стоит O(n). Раньше он выполнялся на
// КАЖДУЮ неудачную попытку, как только записей становилось ≥10000, — при флуде
// уникальными логинами с одного IP это давало квадратичное поведение и
// сериализовало все входы в процессе (issue #776). Теперь проход ограничен по
// частоте: не чаще одного раза за окно. Этого достаточно, чтобы вычищать
// протухшие записи, а пиковый размер map ограничен темпом флуда × длина окна.
func (l *LoginLimiter) purgeLocked(now time.Time) {
	if len(l.attempts) < 10000 {
		return
	}
	if !l.lastPurge.IsZero() && now.Sub(l.lastPurge) < l.window {
		return
	}
	l.lastPurge = now
	for k, b := range l.attempts {
		if now.After(b.blockedUntil) && now.Sub(b.windowStart) > l.window {
			delete(l.attempts, k)
		}
	}
}

// maxLoginKeyLen ограничивает длину логина в ключе лимитера: без него
// неаутентифицированный клиент раздувал бы записи гигантскими строками логина.
const maxLoginKeyLen = 256

// LoginKey строит ключ лимитера (IP, login). X-Forwarded-For намеренно не
// используется: без доверенного прокси заголовок подделывается и позволил бы
// обходить лимит (или блокировать чужие IP).
func LoginKey(r *http.Request, login string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	login = strings.ToLower(strings.TrimSpace(login))
	if len(login) > maxLoginKeyLen {
		login = login[:maxLoginKeyLen]
	}
	return host + "|" + login
}
