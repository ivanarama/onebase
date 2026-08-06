package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// forEachDialect гоняет тело теста на обоих диалектах как два подтеста:
// «sqlite» — всегда, «postgres» — если задан TEST_DATABASE_URL (иначе подтест
// пропускается).
//
// Зачем это нужно отдельным хелпером (план 115, этап F). Расхождения диалектов
// в этом проекте — основной источник ТИХОЙ порчи данных: код проходит проверки,
// миграция завершается успешно, а значения молча меняются. Свежий пример —
// issue #607: ретайп string→boolean на PostgreSQL отрабатывает верно, а на
// SQLite обнуляет все истинные значения, потому что CAST('true' AS INTEGER)
// даёт 0. Поймать это можно только одним и тем же сценарием на обоих
// диалектах со сверкой результата — раздельные тесты (юнит на SQLite,
// *_pg_test.go на PostgreSQL) расхождение не показывают: каждый проверяет
// своё ожидание и оба зелёные.
//
// PostgreSQL-ветка берёт эфемерную схему (план 108): параллельные пакеты
// делят одну сервисную базу, и без изоляции они видят таблицы друг друга.
//
// Файл намеренно БЕЗ тега integration: в юнит-прогоне (`go test ./...`)
// TEST_DATABASE_URL не задан, поэтому postgres-подтест пропускается, а
// sqlite-подтест работает. В PG-джобе (`-tags integration` + TEST_DATABASE_URL)
// исполняются оба.
func forEachDialect(t *testing.T, body func(t *testing.T, db *DB)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		db, err := ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "matrix.db"))
		if err != nil {
			t.Fatalf("ConnectSQLite: %v", err)
		}
		t.Cleanup(db.Close)
		body(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("TEST_DATABASE_URL not set")
		}
		ctx := context.Background()
		schema := NewEphemeralSchemaName()
		db, err := ConnectWithSchema(ctx, dsn, schema)
		if err != nil {
			t.Fatalf("ConnectWithSchema: %v", err)
		}
		if err := db.CreateSchema(ctx, schema); err != nil {
			db.Close()
			t.Fatalf("CreateSchema: %v", err)
		}
		// Обход дырявой изоляции: ConnectWithSchema ставит
		// search_path = "<schema>,public", поэтому карта полей _schema_fields
		// резолвится в ОБЩУЮ public и переживает эфемерную схему. Прогоны
		// начинают видеть карту друг друга: план реструктуризации считает
		// «уже нужного типа» и не делает ничего, а таблица в своей схеме
		// остаётся прежней. Заводим локальную копию, чтобы матричные тесты
		// были действительно изолированы.
		// TODO(#638): убрать, когда системные таблицы перестанут утекать в public.
		if _, err := db.Exec(ctx,
			`CREATE TABLE IF NOT EXISTS `+schema+`._schema_fields
			   (LIKE public._schema_fields INCLUDING ALL)`); err != nil {
			t.Logf("локальная _schema_fields не заведена (%v) — вероятно, в public её ещё нет", err)
		}
		t.Cleanup(func() {
			if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
				t.Errorf("DropSchemaCascade(%s): %v", schema, err)
			}
			db.Close()
		})
		body(t, db)
	})
}
