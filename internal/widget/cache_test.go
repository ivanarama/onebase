package widget

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
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
	first := cacheKey("A", "u1", "s")
	if first == cacheKey("A", "u2", "s") {
		t.Fatal("different users must produce different keys")
	}
	second := cacheKey("A", "u1", "s")
	if first != second {
		t.Fatal("same inputs must produce same key")
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
