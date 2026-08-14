package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Осиротевшие проводки бухрегистра — на обоих диалектах (#881).
//
// Матрично намеренно: таблица бухрегистра и колонки регистратора названы
// кириллицей (акк_*, регистратор, регистратор_тип), а SQL вокруг них —
// диалектозависимый (плейсхолдеры, цитирование идентификаторов). Раздельные
// тесты показали бы зелёное на SQLite при молча ином поведении на PostgreSQL —
// ровно тот разрыв, ради которого заведён dbtest.ForEachDialect (план 115).
func TestOrphanAccountEntries_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{
			Name:   "Реализация",
			Kind:   metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		}
		ar := &metadata.AccountRegister{
			Name:      "БухУчёт",
			Accounts:  "Основной",
			Resources: []metadata.Field{{Name: "Сумма", Type: "number"}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}

		period := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
		rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "сумма": float64(100)}}

		// Живой документ: проводки на месте.
		liveID := uuid.New()
		if err := db.Upsert(ctx, doc.Name, liveID, map[string]any{"Номер": "1"}, doc); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteAccountMovements(ctx, ar.Name, doc.Name, liveID, rows, ar, &period); err != nil {
			t.Fatal(err)
		}
		// Удалённый документ: проводки осиротели.
		if err := db.WriteAccountMovements(ctx, ar.Name, doc.Name, uuid.New(), rows, ar, &period); err != nil {
			t.Fatal(err)
		}
		// Документ вне конфигурации: проводки целы, а не осиротели.
		if err := db.WriteAccountMovements(ctx, ar.Name, "СтароеИмя", uuid.New(), rows, ar, &period); err != nil {
			t.Fatal(err)
		}

		regs := []*metadata.AccountRegister{ar}
		ents := []*metadata.Entity{doc}

		stats, err := db.OrphanAccountEntries(ctx, regs, ents)
		if err != nil {
			t.Fatalf("OrphanAccountEntries: %v", err)
		}
		orphans, unknown := 0, 0
		for _, s := range stats {
			if s.UnknownType {
				unknown += s.Count
			} else {
				orphans += s.Count
			}
		}
		if orphans != 1 {
			t.Errorf("сирот найдено %d, ожидалась 1 (проводка удалённого документа)", orphans)
		}
		if unknown != 1 {
			t.Errorf("проводок вне конфигурации найдено %d, ожидалась 1", unknown)
		}

		deleted, err := db.DeleteOrphanAccountEntries(ctx, regs, ents)
		if err != nil {
			t.Fatalf("DeleteOrphanAccountEntries: %v", err)
		}
		if deleted != 1 {
			t.Errorf("удалено %d проводок, ожидалась 1", deleted)
		}

		var left int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+metadata.AccountRegTableName(ar.Name)).Scan(&left); err != nil {
			t.Fatal(err)
		}
		// Осталась проводка живого документа и проводка документа вне
		// конфигурации: последнюю удалять нельзя — то же расхождение даёт
		// обычное переименование документа.
		if left != 2 {
			t.Errorf("осталось %d проводок, ожидалось 2 (живая и вне конфигурации)", left)
		}
	})
}
