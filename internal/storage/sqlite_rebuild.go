package storage

import (
	"context"
	"fmt"
	"strings"
)

// Пересоздание таблицы SQLite ради удаления колонки.
//
// SQLite отказывается выполнять ALTER TABLE … DROP COLUMN, если колонка
// упомянута в ограничении таблицы — а ссылочные реквизиты платформа объявляет
// именно так: CreateTableSQL дописывает FOREIGN KEY (<кол>) REFERENCES … (см.
// ddl.go). Поэтому удаление ссылочного реквизита на SQLite падало сырой ошибкой
// движка «unknown column … in foreign key definition», причём падало посреди
// прогона миграции: сущности, обработанные раньше, уже отмигрированы (#615).
//
// Тем же путём ходит ретайп: retypeSQLite удаляет старую колонку, поэтому смена
// типа ссылочного реквизита роняла ОБЫЧНЫЙ onebase migrate, без каких-либо
// флагов, и не давала стартовать серверу.
//
// Лечится по рецепту из документации SQLite («Making Other Kinds Of Table
// Schema Changes»): собрать новое определение таблицы без этой колонки и без
// ограничений, которые её упоминают, перелить данные, подменить таблицу.
// Индексы снимаются вызывающим (dropIndexesOnColumn), поэтому воссоздаются
// только те, что колонки не касались.

// dropColumnRebuildSQLite удаляет колонку пересозданием таблицы.
// Вызывается только когда прямой ALTER отказал из-за ограничения.
func (db *DB) dropColumnRebuildSQLite(ctx context.Context, table, column string) error {
	createSQL, err := db.tableCreateSQL(ctx, table)
	if err != nil {
		return err
	}
	newCreate, ok := createWithoutColumn(createSQL, table, column)
	if !ok {
		return fmt.Errorf("%s: не удалось построить определение таблицы без колонки %s", table, column)
	}
	indexes, err := db.tableIndexDDL(ctx, table)
	if err != nil {
		return err
	}

	tmp := table + "__ob_rebuild"
	return db.WithTxScope(ctx, func(ctx context.Context) error {
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(tmp)); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, strings.Replace(newCreate, table, tmp, 1)); err != nil {
			return fmt.Errorf("%s: создание временной таблицы: %w", table, err)
		}
		cols, err := tableColumnNames(ctx, db, tmp)
		if err != nil {
			return err
		}
		if len(cols) > 0 {
			quoted := make([]string, 0, len(cols))
			for _, c := range cols {
				quoted = append(quoted, quoteIdent(c))
			}
			list := strings.Join(quoted, ", ")
			if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
				quoteIdent(tmp), list, list, quoteIdent(table))); err != nil {
				return fmt.Errorf("%s: перенос данных: %w", table, err)
			}
		}
		if _, err := db.Exec(ctx, "DROP TABLE "+quoteIdent(table)); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "ALTER TABLE "+quoteIdent(tmp)+" RENAME TO "+quoteIdent(table)); err != nil {
			return err
		}
		// Индексы переезда не переживают: они принадлежали удалённой таблице.
		for _, ddl := range indexes {
			if _, err := db.Exec(ctx, ddl); err != nil {
				return fmt.Errorf("%s: восстановление индекса: %w", table, err)
			}
		}
		return nil
	})
}

func (db *DB) tableCreateSQL(ctx context.Context, table string) (string, error) {
	var sql string
	err := db.QueryRow(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&sql)
	if err != nil {
		return "", fmt.Errorf("%s: определение таблицы: %w", table, err)
	}
	return sql, nil
}

// tableIndexDDL — определения пользовательских индексов таблицы. Автоиндексы
// (sqlite_autoindex_*) собственного DDL не имеют и переезжают вместе с
// ограничением, поэтому пропускаются.
func (db *DB) tableIndexDDL(ctx context.Context, table string) ([]string, error) {
	rows, err := db.Query(ctx,
		`SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name = ? AND sql IS NOT NULL`, table)
	if err != nil {
		return nil, fmt.Errorf("%s: список индексов: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// createWithoutColumn убирает из определения таблицы колонку и все ограничения
// уровня таблицы, которые её упоминают.
//
// Разбор идёт по элементам верхнего уровня внутри внешних скобок: вложенные
// скобки и кавычки учитываются, иначе «FOREIGN KEY (a) REFERENCES t(id)»
// разъехалось бы по запятой внутри REFERENCES.
func createWithoutColumn(createSQL, table, column string) (string, bool) {
	open := strings.Index(createSQL, "(")
	closeIdx := strings.LastIndex(createSQL, ")")
	if open < 0 || closeIdx <= open {
		return "", false
	}
	head, body, tail := createSQL[:open+1], createSQL[open+1:closeIdx], createSQL[closeIdx:]

	var kept []string
	dropped := false
	for _, item := range splitTopLevel(body) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if definesColumn(trimmed, column) || constraintMentions(trimmed, column) {
			dropped = true
			continue
		}
		kept = append(kept, "\n    "+trimmed)
	}
	if !dropped || len(kept) == 0 {
		return "", false
	}
	return head + strings.Join(kept, ",") + "\n" + tail, true
}

// splitTopLevel режет список определений по запятым нулевого уровня вложенности.
func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	start := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '[':
			quote = ']'
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// definesColumn — элемент объявляет саму колонку (её имя стоит первым словом).
func definesColumn(item, column string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(item), " ")
	return strings.EqualFold(unquoteIdent(name), column)
}

// constraintMentions — ограничение уровня таблицы (FOREIGN KEY / UNIQUE /
// PRIMARY KEY / CHECK), упоминающее колонку. Такие ограничения и мешают SQLite
// удалить колонку обычным ALTER.
func constraintMentions(item, column string) bool {
	upper := strings.ToUpper(strings.TrimSpace(item))
	isConstraint := strings.HasPrefix(upper, "FOREIGN KEY") ||
		strings.HasPrefix(upper, "UNIQUE") ||
		strings.HasPrefix(upper, "PRIMARY KEY") ||
		strings.HasPrefix(upper, "CHECK") ||
		strings.HasPrefix(upper, "CONSTRAINT")
	if !isConstraint {
		return false
	}
	for _, word := range strings.FieldsFunc(item, func(r rune) bool {
		return r == '(' || r == ')' || r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		if strings.EqualFold(unquoteIdent(word), column) {
			return true
		}
	}
	return false
}

func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		switch s[0] {
		case '"', '`', '\'':
			if s[len(s)-1] == s[0] {
				return s[1 : len(s)-1]
			}
		case '[':
			if s[len(s)-1] == ']' {
				return s[1 : len(s)-1]
			}
		}
	}
	return s
}
