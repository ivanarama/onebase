package storage

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Переименование таблиц и колонок с длинными именами в короткие (#1946).
//
// До metadata.SQLIdent имя длиннее 63 байт уходило в базу как есть: SQLite
// хранила его полностью, PostgreSQL — обрезанным. Теперь у такого имени другое
// физическое имя, и без этого шага миграция завела бы рядом новую пустую
// таблицу или колонку, а данные остались бы в старой, невидимой приложению.
// Переименование — ALTER … RENAME: данные не копируются, индексы и внешние
// ключи следуют за таблицей. Для коротких имён (почти всех) шаг ничего не
// делает и в базу не ходит.

// renameLegacyTable переименовывает таблицу с логическим именем logical из её
// прежнего физического имени в metadata.SQLIdent(logical).
func (db *DB) renameLegacyTable(ctx context.Context, logical string) error {
	legacy := metadata.LegacySQLIdents(logical)
	if legacy == nil {
		return nil
	}
	target := metadata.SQLIdent(logical)
	exists, err := db.tableExists(ctx, target)
	if err != nil {
		return fmt.Errorf("длинное имя %s: %w", logical, err)
	}
	if exists {
		return nil
	}
	for _, old := range legacy {
		found, err := db.tableExists(ctx, old)
		if err != nil {
			return fmt.Errorf("длинное имя %s: %w", logical, err)
		}
		if !found {
			continue
		}
		if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", old, target)); err != nil {
			return fmt.Errorf("переименование таблицы %s → %s: %w", old, target, err)
		}
		if err := db.renameSchemaMapTable(ctx, old, target); err != nil {
			return err
		}
		slog.Info("длинное имя таблицы сокращено", "было", old, "стало", target)
		return nil
	}
	return nil
}

// renameLegacyColumns переименовывает колонки таблицы table, чьи логические
// имена (lower(поле) или lower(поле)_id) длиннее предела, из прежних
// физических имён в metadata.SQLIdent. Прежнее имя, на которое претендуют две
// колонки (обрезка PostgreSQL дала им общее), не трогается: чьё оно — не
// установить. На PostgreSQL такая таблица и создаться не могла.
func (db *DB) renameLegacyColumns(ctx context.Context, table string, logical []string) error {
	claims := map[string]int{}
	var long []string
	for _, l := range logical {
		legacy := metadata.LegacySQLIdents(l)
		if legacy == nil {
			continue
		}
		long = append(long, l)
		for _, old := range uniqueStrings(legacy) {
			claims[old]++
		}
	}
	if len(long) == 0 {
		return nil
	}
	cols, err := db.tableColumns(ctx, table)
	if err != nil {
		return err
	}
	for _, l := range long {
		target := metadata.SQLIdent(l)
		if _, ok := cols[target]; ok {
			continue
		}
		for _, old := range metadata.LegacySQLIdents(l) {
			if _, ok := cols[old]; !ok || claims[old] > 1 {
				continue
			}
			if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, old, target)); err != nil {
				return fmt.Errorf("переименование колонки %s.%s → %s: %w", table, old, target, err)
			}
			if err := db.renameSchemaMapColumn(ctx, table, old, target); err != nil {
				return err
			}
			delete(cols, old)
			cols[target] = ""
			slog.Info("длинное имя колонки сокращено", "таблица", table, "было", old, "стало", target)
			break
		}
	}
	return nil
}

// indexName — физическое имя индекса с логическим именем logical на таблице
// table. Для длинного имени удаляет индекс под прежним именем, если он есть и
// стоит на той же таблице: следом создаётся индекс с новым именем, и без
// удаления в базе оставались бы два одинаковых. Индекс чужой таблицы с тем же
// обрезанным именем (так PostgreSQL сводил разные имена в одно) не трогается.
func (db *DB) indexName(ctx context.Context, table, logical string) (string, error) {
	target := metadata.SQLIdent(logical)
	for _, old := range uniqueStrings(metadata.LegacySQLIdents(logical)) {
		owner, err := db.indexTable(ctx, old)
		if err != nil {
			return "", fmt.Errorf("индекс %s: %w", old, err)
		}
		if owner == "" || owner != table {
			continue
		}
		if _, err := db.Exec(ctx, "DROP INDEX IF EXISTS "+old); err != nil {
			return "", fmt.Errorf("удаление индекса %s: %w", old, err)
		}
	}
	return target, nil
}

// indexTable — таблица индекса name ("" — индекса нет).
func (db *DB) indexTable(ctx context.Context, name string) (string, error) {
	var q string
	if db.IsSQLite() {
		q = `SELECT tbl_name FROM sqlite_master WHERE type = 'index' AND name = ?`
	} else {
		q = `SELECT tablename FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`
	}
	rows, err := db.Query(ctx, q, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var table string
	if rows.Next() {
		if err := rows.Scan(&table); err != nil {
			return "", err
		}
	}
	return table, rows.Err()
}

// renameSchemaMapTable / renameSchemaMapColumn держат карту полей плана 81 в
// согласии с переименованием: иначе планировщик искал бы поле под старым
// именем таблицы или колонки.
func (db *DB) renameSchemaMapTable(ctx context.Context, old, target string) error {
	if !tableExistsIn(ctx, db, "_schema_fields") {
		return nil
	}
	d := db.dialect
	if _, err := db.Exec(ctx, `UPDATE _schema_fields SET table_name = `+d.Placeholder(1)+` WHERE table_name = `+d.Placeholder(2), target, old); err != nil {
		return fmt.Errorf("schema map: переименование таблицы %s: %w", old, err)
	}
	return nil
}

func (db *DB) renameSchemaMapColumn(ctx context.Context, table, old, target string) error {
	if !tableExistsIn(ctx, db, "_schema_fields") {
		return nil
	}
	d := db.dialect
	if _, err := db.Exec(ctx, `UPDATE _schema_fields SET column_name = `+d.Placeholder(1)+` WHERE table_name = `+d.Placeholder(2)+` AND column_name = `+d.Placeholder(3), target, table, old); err != nil {
		return fmt.Errorf("schema map: переименование колонки %s.%s: %w", table, old, err)
	}
	return nil
}

// logicalColumnNames — логические имена колонок полей (до SQLIdent).
func logicalColumnNames(fields []metadata.Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, metadata.LogicalColumnName(f))
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
