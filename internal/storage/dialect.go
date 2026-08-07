package storage

import (
	"context"
	"fmt"
)

// Dialect captures the SQL surface where PostgreSQL and SQLite diverge.
// All non-trivial SQL generation in the storage/query/auth layers must go
// through a Dialect so the same caller works on both backends.
type Dialect interface {
	// Name returns "postgres" or "sqlite". Used for logging and feature
	// gating (e.g. some advanced operations only on PG).
	Name() string

	// Placeholder returns the placeholder for the N-th parameter (1-based):
	// PG → "$1", SQLite → "?".
	Placeholder(n int) string

	// Now is the SQL expression for the current timestamp.
	// PG → "now()", SQLite → "datetime('now')".
	Now() string

	// CurrentTimestampTZ is for columns that need a timezone-aware default.
	// PG → "now()", SQLite → "datetime('now')" (UTC, no TZ).
	CurrentTimestampTZ() string

	// LowerLike wraps a column reference for a case-insensitive comparison:
	// PG → "LOWER(col::text)", SQLite → "LOWER(CAST(col AS TEXT))".
	LowerLike(col string) string

	// CaseInsensitiveLikeOp returns the operator used for case-insensitive
	// pattern matching against a literal/parameter:
	// PG → "ILIKE", SQLite → "LIKE" (paired with COLLATE NOCASE or LOWER on
	// both sides — callers should prefer LowerLike).
	CaseInsensitiveLikeOp() string

	// Types — DDL type names for column declarations.
	TypeBool() string
	TypeText() string
	// TypeNumber returns the type for decimal numbers. Per project policy
	// (см. план), SQLite uses TEXT + shopspring/decimal at the Go boundary.
	TypeNumber(precision, scale int) string
	TypeTimestamp() string
	TypeUUID() string
	TypeBytes() string

	// TypeJSON returns the column type for JSON values.
	// PG → "JSONB", SQLite (>=3.45) → "BLOB" (native jsonb).
	TypeJSON() string

	// JSONCast returns the cast suffix needed when binding a JSON value to a
	// parameter: PG → "::jsonb", SQLite → "" (no cast — bind raw TEXT/BLOB).
	JSONCast() string

	// JSONField returns SQL to extract a JSON path from a column.
	// PG → col->'name', SQLite → json_extract(col,'$.name').
	JSONField(col, jsonPath string) string

	// AddColumnIfMissing returns the SQL to add `col typ` to `table` if the
	// column doesn't already exist. PG can do this in one statement; SQLite
	// requires PRAGMA-based introspection — implementations may need to issue
	// multiple statements via DB. ColumnExists is the inspection helper.
	ColumnExists(ctx context.Context, db *DB, table, col string) (bool, error)

	// CreateDatabase ensures the database/file exists. For PG this is
	// CREATE DATABASE on the maintenance DB; for SQLite it's `touch file`
	// (handled inside ConnectSQLite). Returns nil if already exists.
	CreateDatabase(ctx context.Context, dsn string) error

	// LatestPerKey produces a SQL fragment that selects the latest row per
	// composite key from baseTable. The semantics match PostgreSQL's
	// DISTINCT ON: for each unique combination of partitionBy columns, keep
	// the row that comes first in orderBy (which should sort the "latest"
	// row first — usually DESC by timestamp). Used by virtual tables for
	// info-register СрезПоследних/СрезПервых.
	//
	// PG:     SELECT DISTINCT ON (k1,k2) cols FROM t WHERE ... ORDER BY k1,k2,ord
	// SQLite: SELECT cols FROM (SELECT cols, ROW_NUMBER() OVER (PARTITION BY k1,k2 ORDER BY ord) rn FROM t WHERE ...) WHERE rn=1
	LatestPerKey(cols []string, partitionBy, orderBy []string, baseTable, alias, where string) string
}

// PgDialect is the PostgreSQL implementation of Dialect.
type PgDialect struct{}

func (PgDialect) Name() string                  { return "postgres" }
func (PgDialect) Placeholder(n int) string      { return fmt.Sprintf("$%d", n) }
func (PgDialect) Now() string                   { return "now()" }
func (PgDialect) CurrentTimestampTZ() string    { return "now()" }
func (PgDialect) LowerLike(col string) string   { return "LOWER(" + col + "::text)" }
func (PgDialect) CaseInsensitiveLikeOp() string { return "ILIKE" }
func (PgDialect) TypeBool() string              { return "BOOLEAN" }
func (PgDialect) TypeText() string              { return "TEXT" }
func (PgDialect) TypeNumber(p, s int) string {
	if p > 0 {
		return fmt.Sprintf("NUMERIC(%d,%d)", p, s)
	}
	return "NUMERIC"
}
func (PgDialect) TypeTimestamp() string { return "TIMESTAMPTZ" }
func (PgDialect) TypeUUID() string      { return "UUID" }
func (PgDialect) TypeBytes() string     { return "BYTEA" }
func (PgDialect) TypeJSON() string      { return "JSONB" }
func (PgDialect) JSONCast() string      { return "::jsonb" }
func (PgDialect) JSONField(col, path string) string {
	return fmt.Sprintf("%s->'%s'", col, path)
}

// ColumnExists проверяет колонку В СХЕМЕ ПОДКЛЮЧЕНИЯ. Фильтра по схеме тут не
// было вовсе, поэтому колонка «существовала», если одноимённая таблица нашлась
// в любой схеме базы (#638).
func (PgDialect) ColumnExists(ctx context.Context, db *DB, table, col string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		   WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`,
		table, col,
	).Scan(&exists)
	return exists, err
}

func (PgDialect) CreateDatabase(ctx context.Context, dsn string) error {
	// Implemented by storage.EnsureDatabase (kept there for backward compat).
	return EnsureDatabase(ctx, dsn)
}

func (PgDialect) LatestPerKey(cols, partitionBy, orderBy []string, baseTable, alias, where string) string {
	if alias == "" {
		alias = "_t"
	}
	whereSQL := ""
	if where != "" {
		whereSQL = " WHERE " + where
	}
	return fmt.Sprintf(
		"SELECT DISTINCT ON (%s) %s FROM %s AS %s%s ORDER BY %s, %s",
		joinCols(partitionBy),
		joinCols(cols),
		baseTable, alias, whereSQL,
		joinCols(partitionBy), joinCols(orderBy),
	)
}

// SQLiteDialect is the SQLite implementation of Dialect.
type SQLiteDialect struct{}

func (SQLiteDialect) Name() string             { return "sqlite" }
func (SQLiteDialect) Placeholder(n int) string { return "?" }
func (SQLiteDialect) Now() string              { return "datetime('now')" }

// CurrentTimestampTZ wraps datetime('now') in parens because SQLite requires
// non-constant DEFAULT expressions to be parenthesised.
func (SQLiteDialect) CurrentTimestampTZ() string { return "(datetime('now'))" }
func (SQLiteDialect) LowerLike(col string) string {
	// ob_lower — Unicode-aware замена встроенной LOWER (см. init в sqlite.go);
	// нужна для регистронезависимых отборов/поиска по кириллице.
	return "ob_lower(CAST(" + col + " AS TEXT))"
}
func (SQLiteDialect) CaseInsensitiveLikeOp() string { return "LIKE" }
func (SQLiteDialect) TypeBool() string              { return "INTEGER" } // 0/1
func (SQLiteDialect) TypeText() string              { return "TEXT" }

// TEXT для денежной точности: shopspring/decimal на стороне Go. См. план.
func (SQLiteDialect) TypeNumber(p, s int) string { return "TEXT" }
func (SQLiteDialect) TypeTimestamp() string      { return "TEXT" } // ISO 8601
func (SQLiteDialect) TypeUUID() string           { return "TEXT" }
func (SQLiteDialect) TypeBytes() string          { return "BLOB" }
func (SQLiteDialect) TypeJSON() string           { return "BLOB" } // SQLite ≥3.45 native JSONB
func (SQLiteDialect) JSONCast() string           { return "" }
func (SQLiteDialect) JSONField(col, path string) string {
	return fmt.Sprintf("json_extract(%s,'$.%s')", col, path)
}

func (SQLiteDialect) ColumnExists(ctx context.Context, db *DB, table, col string) (bool, error) {
	rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteIdent(table)+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	target := col
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == target {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (SQLiteDialect) CreateDatabase(ctx context.Context, dsn string) error {
	// SQLite database is just a file — created by Open() automatically.
	return nil
}

func (SQLiteDialect) LatestPerKey(cols, partitionBy, orderBy []string, baseTable, alias, where string) string {
	if alias == "" {
		alias = "_t"
	}
	whereSQL := ""
	if where != "" {
		whereSQL = " WHERE " + where
	}
	// Outer SELECT берёт колонки из inner subquery, где они уже под
	// alias-именами («номенклатура_id AS номенклатура»). Поэтому в outer
	// нужно использовать только alias, иначе SQLite ругается «no such
	// column: номенклатура_id» — оригинального имени уже нет в scope
	// подзапроса. Inner SELECT же читает колонки таблицы — там полные
	// выражения с AS работают.
	outerCols := make([]string, len(cols))
	for i, c := range cols {
		outerCols[i] = aliasOf(c)
	}
	return fmt.Sprintf(
		"SELECT %s FROM (SELECT %s, ROW_NUMBER() OVER (PARTITION BY %s ORDER BY %s) AS _rn FROM %s AS %s%s) _w WHERE _rn = 1",
		joinCols(outerCols),
		joinCols(cols),
		joinCols(partitionBy), joinCols(orderBy),
		baseTable, alias, whereSQL,
	)
}

// aliasOf вытаскивает alias-имя из выражения вида «expr AS alias»; если
// AS нет — возвращает выражение как есть. Регистронезависимый поиск
// потому что в DSL встречается и AS, и as.
func aliasOf(col string) string {
	for i := 0; i+3 < len(col); i++ {
		if (col[i] == ' ') &&
			(col[i+1] == 'A' || col[i+1] == 'a') &&
			(col[i+2] == 'S' || col[i+2] == 's') &&
			(col[i+3] == ' ') {
			return col[i+4:]
		}
	}
	return col
}

func joinCols(cs []string) string {
	out := ""
	for i, c := range cs {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

// sqliteIdent quotes a SQLite identifier for embedding directly into SQL.
// Used in places where we cannot use a parameter (PRAGMA, table name).
func sqliteIdent(s string) string {
	// SQLite allows " or [ ] or ` for quoting; double the inner quote.
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	out = append(out, '"')
	return string(out)
}
