package storage

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// QueryAll executes a compiled SQL query and returns rows (without column names).
func (db *DB) QueryAll(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, _, err := db.RunQuery(ctx, sql, args)
	return rows, err
}

// RunQuery executes a compiled SQL query and returns rows with column names.
func (db *DB) RunQuery(ctx context.Context, sql string, args []any) ([]map[string]any, []string, error) {
	rows, cols, _, err := db.RunQueryLimit(ctx, sql, args, 0)
	return rows, cols, err
}

// RunQueryLimit executes a compiled SQL query and reads at most maxRows+1 rows.
// If maxRows <= 0, it behaves like RunQuery. When truncated is true, rows
// contains only the first maxRows rows.
func (db *DB) RunQueryLimit(ctx context.Context, sql string, args []any, maxRows int) ([]map[string]any, []string, bool, error) {
	querySQL := sql
	if maxRows > 0 {
		querySQL = limitedQuerySQL(sql, maxRows+1)
	}
	rows, err := db.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, nil, false, fmt.Errorf("run query: %w", err)
	}
	defer rows.Close()

	cols := rows.FieldNames()

	var result []map[string]any
	truncated := false
	for rows.Next() {
		if maxRows > 0 && len(result) >= maxRows {
			truncated = true
			break
		}
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeValue(dest[i])
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return result, cols, truncated, nil
}

func limitedQuerySQL(query string, limit int) string {
	if limit <= 0 || hasTopLevelLimit(query) {
		return query
	}
	trimmed := strings.TrimRightFunc(strings.TrimSpace(query), func(r rune) bool {
		return unicode.IsSpace(r) || r == ';'
	})
	if trimmed == "" {
		return query
	}
	return fmt.Sprintf("%s LIMIT %d", trimmed, limit)
}

func hasTopLevelLimit(query string) bool {
	depth := 0
	for i := 0; i < len(query); {
		switch query[i] {
		case '\'':
			i = skipSQLQuoted(query, i, '\'')
			continue
		case '"':
			i = skipSQLQuoted(query, i, '"')
			continue
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				i += 2
				for i < len(query) && query[i] != '\n' {
					i++
				}
				continue
			}
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				i += 2
				for i+1 < len(query) && (query[i] != '*' || query[i+1] != '/') {
					i++
				}
				if i+1 < len(query) {
					i += 2
				}
				continue
			}
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 && keywordAt(query, i, "limit") {
			return true
		}
		i++
	}
	return false
}

func skipSQLQuoted(query string, start int, quote byte) int {
	i := start + 1
	for i < len(query) {
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(query)
}

func keywordAt(query string, pos int, keyword string) bool {
	if pos > 0 && isIdentByte(query[pos-1]) {
		return false
	}
	if pos+len(keyword) > len(query) {
		return false
	}
	for i := 0; i < len(keyword); i++ {
		c := query[pos+i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != keyword[i] {
			return false
		}
	}
	return pos+len(keyword) == len(query) || !isIdentByte(query[pos+len(keyword)])
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
