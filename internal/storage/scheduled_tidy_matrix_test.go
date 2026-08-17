package storage_test

// Уборка журнала прогонов (#966): пометка брошенных и подрезка старых.
//
// Матричный тест обязателен: обе операции сравнивают время в WHERE, а время —
// ровно то место, где SQLite и PostgreSQL расходятся (TEXT против timestamptz).
// Раздельные тесты этого не показывают — правило CLAUDE.md, повод #607.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func addRun(t *testing.T, ctx context.Context, db *storage.DB, job string, startedAt time.Time, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.InsertScheduledRun(ctx, id, job, startedAt); err != nil {
		t.Fatalf("InsertScheduledRun: %v", err)
	}
	if status != "running" {
		if err := db.UpdateScheduledRun(ctx, id, status, "", "", 10); err != nil {
			t.Fatalf("UpdateScheduledRun: %v", err)
		}
	}
	return id
}

func statusOf(t *testing.T, ctx context.Context, db *storage.DB, id uuid.UUID) (string, bool) {
	t.Helper()
	run, err := db.ScheduledRunByID(ctx, id)
	if err != nil {
		t.Fatalf("ScheduledRunByID: %v", err)
	}
	if run == nil {
		return "", false
	}
	return run.Status, true
}

// Прогон, оставшийся в running после жёсткого завершения процесса, помечается
// прерванным. Без этого он висит вечно: терминальный статус проставляет
// единственный UPDATE в конце исполнения, которого не случилось.
func TestSweepStaleScheduledRuns_БрошенныйПрогонПомечается(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		old := addRun(t, ctx, db, "Обмен", time.Now().Add(-48*time.Hour), "running")

		n, err := db.SweepStaleScheduledRuns(ctx)
		if err != nil {
			t.Fatalf("SweepStaleScheduledRuns: %v", err)
		}
		if n != 1 {
			t.Fatalf("помечено %d прогонов, ожидался 1", n)
		}
		if st, _ := statusOf(t, ctx, db, old); st != "interrupted" {
			t.Fatalf("статус брошенного прогона = %q, ожидался interrupted", st)
		}
	})
}

// Свежий running не трогаем. Это не педантизм: single-flight планировщика живёт
// в памяти процесса, и при нескольких инстансах на одной базе агрессивная
// уборка пометила бы «прерванным» живой прогон соседа.
func TestSweepStaleScheduledRuns_ЖивойПрогонНеТрогается(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fresh := addRun(t, ctx, db, "Обмен", time.Now().Add(-time.Minute), "running")

		if _, err := db.SweepStaleScheduledRuns(ctx); err != nil {
			t.Fatalf("SweepStaleScheduledRuns: %v", err)
		}
		if st, _ := statusOf(t, ctx, db, fresh); st != "running" {
			t.Fatalf("свежий прогон помечен как %q — сосед по базе потерял бы свой прогон", st)
		}
	})
}

func TestPruneScheduledRuns_СтарыеУдаляютсяСвежиеОстаются(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		veryOld := addRun(t, ctx, db, "Обмен", time.Now().Add(-100*24*time.Hour), "success")
		recent := addRun(t, ctx, db, "Обмен", time.Now().Add(-24*time.Hour), "success")

		n, err := db.PruneScheduledRuns(ctx, 90*24*time.Hour)
		if err != nil {
			t.Fatalf("PruneScheduledRuns: %v", err)
		}
		if n != 1 {
			t.Fatalf("удалено %d прогонов, ожидался 1", n)
		}
		if _, ok := statusOf(t, ctx, db, veryOld); ok {
			t.Error("старый прогон не удалён")
		}
		if _, ok := statusOf(t, ctx, db, recent); !ok {
			t.Error("свежий прогон удалён, хотя не должен был")
		}
	})
}

// Незавершённый прогон уборка не удаляет, даже если он старый: его судьба —
// дело sweep, а удалять running значит терять след работающего задания.
func TestPruneScheduledRuns_НезавершённыйНеУдаляется(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		stuck := addRun(t, ctx, db, "Обмен", time.Now().Add(-100*24*time.Hour), "running")

		if _, err := db.PruneScheduledRuns(ctx, 90*24*time.Hour); err != nil {
			t.Fatalf("PruneScheduledRuns: %v", err)
		}
		if _, ok := statusOf(t, ctx, db, stuck); !ok {
			t.Fatal("незавершённый прогон удалён — след работающего задания потерян")
		}
	})
}

// Нулевой срок отключает уборку целиком: это способ сказать «ничего не удалять»,
// и он не должен молча означать «удалить всё».
func TestPruneScheduledRuns_НулевойСрокНичегоНеУдаляет(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		old := addRun(t, ctx, db, "Обмен", time.Now().Add(-1000*24*time.Hour), "success")

		n, err := db.PruneScheduledRuns(ctx, 0)
		if err != nil {
			t.Fatalf("PruneScheduledRuns: %v", err)
		}
		if n != 0 {
			t.Fatalf("удалено %d прогонов при отключённой уборке", n)
		}
		if _, ok := statusOf(t, ctx, db, old); !ok {
			t.Fatal("прогон удалён при нулевом сроке")
		}
	})
}
