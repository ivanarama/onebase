// Package dbtest — общий тест-инструментарий для проверок, которые обязаны
// вести себя одинаково на обоих диалектах (план 115, этап F).
//
// Живёт отдельным пакетом, а не в internal/storage, из-за цикла импорта:
// собственные тесты storage лежат в package storage и не могут импортировать
// пакет, который импортирует storage. Другим пакетам (query, dbcheck, ui)
// такой проблемы нет, и они пользуются этим хелпером.
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

// ForEachDialect гоняет тело теста на обоих диалектах как два подтеста:
// «sqlite» — всегда, «postgres» — если задан TEST_DATABASE_URL (иначе подтест
// пропускается).
//
// Зачем это нужно. Расхождения диалектов в этом проекте — основной источник
// ТИХОЙ порчи данных: код проходит проверки, миграция завершается успешно, а
// значения молча меняются. Раздельные тесты (юнит на SQLite, *_pg_test.go на
// PostgreSQL) расхождение не показывают — каждый проверяет своё ожидание, и
// оба зелёные. Первый же матричный тест по плану 115 вскрыл сразу три таких
// дефекта, включая #607.
//
// PostgreSQL-ветка берёт эфемерную схему (план 108): параллельные пакеты
// делят одну сервисную базу, и без изоляции они видят таблицы друг друга.
//
// Файл намеренно БЕЗ тега integration: в юнит-прогоне TEST_DATABASE_URL не
// задан, postgres-подтест пропускается, sqlite работает. В PG-джобе
// исполняются оба.
func ForEachDialect(t *testing.T, body func(t *testing.T, db *storage.DB)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "matrix.db"))
		if err != nil {
			t.Fatalf("ConnectSQLite: %v", err)
		}
		t.Cleanup(db.Close)
		ensureServiceSchema(t, db)
		body(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("TEST_DATABASE_URL not set")
		}
		ctx := context.Background()
		schema := storage.NewEphemeralSchemaName()
		db, err := storage.ConnectWithSchema(ctx, dsn, schema)
		if err != nil {
			t.Fatalf("ConnectWithSchema: %v", err)
		}
		if err := db.CreateSchema(ctx, schema); err != nil {
			db.Close()
			t.Fatalf("CreateSchema: %v", err)
		}
		t.Cleanup(func() {
			if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
				t.Errorf("DropSchemaCascade(%s): %v", schema, err)
			}
			db.Close()
		})
		ensureServiceSchema(t, db)
		body(t, db)
	})
}

// ensureServiceSchema заводит служебные таблицы платформы до тела теста.
//
// Без этого тест зависел от ПОРЯДКА прогона: эфемерная схема PostgreSQL пуста,
// но search_path подхватывает public, куда служебные таблицы клал предыдущий
// пакет в общей сервисной базе. Пакет, запущенный отдельно (обычная отладка) и
// первым в CI, падал лавиной «current transaction is aborted», в которой
// причина — отсутствующая таблица — не называлась ни разу (issue #827).
func ensureServiceSchema(t *testing.T, db *storage.DB) {
	t.Helper()
	if err := db.EnsureServiceSchema(context.Background()); err != nil {
		t.Fatalf("служебные таблицы: %v", err)
	}
}
