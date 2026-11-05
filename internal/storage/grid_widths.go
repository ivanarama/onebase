package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ширины колонок таблицы управляемой формы, заданные пользователем мышью
// (как в 1С: растянул колонку — форма открывается с той же шириной). Ключ
// таблицы тот же, что у порядка колонок («вид/объект/форма/элемент»);
// значение — JSON-объект «id колонки → ширина в пикселях».

func gridWidthsKey(grid, user string) string {
	return fmt.Sprintf("formgrid.widths.%d:%s.%d:%s", len(grid), grid, len(user), user)
}

// GetFormGridWidths возвращает сохранённые ширины колонок (nil — не заданы).
func (db *DB) GetFormGridWidths(ctx context.Context, grid, user string) map[string]int {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return nil
	}
	var raw string
	if err := db.QueryRow(ctx, `SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1),
		gridWidthsKey(grid, user)).Scan(&raw); err != nil {
		return nil
	}
	var widths map[string]int
	if json.Unmarshal([]byte(raw), &widths) != nil || len(widths) == 0 {
		return nil
	}
	return widths
}

// SaveFormGridWidths сохраняет ширины колонок; пустой набор удаляет запись
// («стандартные колонки»).
func (db *DB) SaveFormGridWidths(ctx context.Context, grid, user string, widths map[string]int) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	key := gridWidthsKey(grid, user)
	d := db.dialect
	if len(widths) == 0 {
		if _, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), key); err != nil {
			return fmt.Errorf("settings: delete %s: %w", key, err)
		}
		return nil
	}
	raw, err := json.Marshal(widths)
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
