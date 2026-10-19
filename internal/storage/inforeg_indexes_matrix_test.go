package storage_test

// Индексы регистра сведений (`indexes:` в inforegs/*.yaml).
//
// Первичный ключ периодического регистра начинается с period, поэтому отбор
// по измерению без периода («зона по адресу», срез последних по одному
// измерению) без своего индекса читает всю таблицу. Тест идёт путём
// пользователя: YAML регистра → LoadInfoRegisterFile → MigrateInfoRegisters, и
// матричный — DDL индекса у SQLite и PostgreSQL свой.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func loadInfoRegYAML(t *testing.T, body string) *metadata.InfoRegister {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reg.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ir, err := metadata.LoadInfoRegisterFile(p)
	if err != nil {
		t.Fatalf("загрузка регистра: %v", err)
	}
	return ir
}

// tableIndexColumns — составы колонок всех индексов таблицы, по порядку
// колонок в индексе («a,b,c»).
func tableIndexColumns(t *testing.T, db *storage.DB, table string) map[string]bool {
	t.Helper()
	ctx := context.Background()
	out := map[string]bool{}
	if db.Dialect().Name() == "postgres" {
		rows, err := db.QueryAll(ctx, `SELECT i.relname AS idx,
			string_agg(a.attname, ',' ORDER BY array_position(x.indkey::int[], a.attnum::int)) AS cols
			FROM pg_index x
			JOIN pg_class i ON i.oid = x.indexrelid
			JOIN pg_class c ON c.oid = x.indrelid
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(x.indkey)
			WHERE c.relname = $1 AND c.relnamespace = to_regnamespace(current_schema())
			GROUP BY i.relname`, table)
		if err != nil {
			t.Fatalf("индексы %s: %v", table, err)
		}
		for _, r := range rows {
			out[strings.TrimSpace(asString(r["cols"]))] = true
		}
		return out
	}
	list, err := db.QueryAll(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ?`, table)
	if err != nil {
		t.Fatalf("индексы %s: %v", table, err)
	}
	for _, r := range list {
		info, err := db.QueryAll(ctx, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, asString(r["name"]))
		if err != nil {
			t.Fatalf("pragma_index_info: %v", err)
		}
		cols := make([]string, 0, len(info))
		for _, c := range info {
			cols = append(cols, asString(c["name"]))
		}
		out[strings.Join(cols, ",")] = true
	}
	return out
}

func asString(v any) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func TestInfoRegIndexes_CreatedFromYAMLMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		name := "ЗоныАдресов" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		ir := loadInfoRegYAML(t, `name: `+name+`
periodic: true
dimensions:
  - name: Адрес
    type: string
  - name: Зона
    type: string
  - name: Направление
    type: string
resources:
  - name: Сумма
    type: number
indexes:
  - fields: [Адрес, Направление, period]
  - fields: [Сумма]
`)
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		table := metadata.InfoRegTableName(name)
		got := tableIndexColumns(t, db, table)
		for _, want := range []string{"адрес,направление,period", "сумма"} {
			if !got[want] {
				t.Fatalf("нет индекса (%s); индексы таблицы: %v", want, got)
			}
		}

		// Повторная миграция ничего не ломает, а смена ключа (новое
		// измерение) пересоздаёт таблицу на SQLite — индекс обязан пережить и
		// это. Регистр пуст: новое измерение NOT NULL, а заполненный регистр
		// платформа так расширить не даёт — это отдельная история.
		ir2 := loadInfoRegYAML(t, `name: `+name+`
periodic: true
dimensions:
  - name: Адрес
    type: string
  - name: Зона
    type: string
  - name: Направление
    type: string
  - name: Бригада
    type: string
resources:
  - name: Сумма
    type: number
indexes:
  - fields: [Адрес, Направление, period]
`)
		for i := 0; i < 2; i++ {
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir2}); err != nil {
				t.Fatalf("миграция %d после смены ключа: %v", i+1, err)
			}
		}
		if got := tableIndexColumns(t, db, table); !got["адрес,направление,period"] {
			t.Fatalf("индекс потерян после смены ключа; индексы таблицы: %v", got)
		}
	})
}
