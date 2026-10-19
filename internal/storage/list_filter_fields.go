package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Поля, которые пользователь добавил себе в панель «Отбор» списка сверх
// основного набора list_filters сущности. Хранятся в _settings за ним;
// пустой список удаляет запись. Для анонимной сессии user = "".

func listFilterFieldsKey(entity, user string) string {
	return fmt.Sprintf("list.filterfields.%d:%s.%d:%s", len(entity), entity, len(user), user)
}

// GetListFilterFields возвращает добавленные пользователем поля отбора.
func (db *DB) GetListFilterFields(ctx context.Context, entity, user string) []string {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return nil
	}
	var raw string
	if err := db.QueryRow(ctx, `SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1),
		listFilterFieldsKey(entity, user)).Scan(&raw); err != nil {
		return nil
	}
	var fields []string
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return nil
	}
	return fields
}

// SaveListFilterFields сохраняет добавленные пользователем поля отбора.
func (db *DB) SaveListFilterFields(ctx context.Context, entity, user string, fields []string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	key := listFilterFieldsKey(entity, user)
	d := db.dialect
	if len(fields) == 0 {
		if _, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), key); err != nil {
			return fmt.Errorf("settings: delete %s: %w", key, err)
		}
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, key, string(raw)); err != nil {
		return fmt.Errorf("settings: save %s: %w", key, err)
	}
	return nil
}
