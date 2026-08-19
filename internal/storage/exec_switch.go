package storage

// Переключатель выполнения команд ОС (план 67): флаг exec.enabled, без которого
// builtin ВыполнитьКоманду заблокирован. Зеркалит предохранитель сети (план 62).
//
// Зачем отдельный флаг (а не общий «сеть»): запуск процесса — исполнение
// произвольного кода на сервере (RCE), строго опаснее сети/файлов. Включается
// осознанно владельцем доверенной/локальной базы. Флаг — свойство ИНСТАНСА
// (в _settings, не в app.yaml), поэтому чужая конфигурация не включит его сама.
//
// По умолчанию ВЫКЛЮЧЕНО. При восстановлении .obz сбрасывается в выкл.

import (
	"context"
	"fmt"
	"strings"
)

// execEnabledKey — ключ _settings переключателя команд ОС.
const execEnabledKey = "exec.enabled"

// ExecEnabledHint — «что сделать» для отказа переключателя команд ОС; парный к
// NetworkEnabledHint и по той же причине лежит рядом с флагом. Путь в
// конфигураторе — тот же экран «Параметры базы», галочка ниже сетевой.
const ExecEnabledHint = "включите «Разрешить выполнение команд ОС» " +
	"в конфигураторе (Меню → «Параметры базы» → «Безопасность») либо командой: " +
	"onebase settings set exec.enabled вкл"

// GetExecEnabled сообщает, разрешено ли выполнение команд ОС из DSL.
// Отсутствие ключа/таблицы → false (запрещено — secure by default).
func (db *DB) GetExecEnabled(ctx context.Context) bool {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1), execEnabledKey).Scan(&v)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(v) {
	case "1", "true", "True", "TRUE":
		return true
	default:
		return false
	}
}

// SaveExecEnabled устанавливает переключатель команд ОС.
func (db *DB) SaveExecEnabled(ctx context.Context, on bool) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	v := "0"
	if on {
		v = "1"
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, execEnabledKey, v); err != nil {
		return fmt.Errorf("settings: save %s: %w", execEnabledKey, err)
	}
	return nil
}
