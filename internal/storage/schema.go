package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Эфемерные схемы PostgreSQL для schema-изоляции тестов (план 136, шаг 5):
// весь прогон идёт во временной схеме, которая в конце удаляется CASCADE, не
// затрагивая рабочие данные. В отличие от транзакционной изоляции подходит
// тестам, которые сами управляют транзакциями (проведение, вложенные SAVEPOINT).

// NewEphemeralSchemaName возвращает уникальное имя временной схемы. Только
// [a-z0-9_], поэтому безопасно и без кавычек, но всё равно квотируется при DDL.
func NewEphemeralSchemaName() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "onebase_test_" + hex.EncodeToString(b[:])
}

// ConnectWithSchema подключается к PostgreSQL, прибивая search_path каждого
// коннекта пула к «<schema>,public». Так весь ввод-вывод (миграции, запись/
// чтение из тестов) идёт в указанную схему, а общие типы/расширения из public
// по-прежнему резолвятся. Схему нужно создать (CreateSchema) до запросов и
// удалить (DropSchemaCascade) после.
//
// Контракт (#638): служебные таблицы платформы — _schema_fields, _settings,
// _numerators, _sequences, _fts, _audit и прочие — живут В ЭТОЙ схеме, а не в
// public. Так уже устроена запись: все Ensure*Schema делают неквалифицированный
// CREATE TABLE IF NOT EXISTS, и PostgreSQL кладёт таблицу в первую существующую
// схему search_path, то есть в эфемерную; прогон обязан умирать вместе со
// схемой. Из public берутся только общие типы, расширения и глобальный каст
// uuid→text.
//
// Отсюда правило для всего пакета: любая интроспекция каталога (pg_tables,
// information_schema.*, pg_constraint) фильтруется по current_schema(), а не по
// литералу 'public'. Иначе читаем и пишем в разные места — ровно это и ломало
// реструктуризацию: карта полей бралась из public, а таблица лежала в схеме
// подключения.
func ConnectWithSchema(ctx context.Context, dsn, schema string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: parse dsn: %w", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	// search_path применяется на старте соединения; несуществующая схема в
	// списке не ошибка — резолв идёт при запросе, к тому времени схема создана.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}
	// Тот же implicit uuid→text cast, что и в обычном Connect (каст глобальный,
	// не привязан к схеме) — чтобы запросы для SQLite работали и на PG.
	_, _ = pool.Exec(ctx, `DROP CAST IF EXISTS (uuid AS text)`)
	_, _ = pool.Exec(ctx, `CREATE CAST (uuid AS text) WITH INOUT AS IMPLICIT`)

	return &DB{pool: pool, filesDir: defaultFilesDir(dsn), dialect: PgDialect{}}, nil
}

// CreateSchema создаёт схему (идемпотентно). Только PostgreSQL.
func (db *DB) CreateSchema(ctx context.Context, name string) error {
	if db.pool == nil {
		return fmt.Errorf("storage: schema-изоляция доступна только на PostgreSQL")
	}
	_, err := db.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgQuoteIdent(name))
	return err
}

// DropSchemaCascade удаляет схему со всем содержимым. Только PostgreSQL;
// на SQLite — no-op (нечего чистить, база и так временная/файловая).
func (db *DB) DropSchemaCascade(ctx context.Context, name string) error {
	if db.pool == nil {
		return nil
	}
	_, err := db.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgQuoteIdent(name)+" CASCADE")
	return err
}
