package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Порядок колонок таблицы управляемой формы, переставленный пользователем
// (как в 1С: перетащил колонку — форма открывается с тем же порядком). Ключ
// таблицы — «вид/объект/форма/элемент»; значение — JSON-массив id колонок.
// Для анонимной/однопользовательской сессии user = "".

// gridOrderKey кодирует ключ длиной-префиксом, как reportSettingsKey: точка
// или «/» внутри частей не дают коллизий.
func gridOrderKey(grid, user string) string {
	return fmt.Sprintf("formgrid.order.%d:%s.%d:%s", len(grid), grid, len(user), user)
}

// GetFormGridOrder возвращает сохранённый порядок колонок (nil — не задан).
func (db *DB) GetFormGridOrder(ctx context.Context, grid, user string) []string {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return nil
	}
	var raw string
	if err := db.QueryRow(ctx, `SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1),
		gridOrderKey(grid, user)).Scan(&raw); err != nil {
		return nil
	}
	var order []string
	if json.Unmarshal([]byte(raw), &order) != nil {
		return nil
	}
	return order
}

// SaveFormGridOrder сохраняет порядок колонок; пустой порядок удаляет запись
// («стандартный порядок»).
func (db *DB) SaveFormGridOrder(ctx context.Context, grid, user string, order []string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	key := gridOrderKey(grid, user)
	d := db.dialect
	if len(order) == 0 {
		if _, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), key); err != nil {
			return fmt.Errorf("settings: delete %s: %w", key, err)
		}
		return nil
	}
	raw, err := json.Marshal(order)
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
