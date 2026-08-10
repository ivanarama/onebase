package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Тесты матричные: правка меняет поведение migrate, а признак наполнения
// читается из _settings и опирается на проверку существования таблицы — и то,
// и другое разное у диалектов. Раздельные тесты расхождения не показали бы.

func backfillTestEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func backfillSearch(t *testing.T, db *storage.DB, text string) []storage.FTSHit {
	t.Helper()
	hits, err := db.SearchFullText(context.Background(), storage.FTSQuery{Text: text, Limit: 20})
	if err != nil {
		t.Fatalf("поиск %q: %v", text, err)
	}
	return hits
}

// Оборванное первичное наполнение обязано повториться на следующем migrate.
//
// Таблица _fts создаётся в начале Migrate, а наполняется в конце и вне
// транзакции. Обрыв между этими точками (Ctrl+C, падение процесса, разрыв
// соединения) оставляет таблицу на месте — пустой. Прежний признак «наполнять
// надо» выводился ровно из «таблицы не было», поэтому при следующем запуске
// она уже существовала, наполнение не повторялось никогда, и поиск молча не
// находил ничего (#615).
//
// Состояние после обрыва воспроизводим прямо: таблица _fts есть и пуста,
// отметки о доведённом до конца наполнении нет. Именно это и остаётся на диске
// от прерванного migrate.
func TestFullText_ОборванноеНаполнениеПовторяется(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := backfillTestEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, cat.Name, uuid.New(),
			map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
			t.Fatal(err)
		}

		// Обрыв: индекс опустошён, отметка снята, таблица на месте.
		if _, err := db.Exec(ctx, "DELETE FROM _fts"); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveSetting(ctx, "fts_backfill_done", ""); err != nil {
			t.Fatal(err)
		}
		if hits := backfillSearch(t, db, "ромашка"); len(hits) != 0 {
			t.Fatalf("индекс не опустошён, проба некорректна: %+v", hits)
		}

		// Повторный запуск платформы.
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}
		if hits := backfillSearch(t, db, "ромашка"); len(hits) != 1 {
			t.Fatalf("после повторного migrate поиск по-прежнему пуст: %+v", hits)
		}
	})
}

// Наполнение не повторяется на каждом запуске: отметка держится, и обычный
// migrate уже проиндексированной базы полной пересборки не делает.
//
// Проверяем не по времени, а по следу: подложенная в индекс строка после
// migrate остаётся на месте — пересборка снесла бы её вместе со всем индексом.
func TestFullText_НаполнениеНеПовторяетсяБезНужды(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := backfillTestEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, cat.Name, uuid.New(),
			map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
			t.Fatal(err)
		}

		marker := uuid.New()
		if _, err := db.Exec(ctx,
			"INSERT INTO _fts (owner_kind, owner_name, owner_id, title, body) VALUES ("+
				db.Dialect().Placeholder(1)+", "+db.Dialect().Placeholder(2)+", "+db.Dialect().Placeholder(3)+", 'метка', '')",
			string(cat.Kind), cat.Name, marker); err != nil {
			t.Fatal(err)
		}

		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}

		var n int
		if err := db.QueryRow(ctx,
			"SELECT COUNT(*) FROM _fts WHERE owner_id = "+db.Dialect().Placeholder(1), marker).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("migrate пересобрал индекс уже наполненной базы: метки нет (%d)", n)
		}
	})
}
