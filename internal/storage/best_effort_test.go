package storage_test

// Необязательные операции не должны ронять запись объекта (issue #826).
//
// Тесты матричные по существу дела: правило «сбойный запрос отравляет всю
// транзакцию» есть только у PostgreSQL. На SQLite оба этих теста зелены и
// БЕЗ исправления — то есть проверка на одном диалекте показала бы успех там,
// где поведение молча разное. Ровно тот повод, по которому в проекте заведён
// dbtest.ForEachDialect.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func auditlessEntity(name string) *metadata.Entity {
	return &metadata.Entity{
		Name: name, Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
}

// Журнал аудита недоступен (таблицы нет) — объект всё равно записывается.
//
// До исправления на PostgreSQL падала вся запись: INSERT в _audit сваливался,
// транзакция уходила в aborted, и дальше отклонялось всё до отката. Причём
// сообщение говорило про savepoint и commit, а не про журнал.
func TestAudit_FailureDoesNotBreakWriteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := auditlessEntity("Контрагенты" + uuid.NewString()[:8])
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatalf("схема аудита: %v", err)
		}
		// Ломаем журнал так, как это может случиться в бою: нет прав, нет
		// таблицы, кончилось место. Проще всего воспроизвести отсутствием.
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS _audit"); err != nil {
			t.Fatalf("снос журнала: %v", err)
		}

		id := uuid.New()
		err := db.WithTx(ctx, func(txCtx context.Context) error {
			return db.Upsert(txCtx, ent.Name, id, map[string]any{"Наименование": "Альфа"}, ent)
		})
		if err != nil {
			t.Fatalf("запись объекта при недоступном журнале: %v", err)
		}

		row, err := db.GetByID(ctx, ent.Name, id, ent)
		if err != nil {
			t.Fatalf("чтение записанного объекта: %v", err)
		}
		if got, _ := row["Наименование"].(string); got != "Альфа" {
			t.Errorf("объект записан неверно: %v", row)
		}
	})
}

// Вторая запись в той же транзакции тоже проходит: savepoint откатывает только
// неудавшуюся операцию, а не всё, что было до неё.
func TestAudit_FailureKeepsTransactionUsableMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := auditlessEntity("Склады" + uuid.NewString()[:8])
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatalf("схема аудита: %v", err)
		}
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS _audit"); err != nil {
			t.Fatalf("снос журнала: %v", err)
		}

		first, second := uuid.New(), uuid.New()
		err := db.WithTx(ctx, func(txCtx context.Context) error {
			if err := db.Upsert(txCtx, ent.Name, first, map[string]any{"Наименование": "Первый"}, ent); err != nil {
				return err
			}
			return db.Upsert(txCtx, ent.Name, second, map[string]any{"Наименование": "Второй"}, ent)
		})
		if err != nil {
			t.Fatalf("две записи в одной транзакции: %v", err)
		}
		rows, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: 10})
		if err != nil {
			t.Fatalf("чтение: %v", err)
		}
		if len(rows) != 2 {
			t.Errorf("записей %d, ожидалось 2 — часть транзакции потеряна", len(rows))
		}
	})
}

// Настройки аудита читаются до записи, и их чтение тоже необязательное: без
// таблицы настроек берутся умолчания, а транзакция остаётся живой.
func TestAudit_SettingsReadFailureIsHarmlessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := auditlessEntity("Валюты" + uuid.NewString()[:8])
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS _settings"); err != nil {
			t.Fatalf("снос настроек: %v", err)
		}

		id := uuid.New()
		if err := db.WithTx(ctx, func(txCtx context.Context) error {
			return db.Upsert(txCtx, ent.Name, id, map[string]any{"Наименование": "Рубль"}, ent)
		}); err != nil {
			t.Fatalf("запись при недоступных настройках: %v", err)
		}
		if _, err := db.GetByID(ctx, ent.Name, id, ent); err != nil {
			t.Errorf("объект не записан: %v", err)
		}
	})
}

// Журнал в порядке — запись в него по-прежнему идёт: savepoint не должен
// «съедать» успешные строки.
func TestAudit_WorkingLogStillRecordsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := auditlessEntity("Договоры" + uuid.NewString()[:8])
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatalf("схема аудита: %v", err)
		}

		id := uuid.New()
		if err := db.WithTx(ctx, func(txCtx context.Context) error {
			return db.Upsert(txCtx, ent.Name, id, map[string]any{"Наименование": "Альфа"}, ent)
		}); err != nil {
			t.Fatalf("запись: %v", err)
		}

		entries, err := db.AuditByRecord(ctx, ent.Name, id)
		if err != nil {
			t.Fatalf("чтение журнала: %v", err)
		}
		if len(entries) == 0 {
			t.Error("запись в журнал не попала — savepoint откатил успешную операцию")
		}
	})
}
