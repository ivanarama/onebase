package storage_test

// Чтение прогона регламентного задания по идентификатору (#742, план 123).
//
// Матричный тест, а не юнит на SQLite: колонки времени и NULL-поля читаются
// драйверами по-разному, и расхождение диалектов здесь проявилось бы уже в
// прикладном коде — из DSL этот прогон отдаётся Структурой. Правило про
// dbtest.ForEachDialect — из CLAUDE.md, повод описан в #607.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestScheduledRunByID_ЧитаетНачатыйИЗавершённыйПрогон(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		id := uuid.New()
		startedAt := time.Now().UTC().Truncate(time.Second)

		if err := db.InsertScheduledRun(ctx, id, "ОбменСУзлами", startedAt); err != nil {
			t.Fatalf("InsertScheduledRun: %v", err)
		}

		// Пока задание работает: строка уже есть, финиша ещё нет. Прикладной
		// код опрашивает прогон именно в этом состоянии.
		run, err := db.ScheduledRunByID(ctx, id)
		if err != nil {
			t.Fatalf("ScheduledRunByID: %v", err)
		}
		if run == nil {
			t.Fatal("прогон не найден сразу после вставки")
		}
		if run.ID != id || run.JobName != "ОбменСУзлами" {
			t.Fatalf("прочитан не тот прогон: %+v", run)
		}
		if run.Status != "running" {
			t.Errorf("статус = %q, ожидался running", run.Status)
		}
		if run.FinishedAt != nil {
			t.Errorf("у незавершённого прогона проставлено время финиша: %v", run.FinishedAt)
		}
		if !run.StartedAt.UTC().Truncate(time.Second).Equal(startedAt) {
			t.Errorf("время старта = %v, ожидалось %v", run.StartedAt.UTC(), startedAt)
		}

		if err := db.UpdateScheduledRun(ctx, id, "success", "обошёл 360 узлов", "", 1234); err != nil {
			t.Fatalf("UpdateScheduledRun: %v", err)
		}
		run, err = db.ScheduledRunByID(ctx, id)
		if err != nil {
			t.Fatalf("ScheduledRunByID после завершения: %v", err)
		}
		if run == nil {
			t.Fatal("завершённый прогон не найден")
		}
		if run.Status != "success" || run.Output != "обошёл 360 узлов" || run.DurationMs != 1234 {
			t.Fatalf("завершённый прогон прочитан неверно: %+v", run)
		}
		if run.FinishedAt == nil {
			t.Error("у завершённого прогона нет времени финиша")
		}
		if run.Error != "" {
			t.Errorf("ошибка непуста у успешного прогона: %q", run.Error)
		}
	})
}

// Чужой идентификатор — не ошибка, а «журнал такого не помнит»: запись могла
// быть подрезана ретенцией, а из DSL сюда прилетает произвольная строка.
func TestScheduledRunByID_НеизвестныйИдДаётNilБезОшибки(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		run, err := db.ScheduledRunByID(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("неизвестный id вернул ошибку: %v", err)
		}
		if run != nil {
			t.Fatalf("неизвестный id вернул прогон: %+v", run)
		}
	})
}

// Ошибка упавшего задания доезжает до читателя целиком — по ней прикладной код
// отличает «отработало» от «сломалось».
func TestScheduledRunByID_ОшибкаУпавшегоПрогонаЧитается(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		id := uuid.New()
		if err := db.InsertScheduledRun(ctx, id, "Сломанное", time.Now()); err != nil {
			t.Fatalf("InsertScheduledRun: %v", err)
		}
		if err := db.UpdateScheduledRun(ctx, id, "error", "", "узел N-217 не ответил", 42); err != nil {
			t.Fatalf("UpdateScheduledRun: %v", err)
		}
		run, err := db.ScheduledRunByID(ctx, id)
		if err != nil || run == nil {
			t.Fatalf("ScheduledRunByID: run=%+v err=%v", run, err)
		}
		if run.Status != "error" || run.Error != "узел N-217 не ответил" {
			t.Fatalf("упавший прогон прочитан неверно: %+v", run)
		}
	})
}
