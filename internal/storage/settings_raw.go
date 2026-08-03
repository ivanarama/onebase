package storage

import (
	"context"
	"fmt"
	"sort"
)

// Прямой доступ к _settings по ключу — для инструментов, которые работают со
// служебной таблицей как с хранилищем, а не с конкретной настройкой:
// `onebase secret` (план 83) перечисляет носители секретов, шифрует их значения
// и перешифровывает при ротации мастер-ключа.
//
// Прикладной код должен пользоваться типизированными аксессорами
// (GetLLMConfig, GetExchangeToken и т. п.): они знают формат значения и его
// допустимый диапазон. Эти три функции формата не знают вовсе.

// SettingEntry — одна запись служебной таблицы.
type SettingEntry struct {
	Key   string
	Value string
}

// ListSettings возвращает все записи _settings, отсортированные по ключу.
// Отсутствие таблицы — не ошибка: база могла быть создана до появления схемы
// настроек, и перечислять в ней просто нечего.
func (db *DB) ListSettings(ctx context.Context) ([]SettingEntry, error) {
	if !tableExistsIn(ctx, db, "_settings") {
		return nil, nil
	}
	rows, err := db.Query(ctx, `SELECT key, value FROM _settings`)
	if err != nil {
		return nil, fmt.Errorf("settings: list: %w", err)
	}
	defer rows.Close()

	var out []SettingEntry
	for rows.Next() {
		var e SettingEntry
		if err := rows.Scan(&e.Key, &e.Value); err != nil {
			return nil, fmt.Errorf("settings: list: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: list: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// GetSetting читает значение по ключу. ok=false — ключа (или таблицы) нет.
func (db *DB) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	if !tableExistsIn(ctx, db, "_settings") {
		return "", false, nil
	}
	var v string
	if e := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1), key).Scan(&v); e != nil {
		if IsNotFound(e) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("settings: read %s: %w", key, e)
	}
	return v, true, nil
}

// tableExistsIn — проверка наличия служебной таблицы без её создания: команды
// вида `onebase secret list` обязаны оставаться читающими.
func tableExistsIn(ctx context.Context, db *DB, table string) bool {
	var exists bool
	var err error
	if db.IsSQLite() {
		err = db.QueryRow(ctx,
			`SELECT COUNT(*)>0 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists)
	} else {
		err = db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&exists)
	}
	return err == nil && exists
}

// SaveSetting записывает значение по ключу как есть, без интерпретации.
func (db *DB) SaveSetting(ctx context.Context, key, value string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, key, value); err != nil {
		return fmt.Errorf("settings: save %s: %w", key, err)
	}
	return nil
}
