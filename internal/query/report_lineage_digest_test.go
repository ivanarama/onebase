package query

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

type lineageFakeJSON struct{ Value string }

func (lineageFakeJSON) MarshalJSON() ([]byte, error) {
	panic("receipt must never invoke arbitrary MarshalJSON")
}

func TestReportLineageTypedDigestDistinguishesJSONCollisions(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b any
	}{
		{"decimal_string", decimal.RequireFromString("1"), "1"},
		{"bytes_base64", []byte("A"), "QQ=="},
		{"int_float", int64(1), float64(1)},
		{"nil_empty_bytes", []byte(nil), []byte{}},
		{"typed_untyped_nil", (*string)(nil), nil},
		{"nested", map[string]any{"x": []any{decimal.RequireFromString("1")}}, map[string]any{"x": []any{"1"}}},
		{"time_string", time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), "2026-11-16T12:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, okA := lineageTypedDigest(tc.a)
			b, okB := lineageTypedDigest(tc.b)
			if !okA || !okB || a == b {
				t.Fatal("typed values collapsed")
			}
		})
	}
	a, ok := lineageTypedDigest(map[string]any{"a": int64(1), "b": "2"})
	b, ok2 := lineageTypedDigest(map[string]any{"b": "2", "a": int64(1)})
	if !ok || !ok2 || a != b {
		t.Fatal("map iteration invalidated stable receipt")
	}
}
func TestReportLineageTypedEnvironmentAndUnknownArguments(t *testing.T) {
	opts := CompileOpts{Params: map[string]any{"x": decimal.RequireFromString("1")}, RowFilters: map[SourceRef]*storage.Predicate{{Kind: "document", Name: "D"}: {Field: "Owner", Op: "eq", Value: decimal.RequireFromString("1")}}}
	original, ok := lineageEnvironmentDigest(opts)
	if !ok {
		t.Fatal("known environment rejected")
	}
	opts.Params["x"] = "1"
	changed, ok := lineageEnvironmentDigest(opts)
	if !ok || original == changed {
		t.Fatal("parameter type change accepted")
	}
	opts.Params["x"] = decimal.RequireFromString("1")
	opts.RowFilters[SourceRef{Kind: "document", Name: "D"}].Value = "1"
	changed, ok = lineageEnvironmentDigest(opts)
	if !ok || original == changed {
		t.Fatal("RLS value type change accepted")
	}
	if lineageArgumentsSupported([]any{lineageFakeJSON{Value: "1"}}) {
		t.Fatal("arbitrary marshaler accepted as SQL input")
	}
	if _, ok := lineageTypedDigest(lineageFakeJSON{Value: "1"}); !ok {
		t.Fatal("plain exported state could not be inspected without invoking MarshalJSON")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if lineageArgumentsSupported([]any{cycle}) {
		t.Fatal("cyclic SQL argument accepted")
	}
	if _, ok := lineageTypedDigest(cycle); ok {
		t.Fatal("cyclic environment accepted")
	}
	if _, ok := lineageTypedDigest(func() {}); ok {
		t.Fatal("host callback fingerprinted as empty data")
	}
}
