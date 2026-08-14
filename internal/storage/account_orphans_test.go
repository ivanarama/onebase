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

		// Общего количества недостаточно: ошибочный DELETE мог снести живую
		// проводку и случайно оставить другую строку. Проверяем тот самый UUID
		// регистратора, документ которого существует.
		var liveLeft int
		liveQuery := "SELECT COUNT(*) FROM " + metadata.AccountRegTableName(ar.Name) +
			" WHERE CAST(регистратор AS TEXT) = " + db.Dialect().Placeholder(1)
		if err := db.QueryRow(ctx, liveQuery, liveID.String()).Scan(&liveLeft); err != nil {
			t.Fatal(err)
		}
		if liveLeft != 1 {
			t.Errorf("проводок живого документа %d, ожидалась 1", liveLeft)
		}
	})
}

// Явное удаление движений документа, которого больше нет в конфигурации,
// обязано использовать физическое имя колонки бухрегистра
// `регистратор_тип`, а не имя `recorder_type` регистра накопления. Проверяем
// также точность фильтра: проводки соседнего типа должны остаться.
func TestDeleteAccountEntriesOfUnknownRecorderType_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ar := &metadata.AccountRegister{
			Name:      "БухУдалениеТипа",
			Accounts:  "Основной",
			Resources: []metadata.Field{{Name: "Сумма", Type: "number"}},
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatal(err)
		}

		period := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
		rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "сумма": float64(100)}}
		const targetType = "УдалённыйДокумент"
		const neighborType = "СоседнийДокумент"
		for i := 0; i < 2; i++ {
			if err := db.WriteAccountMovements(ctx, ar.Name, targetType, uuid.New(), rows, ar, &period); err != nil {
				t.Fatalf("WriteAccountMovements(%s): %v", targetType, err)
			}
		}
		if err := db.WriteAccountMovements(ctx, ar.Name, neighborType, uuid.New(), rows, ar, &period); err != nil {
			t.Fatalf("WriteAccountMovements(%s): %v", neighborType, err)
		}

		deleted, err := db.DeleteAccountEntriesOfUnknownRecorderType(
			ctx, []*metadata.AccountRegister{ar}, []string{targetType},
		)
		if err != nil {
			t.Fatalf("DeleteAccountEntriesOfUnknownRecorderType: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("удалено %d проводок типа %s, ожидалось 2", deleted, targetType)
		}

		countByType := func(recorderType string) int {
			t.Helper()
			query := "SELECT COUNT(*) FROM " + metadata.AccountRegTableName(ar.Name) +
				" WHERE регистратор_тип = " + db.Dialect().Placeholder(1)
			var n int
			if err := db.QueryRow(ctx, query, recorderType).Scan(&n); err != nil {
				t.Fatalf("подсчёт проводок типа %s: %v", recorderType, err)
			}
			return n
		}
		if got := countByType(targetType); got != 0 {
			t.Errorf("после удаления осталось %d проводок типа %s", got, targetType)
		}
		if got := countByType(neighborType); got != 1 {
			t.Errorf("проводок соседнего типа %d, ожидалась 1", got)
		}
	})
}
