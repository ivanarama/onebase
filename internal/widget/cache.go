package widget

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
)

// Cache stores widget execution results for a fixed TTL. Dashboard requests
// are bursty (every reload re-runs all widgets), so even a 60-second window
// drastically cuts query pressure when the user navigates around the app.
//
// The cache is intentionally simple: no LRU, no size cap. Widgets are
// short-lived, dashboards rarely host more than a few dozen entries, and the
// per-base process is the natural memory boundary.
type Cache struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]cacheEntry
}

type cacheEntry struct {
	result    Result
	expiresAt time.Time
}

// NewCache creates a cache with the given TTL. Pass zero to disable expiry
// (results live until Invalidate or process exit).
func NewCache(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl, m: make(map[string]cacheEntry)}
}

func (c *Cache) get(key string) (Result, bool) {
	if c == nil {
		return Result{}, false
	}
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok {
		return Result{}, false
	}
	if c.ttl > 0 && time.Now().After(e.expiresAt) {
		// Lazy eviction — fine for small caches, avoids a background goroutine.
		c.mu.Lock()
		delete(c.m, key)
		c.mu.Unlock()
		return Result{}, false
	}
	return e.result, true
}

func (c *Cache) put(key string, r Result) {
	if c == nil {
		return
	}
	exp := time.Time{}
	if c.ttl > 0 {
		exp = time.Now().Add(c.ttl)
	}
	c.mu.Lock()
	c.m[key] = cacheEntry{result: r, expiresAt: exp}
	c.mu.Unlock()
}

// Invalidate drops every entry. Use after a configuration reload so users
// don't see stale widgets.
func (c *Cache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.m = make(map[string]cacheEntry)
	c.mu.Unlock()
}

func cacheKey(widgetName, user, security string) string {
	// NUL cannot occur in widget names or logins and cannot occur in the hex
	// security fingerprint, so unlike a printable delimiter it is unambiguous.
	return widgetName + "\x00" + user + "\x00" + security
}

// securityFingerprint makes cached output follow the complete authorization
// state, not merely a login. Role/row/field-policy changes therefore produce a
// cache miss immediately. Unsupported host attributes disable caching rather
// than risking reuse under an incomplete fingerprint.
func securityFingerprint(user *auth.User) (string, bool) {
	payload := struct {
		IsAdmin   bool
		Attrs     map[string]any
		Roles     []*auth.Role
		MaskAdmin bool
	}{MaskAdmin: access.MaskAdmin()}
	if user != nil {
		payload.IsAdmin = user.IsAdmin
		payload.Attrs = user.Attrs
		payload.Roles = user.Roles
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), true
}
