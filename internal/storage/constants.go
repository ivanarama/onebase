package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
)

func (db *DB) MigrateConstants(ctx context.Context, consts []*metadata.Constant) error {
	d := db.dialect
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS _constants (
		name TEXT PRIMARY KEY,
		value %s,
		updated_at %s DEFAULT %s
	)`, d.TypeJSON(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return err
	}
	insert := fmt.Sprintf(`
		INSERT INTO _constants (name, value, updated_at) VALUES (%s, %s%s, %s)
		ON CONFLICT (name) DO NOTHING
	`, d.Placeholder(1), d.Placeholder(2), d.JSONCast(), d.Now())
	for _, c := range consts {
		if c.Default == "" {
			continue
		}
		raw, _ := json.Marshal(c.Default)
		if _, err := db.Exec(ctx, insert, c.Name, raw); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) GetConstant(ctx context.Context, name string) (any, error) {
	d := db.dialect
	var raw []byte
	q := fmt.Sprintf(`SELECT value FROM _constants WHERE name = %s`, d.Placeholder(1))
	if err := db.QueryRow(ctx, q, name).Scan(&raw); err != nil {
		return nil, err
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	return val, nil
}

func (db *DB) SetConstant(ctx context.Context, name string, value any) error {
	d := db.dialect
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`
		INSERT INTO _constants (name, value, updated_at) VALUES (%s, %s%s, %s)
		ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, updated_at = %s
	`, d.Placeholder(1), d.Placeholder(2), d.JSONCast(), d.Now(), d.Now())
	_, err = db.Exec(ctx, q, name, raw)
	return err
}

// ListConstants читает значения констант. Вызывающие используют её
// «best-effort» (ошибку игнорируют и работают с пустой картой), поэтому чтение
// изолировано savepoint'ом: на PostgreSQL сбойный SELECT внутри транзакции
// губит её целиком, и отсутствие таблицы констант роняло бы проведение
// документа (issue #826). Строки вычитываются внутри области — освобождать
// savepoint при открытом курсоре нельзя.
func (db *DB) ListConstants(ctx context.Context) (map[string]any, error) {
	result := make(map[string]any)
	err := db.bestEffort(ctx, func(ctx context.Context) error {
		rows, err := db.Query(ctx, `SELECT name, value FROM _constants`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var raw []byte
			if err := rows.Scan(&name, &raw); err != nil {
				continue
			}
			var val any
			if err := json.Unmarshal(raw, &val); err != nil {
				continue
			}
			result[name] = val
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
