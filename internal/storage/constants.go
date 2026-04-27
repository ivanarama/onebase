package storage

import (
	"context"
	"encoding/json"

	"github.com/ivantit66/onebase/internal/metadata"
)

func (db *DB) MigrateConstants(ctx context.Context, consts []*metadata.Constant) error {
	if _, err := db.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS _constants (
		name TEXT PRIMARY KEY,
		value JSONB,
		updated_at TIMESTAMPTZ DEFAULT now()
	)`); err != nil {
		return err
	}
	for _, c := range consts {
		if c.Default == "" {
			continue
		}
		raw, _ := json.Marshal(c.Default)
		if _, err := db.pool.Exec(ctx, `
			INSERT INTO _constants (name, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (name) DO NOTHING
		`, c.Name, raw); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) GetConstant(ctx context.Context, name string) (any, error) {
	var raw []byte
	if err := db.pool.QueryRow(ctx, `SELECT value FROM _constants WHERE name = $1`, name).Scan(&raw); err != nil {
		return nil, err
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	return val, nil
}

func (db *DB) SetConstant(ctx context.Context, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO _constants (name, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, name, raw)
	return err
}

func (db *DB) ListConstants(ctx context.Context) (map[string]any, error) {
	rows, err := db.pool.Query(ctx, `SELECT name, value FROM _constants`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]any)
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
	return result, rows.Err()
}
