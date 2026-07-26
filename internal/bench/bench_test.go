package bench

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestPercentile_NearestRank(t *testing.T) {
	s := []time.Duration{10, 20, 30, 40, 50}
	cases := []struct {
		p    float64
		want time.Duration
	}{
		{0.50, 30},
		{0.95, 50},
		{0.99, 50},
		{1.0, 50},
	}
	for _, c := range cases {
		if got := percentile(s, c.p); got != c.want {
			t.Errorf("percentile(%.2f)=%d, want %d", c.p, got, c.want)
		}
	}
	if percentile(nil, 0.5) != 0 {
		t.Error("percentile(nil) должен быть 0")
	}
}

func TestApdex(t *testing.T) {
	t0 := 200 * time.Millisecond
	lat := []time.Duration{
		100 * time.Millisecond, // satisfied (<=T)
		200 * time.Millisecond, // satisfied (==T)
		300 * time.Millisecond, // tolerated (<=4T=800ms)
		900 * time.Millisecond, // frustrated (>4T)
	}
	// (2 + 1/2) / 4 = 0.625
	if got := apdex(lat, t0); got != 0.625 {
		t.Errorf("apdex=%v, want 0.625", got)
	}
	if apdex(lat, 0) != 0 {
		t.Error("apdex при T=0 должен быть 0")
	}
}

func TestHarness_RunSmoke(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	h, err := NewHarness(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.Run(ctx, Options{
		Threads: 2,
		Count:   50, // детерминированный объём вместо длительности
		Warmup:  0,
		ApdexT:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// В count-режиме суммарно выполняется ровно Count операций.
	if res.Ops+res.Errors != 50 {
		t.Errorf("Ops+Errors=%d, ожидалось 50", res.Ops+res.Errors)
	}
	if res.Errors != 0 {
		t.Errorf("неожиданные ошибки: %d", res.Errors)
	}
	if res.Throughput <= 0 {
		t.Error("throughput должен быть > 0")
	}
	if res.P50 > res.P95 || res.P95 > res.P99 {
		t.Errorf("перцентили не упорядочены: p50=%v p95=%v p99=%v", res.P50, res.P95, res.P99)
	}

	// Документы реально записаны и проведены — проверяем движения регистра.
	var movements int
	table := metadata.RegisterTableName("ОстаткиТоваров")
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&movements); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if movements != res.Ops {
		t.Errorf("движений регистра=%d, ожидалось %d (по числу проведённых)", movements, res.Ops)
	}
}
