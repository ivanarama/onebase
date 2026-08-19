package storage

// Административное решение о включённости регламентного задания (#991).
//
// В 1С «Использование» регламентного задания — состояние информационной
// базы: администратор управляет им в пользовательском режиме, и решение
// переживает обновление конфигурации. В OneBase YAML-поле enabled — только
// дефолт из конфигурации; фактическое состояние каждого задания живёт
// здесь, в _settings, как свойство инстанса базы.
//
// Ключ — scheduled.enabled.<имя>, где имя нормализовано как в планировщике
// (lower/trim), поэтому регистр в вызовах не ломает совпадение. Значения
// "1"/"0" — как у net.enabled. Отсутствие ключа — «решения нет»: действует
// конфигурационный дефолт. Битое значение при чтении тоже трактуется как
// «решения нет», а не как ошибка: тумблер не должен ронять расписание из-за
// мусора в таблице. Орфанные ключи (задание удалено из конфигурации) не
// мешают работе и не чистятся — как и в 1С.

import (
	"context"
	"fmt"
	"strings"
)

// scheduledEnabledPrefix — семейство ключей административных решений о заданиях.
const scheduledEnabledPrefix = "scheduled.enabled."

// scheduledEnabledKey строит ключ решения для имени задания.
func scheduledEnabledKey(jobName string) string {
	return scheduledEnabledPrefix + strings.ToLower(strings.TrimSpace(jobName))
}

// GetScheduledEnabled читает административное решение о включённости задания.
// ok=false — решения нет, действует конфигурационный дефолт. Отсутствие
// таблицы/ключа — не ошибка.
func (db *DB) GetScheduledEnabled(ctx context.Context, jobName string) (on, ok bool, err error) {
	v, ok, err := db.GetSetting(ctx, scheduledEnabledKey(jobName))
	if err != nil || !ok {
		return false, false, err
	}
	switch strings.TrimSpace(v) {
	case "1":
		return true, true, nil
	case "0":
		return false, true, nil
	default:
		// Мусорное значение — «решения нет», а не ошибка: чтение зовётся на
		// каждом тике планировщика и не должно ронять расписание.
		return false, false, nil
	}
}

// SaveScheduledEnabled записывает административное решение о включённости.
func (db *DB) SaveScheduledEnabled(ctx context.Context, jobName string, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return db.SaveSetting(ctx, scheduledEnabledKey(jobName), v)
}

// DeleteScheduledEnabled убирает административное решение — задание снова
// следует конфигурации. Отсутствие ключа или таблицы — не ошибка.
func (db *DB) DeleteScheduledEnabled(ctx context.Context, jobName string) error {
	if exists, err := tableExistsErr(ctx, db, "_settings"); err != nil {
		return fmt.Errorf("settings: проверка таблицы настроек: %w", err)
	} else if !exists {
		return nil
	}
	d := db.dialect
	q := `DELETE FROM _settings WHERE key = ` + d.Placeholder(1)
	if _, err := db.Exec(ctx, q, scheduledEnabledKey(jobName)); err != nil {
		return fmt.Errorf("settings: delete %s: %w", scheduledEnabledKey(jobName), err)
	}
	return nil
}

// ScheduledEnabledOverrides возвращает все решения семейства одним запросом
// (страница списка заданий). Ключ карты — нормализованное имя задания.
// Отсутствие таблицы — пустая карта, не ошибка. Мусорные значения
// пропускаются: карта несёт только валидные решения.
func (db *DB) ScheduledEnabledOverrides(ctx context.Context) (map[string]bool, error) {
	if exists, err := tableExistsErr(ctx, db, "_settings"); err != nil {
		return nil, fmt.Errorf("settings: проверка таблицы настроек: %w", err)
	} else if !exists {
		return nil, nil
	}
	// Ключи семейства всегда записываются нормализованными (нижний регистр),
	// поэтому LIKE по нижнерегистровому префиксу хватает без LOWER(key).
	rows, err := db.Query(ctx,
		`SELECT key, value FROM _settings WHERE key LIKE `+db.dialect.Placeholder(1),
		scheduledEnabledPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("settings: список решений о заданиях: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("settings: список решений о заданиях: %w", err)
		}
		switch strings.TrimSpace(value) {
		case "1":
			out[strings.TrimPrefix(key, scheduledEnabledPrefix)] = true
		case "0":
			out[strings.TrimPrefix(key, scheduledEnabledPrefix)] = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: список решений о заданиях: %w", err)
	}
	return out, nil
}
