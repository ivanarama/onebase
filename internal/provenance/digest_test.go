package provenance_test

import (
	"database/sql/driver"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/provenance"
	"github.com/shopspring/decimal"
)

type fakeJSON struct{ Value string }

func (fakeJSON) MarshalJSON() ([]byte, error) { panic("MarshalJSON must not be called") }

type driverValue struct{ Text string }

func (driverValue) Value() (driver.Value, error) { panic("Valuer must not be called") }

func TestTypedDigestSeparatesCollisions(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b any
	}{
		{"decimal_string", decimal.RequireFromString("1"), "1"},
		{"bytes_base64", []byte("x"), "eA=="},
		{"int_float", int64(1), float64(1)},
		{"nil_empty_bytes", []byte(nil), []byte{}},
		{"typed_untyped_nil", (*string)(nil), nil},
		{"nested", map[string]any{"v": []any{decimal.RequireFromString("1")}}, map[string]any{"v": []any{"1"}}},
		{"time_string", time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), "2026-11-16T12:00:00Z"},
		{"decimal_scale", decimal.New(1, 0), decimal.New(10, -1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := provenance.TypedDigest(tc.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := provenance.TypedDigest(tc.b)
			if err != nil || a == b {
				t.Fatal("typed values collapsed")
			}
		})
	}
}
func TestHostSnapshotAndCanonicalMapOrder(t *testing.T) {
	type predicate struct {
		Field, Op string
		Value     any
	}
	type field struct {
		Name, Type string
		PII        bool
	}
	type snapshot struct {
		Generation uint64
		Actor      string
		Roles      map[string][]string
		Fields     []field
		RLS        []predicate
		Params     map[string]any
	}
	s := snapshot{Generation: 1, Actor: "reader", Roles: map[string][]string{"reports": {"run"}}, Fields: []field{{"Phone", "string", true}}, RLS: []predicate{{"Owner", "eq", decimal.RequireFromString("1")}}, Params: map[string]any{"x": []byte("x"), "y": false}}
	a, err := provenance.TypedDigest(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Params = map[string]any{"y": false, "x": []byte("x")}
	b, err := provenance.TypedDigest(s)
	if err != nil || a != b {
		t.Fatal("map insertion order changed digest")
	}
	s.RLS[0].Value = "1"
	b, err = provenance.TypedDigest(s)
	if err != nil || a == b {
		t.Fatal("RLS value type change lost")
	}
}
func TestInputDigestStrictTypes(t *testing.T) {
	for _, value := range []any{nil, false, int64(1), float64(1), "1", []byte("x"), uuid.New(), decimal.RequireFromString("1.25"), time.Now(), map[string]any{"x": []any{1, "x", false}}} {
		if _, err := provenance.InputDigest(value); err != nil {
			t.Fatalf("known input type %T rejected", value)
		}
	}
	for _, value := range []any{fakeJSON{"x"}, driverValue{"x"}, func() {}, make(chan int), (*string)(nil), map[int]string{1: "x"}, math.Inf(1), math.NaN()} {
		if _, err := provenance.InputDigest(value); err == nil {
			t.Fatalf("unknown input type %T accepted", value)
		}
	}
}
func TestTypedDigestDoesNotInvokeHostMethods(t *testing.T) {
	for _, value := range []any{fakeJSON{"x"}, driverValue{"x"}} {
		if _, err := provenance.TypedDigest(value); err != nil {
			t.Fatal(err)
		}
	}
	type private struct{ secret string }
	for _, value := range []any{private{"not_for_error"}, func() {}, make(chan int)} {
		if _, err := provenance.TypedDigest(value); err == nil || err.Error() != provenance.ErrUnsupported.Error() {
			t.Fatal("host state accepted or data-dependent error")
		}
	}
}
func TestDigestBudgetsAndCycles(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := provenance.TypedDigest(cycle); err == nil {
		t.Fatal("cyclic snapshot accepted")
	}
	if _, err := provenance.InputDigest(cycle); err == nil {
		t.Fatal("cyclic input accepted")
	}
	var deep any = "x"
	for i := 0; i < 70; i++ {
		deep = []any{deep}
	}
	if _, err := provenance.TypedDigest(deep); err == nil {
		t.Fatal("snapshot depth budget not applied")
	}
	values := make([]any, 65537)
	if _, err := provenance.InputDigest(values); err == nil {
		t.Fatal("input item budget not applied")
	}
}
