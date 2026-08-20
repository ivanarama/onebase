package storage

import (
	"context"
	"fmt"
	"strings"
)

// sqliteFullText — реализация полнотекстового поиска на SQLite: виртуальная
// таблица FTS5 поверх _fts (external content) плюс триггеры синхронизации.
// External content означает, что текст хранится один раз — в _fts, а FTS5
// держит только индекс; писать в него отдельно не нужно, за это отвечают
// триггеры, поэтому наполнение индекса общее для обоих диалектов.
//
// Морфологии здесь нет (в FTS5 нет русского стеммера), поэтому «договоры» по
// запросу «договор» найдётся за счёт префиксного поиска, а обратно — нет.
// Для dev-баз это приемлемо; прод на PostgreSQL получает полноценный стеммер.
type sqliteFullText struct{}

func (sqliteFullText) EnsureSchema(ctx context.Context, db *DB) error {
	stmts := []string{
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(
			title, body,
			content='%s', content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2')`, ftsIndexTbl, ftsTable),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS _fts_ai AFTER INSERT ON %s BEGIN
			INSERT INTO %s(rowid, title, body) VALUES (new.rowid, new.title, new.body);
		END`, ftsTable, ftsIndexTbl),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS _fts_ad AFTER DELETE ON %s BEGIN
			INSERT INTO %s(%s, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
		END`, ftsTable, ftsIndexTbl, ftsIndexTbl),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS _fts_au AFTER UPDATE ON %s BEGIN
			INSERT INTO %s(%s, rowid, title, body) VALUES ('delete', old.rowid, old.title, old.body);
			INSERT INTO %s(rowid, title, body) VALUES (new.rowid, new.title, new.body);
		END`, ftsTable, ftsIndexTbl, ftsIndexTbl, ftsIndexTbl),
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			return fmt.Errorf("полнотекстовый индекс (FTS5): %w", err)
		}
	}
	return nil
}

func (sqliteFullText) Search(ctx context.Context, db *DB, q FTSQuery) ([]FTSHit, error) {
	match := sqliteMatchExpr(ftsTokens(q.Text))
	if match == "" {
		return nil, nil
	}
	args := []any{match}
	where := fmt.Sprintf("%s MATCH ?", ftsIndexTbl)
	if len(q.Names) > 0 {
		where += " AND f.owner_name IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Names)), ",") + ")"
		for _, n := range q.Names {
			args = append(args, n)
		}
	}
	scopeSQL, scopeArgs, _, err := ftsScopeSQL(db.dialect, q.Scopes, len(args)+1)
	if err != nil {
		return nil, err
	}
	if scopeSQL != "" {
		where += " AND (" + scopeSQL + ")"
		args = append(args, scopeArgs...)
	}
	args = append(args, q.Limit, q.Offset)
	// bm25 тем меньше, чем релевантнее строка; инвертируем, чтобы контракт
	// FTSHit.Rank («больше = лучше») был одинаков на обоих диалектах.
	// Веса колонок 10:1 — представление важнее прочего текста, как вес A/B в PG.
	sqlText := fmt.Sprintf(`
		SELECT f.owner_kind, f.owner_name, f.owner_id, f.title, -bm25(%s, 10.0, 1.0) AS rank
		FROM %s JOIN %s f ON f.rowid = %s.rowid
		WHERE %s
		ORDER BY rank DESC, f.owner_name, f.owner_id
		LIMIT ? OFFSET ?`, ftsIndexTbl, ftsIndexTbl, ftsTable, ftsIndexTbl, where)
	return scanFTSHits(ctx, db, sqlText, args...)
}

// sqliteMatchExpr собирает выражение FTS5: каждое слово в кавычках и с «*» —
// префиксный поиск, слова соединяются неявным И. Кавычки обязательны: они
// делают токен строковым литералом, поэтому синтаксис MATCH не зависит от
// содержимого ввода.
func sqliteMatchExpr(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"*`)
	}
	return strings.Join(parts, " ")
}
