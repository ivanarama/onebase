package provenance_test

import (
	"github.com/ivantit66/onebase/internal/provenance"
	left "github.com/ivantit66/onebase/internal/provenance/testtypes/left"
	right "github.com/ivantit66/onebase/internal/provenance/testtypes/right"
	"testing"
)

func TestCompositeImportPathIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b any
	}{
		{"nil_pointer", (*left.Snapshot)(nil), (*right.Snapshot)(nil)},
		{"named_bytes", []left.Byte{1}, []right.Byte{1}},
		{"nil_named_bytes", []left.Byte(nil), []right.Byte(nil)},
		{"empty_array", [0]left.Byte{}, [0]right.Byte{}},
		{"nested_map", map[string][]left.Byte{"x": {}}, map[string][]right.Byte{"x": {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, errA := provenance.TypedDigest(tc.a)
			b, errB := provenance.TypedDigest(tc.b)
			if errA != nil || errB != nil || a == b {
				t.Fatal("different import path lost from composite type identity")
			}
		})
	}
}
func TestInputDeclaredTypeWhitelistEvenWhenEmpty(t *testing.T) {
	for _, value := range []any{[]left.Byte(nil), []right.Byte{}, [0]left.Byte{}, map[string]left.Byte{}, map[left.Byte]string{}, []func(){}, map[string]func(){}, [0]func(){}} {
		if _, err := provenance.InputDigest(value); err == nil {
			t.Fatalf("unknown declared empty type %T entered strict inputs", value)
		}
	}
	for _, value := range []any{[]any{}, map[string]any{}, []string(nil), [0]string{}, map[string][]int{}} {
		if _, err := provenance.InputDigest(value); err != nil {
			t.Fatalf("known empty type %T rejected", value)
		}
	}
}

type genericDigestBox[T any] struct{ Value T }

func TestUnprovedGenericTypeIdentityStops(t *testing.T) {
	if _, err := provenance.TypedDigest((*genericDigestBox[left.Byte])(nil)); err == nil {
		t.Fatal("unproved instantiated generic identity accepted")
	}
}
