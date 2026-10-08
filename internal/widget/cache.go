package widget

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/shopspring/decimal"
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

func cacheKey(widgetName, user, security, params string) string {
	// NUL cannot occur in widget names or logins and cannot occur in the hex
	// fingerprints, so unlike a printable delimiter it is unambiguous.
	return widgetName + "\x00" + user + "\x00" + security + "\x00" + params
}

// paramsFingerprint returns a stable, non-reversible identity of all effective
// query parameters. Values are tagged by their semantic type so nil, false, 0
// and "" can never share a cache entry. Unsupported compound/host values turn
// caching off for the run instead of risking an incomplete key.
func paramsFingerprint(params map[string]any) (string, bool) {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var canonical bytes.Buffer
	for _, key := range keys {
		writeSized(&canonical, "k", key)
		if !writeCanonicalParam(&canonical, params[key]) {
			return "", false
		}
	}
	sum := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(sum[:]), true
}

func writeSized(dst *bytes.Buffer, tag, value string) {
	dst.WriteString(tag)
	dst.WriteByte(':')
	dst.WriteString(strconv.Itoa(len(value)))
	dst.WriteByte(':')
	dst.WriteString(value)
	dst.WriteByte(';')
}

func writeCanonicalParam(dst *bytes.Buffer, value any) bool {
	if value == nil {
		dst.WriteString("nil;")
		return true
	}
	switch v := value.(type) {
	case string:
		writeSized(dst, "string", v)
	case bool:
		dst.WriteString("bool:")
		dst.WriteString(strconv.FormatBool(v))
		dst.WriteByte(';')
	case uuid.UUID:
		dst.WriteString("uuid:")
		dst.WriteString(v.String())
		dst.WriteByte(';')
	case time.Time:
		dst.WriteString("time:")
		dst.WriteString(v.UTC().Format(time.RFC3339Nano))
		dst.WriteByte(';')
	case decimal.Decimal:
		dst.WriteString("decimal:")
		dst.WriteString(v.String())
		dst.WriteByte(';')
	case int:
		writeSigned(dst, int64(v))
	case int8:
		writeSigned(dst, int64(v))
	case int16:
		writeSigned(dst, int64(v))
	case int32:
		writeSigned(dst, int64(v))
	case int64:
		writeSigned(dst, v)
	case uint:
		writeUnsigned(dst, uint64(v))
	case uint8:
		writeUnsigned(dst, uint64(v))
	case uint16:
		writeUnsigned(dst, uint64(v))
	case uint32:
		writeUnsigned(dst, uint64(v))
	case uint64:
		writeUnsigned(dst, v)
	case float32:
		return writeFloat(dst, float64(v), 32)
	case float64:
		return writeFloat(dst, v, 64)
	default:
		// A typed nil pointer/interface is nil in meaning, but every non-nil
		// pointer or container is deliberately unsupported: its host encoding
		// could be unstable or omit authorization-relevant state.
		rv := reflect.ValueOf(value)
		if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) && rv.IsNil() {
			dst.WriteString("nil;")
			return true
		}
		return false
	}
	return true
}

func writeSigned(dst *bytes.Buffer, value int64) {
	dst.WriteString("int:")
	dst.WriteString(strconv.FormatInt(value, 10))
	dst.WriteByte(';')
}

func writeUnsigned(dst *bytes.Buffer, value uint64) {
	dst.WriteString("uint:")
	dst.WriteString(strconv.FormatUint(value, 10))
	dst.WriteByte(';')
}

func writeFloat(dst *bytes.Buffer, value float64, bits int) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	_, _ = fmt.Fprintf(dst, "float%d:", bits)
	dst.WriteString(strconv.FormatFloat(value, 'g', -1, bits))
	dst.WriteByte(';')
	return true
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
