package widget

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/shopspring/decimal"
)

func TestCache_GetPut(t *testing.T) {
	c := NewCache(time.Minute)
	if _, ok := c.get("missing"); ok {
		t.Fatal("empty cache returned hit")
	}
	c.put("a", Result{Name: "a", Title: "T"})
	got, ok := c.get("a")
	if !ok || got.Name != "a" {
		t.Fatalf("get after put: ok=%v name=%q", ok, got.Name)
	}
}

func TestCache_Expiry(t *testing.T) {
	c := NewCache(50 * time.Millisecond)
	c.put("k", Result{Name: "k"})
	if _, ok := c.get("k"); !ok {
		t.Fatal("fresh entry should be cached")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.get("k"); ok {
		t.Fatal("expired entry should be evicted")
	}
}

func TestCache_Invalidate(t *testing.T) {
	c := NewCache(time.Minute)
	c.put("a", Result{Name: "a"})
	c.put("b", Result{Name: "b"})
	c.Invalidate()
	if _, ok := c.get("a"); ok {
		t.Fatal("Invalidate did not drop entries")
	}
}

func TestCache_NilSafe(t *testing.T) {
	var c *Cache
	if _, ok := c.get("x"); ok {
		t.Fatal("nil cache should miss")
	}
	c.put("x", Result{})
	c.Invalidate()
}

func TestCacheKey(t *testing.T) {
	first := cacheKey("A", "u1", "s", "p1")
	if first == cacheKey("A", "u2", "s", "p1") {
		t.Fatal("different users must produce different keys")
	}
	if first == cacheKey("A", "u1", "s", "p2") {
		t.Fatal("different effective params must produce different keys")
	}
	second := cacheKey("A", "u1", "s", "p1")
	if first != second {
		t.Fatal("same inputs must produce same key")
	}
}

func TestParamsFingerprintCanonicalAndTypeAware(t *testing.T) {
	tm := time.Date(2026, 9, 22, 12, 34, 56, 123, time.FixedZone("MSK", 3*60*60))
	id := uuid.MustParse("2ce0d050-1db3-4dcb-a30c-50eb237c3c2b")
	one, ok := paramsFingerprint(map[string]any{
		"nil": nil, "false": false, "zero": int64(0), "empty": "",
		"uuid": id, "time": tm, "decimal": decimal.RequireFromString("10.500"),
	})
	if !ok {
		t.Fatal("supported typed params unexpectedly disabled cache")
	}
	two, ok := paramsFingerprint(map[string]any{
		"decimal": decimal.RequireFromString("10.5"), "time": tm.UTC(), "uuid": id,
		"empty": "", "zero": int64(0), "false": false, "nil": nil,
	})
	if !ok || one != two {
		t.Fatalf("map order/canonical scalar representation changed fingerprint: %q != %q", one, two)
	}

	distinct := []map[string]any{{"v": nil}, {"v": false}, {"v": int64(0)}, {"v": ""}}
	seen := make(map[string]bool, len(distinct))
	for _, params := range distinct {
		fp, cacheable := paramsFingerprint(params)
		if !cacheable {
			t.Fatalf("ordinary scalar disabled cache: %#v", params)
		}
		if seen[fp] {
			t.Fatalf("typed values collapsed to one fingerprint: %#v", distinct)
		}
		seen[fp] = true
	}
}

func TestParamsFingerprintUnsupportedDisablesCache(t *testing.T) {
	for _, value := range []any{[]string{"x"}, map[string]string{"x": "y"}, make(chan int), math.NaN()} {
		if _, ok := paramsFingerprint(map[string]any{"v": value}); ok {
			t.Fatalf("unsupported value must disable cache: %T", value)
		}
	}
}

func TestSecurityFingerprintChangesWithPermissions(t *testing.T) {
	user := &auth.User{Login: "u", Roles: []*auth.Role{{Name: "reader", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Товар": {"read"}},
	}}}}
	one, ok := securityFingerprint(user)
	if !ok {
		t.Fatal("ordinary auth state must be cacheable")
	}
	user.Roles[0].Permissions.Catalogs["Товар"] = []string{"read", "write"}
	two, ok := securityFingerprint(user)
	if !ok || one == two {
		t.Fatal("permission change must alter fingerprint")
	}
	access.SetMaskAdmin(true)
	defer access.SetMaskAdmin(false)
	three, ok := securityFingerprint(user)
	if !ok || two == three {
		t.Fatal("mask_admin change must alter fingerprint")
	}

	user.Attrs = map[string]any{"unsupported": make(chan int)}
	if _, ok := securityFingerprint(user); ok {
		t.Fatal("unsupported attributes must disable caching")
	}
}
