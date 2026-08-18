package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Административные решения о включённости заданий переезжают в .obz (#991).
//
// Копия базы должна вести задания так же, как источник: без этого
// восстановленный из бэкапа «выключенный автобэкап» молча оживает, а
// выключенный на исходной базе поллинг вдруг стартует на копии. Решения
// переезжают безусловно — в любом режиме восстановления: состояние заданий
// не идентичность базы (ср. exchange.this_node, который едет только при
// полном восстановлении).

func TestUniversalScheduledToggleRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "sched-src")
	if err := src.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveScheduledEnabled(ctx, "Автобэкап", false); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveScheduledEnabled(ctx, "ТелеграмПоллинг", true); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", testConfigDir(t), "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	// В архиве — оба решения.
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmpDir, "settings", "safe.jsonl"))
	if err != nil {
		t.Fatalf("safe settings missing: %v", err)
	}
	for _, key := range []string{"scheduled.enabled.автобэкап", "scheduled.enabled.телеграмполлинг"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatalf("решение %s не попало в архив:\n%s", key, raw)
		}
	}

	// Целевая база со СВОИМ решением по тому же заданию: импорт обязан
	// заменить его решением источника, а не оставить старое.
	dst := newSQLite(t, "sched-dst")
	if err := dst.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.SaveScheduledEnabled(ctx, "Автобэкап", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportUniversal(ctx, dst, "file", t.TempDir(), "", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}

	if on, ok, _ := dst.GetScheduledEnabled(ctx, "Автобэкап"); !ok || on {
		t.Fatalf("решение по Автобэкапу после импорта: on=%v ok=%v, ожидалось выключено источником", on, ok)
	}
	if on, ok, _ := dst.GetScheduledEnabled(ctx, "ТелеграмПоллинг"); !ok || !on {
		t.Fatalf("решение по ТелеграмПоллингу после импорта: on=%v ok=%v, ожидалось включено источником", on, ok)
	}
}
