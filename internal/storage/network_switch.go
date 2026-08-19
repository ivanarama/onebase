package storage

// Предохранитель сети (план 62): единый флаг, без которого блокируются все
// инициируемые конфигурацией сетевые операции — исходящие веб-хуки (план 29),
// HTTP-клиент DSL (план 19), входящие HTTP-сервисы (план 61) и отправка email.
//
// Зачем: после восстановления копии базы на другой машине (или установки чужой
// конфигурации) её интеграции не должны молча стрелять в боевые системы
// (Telegram/платёжки/почта клиентам) — аналог блокировки регламентных заданий
// при старте копии в 1С. Флаг — свойство ИНСТАНСА базы (хранится в _settings,
// не в app.yaml), поэтому чужая конфигурация не может включить его себе сама.
//
// По умолчанию сеть ЗАБЛОКИРОВАНА: владелец осознанно включает предохранитель
// в конфигураторе. При восстановлении .obz флаг сбрасывается в выкл.

import (
	"context"
	"fmt"
	"strings"
)

// netEnabledKey — ключ _settings предохранителя сети.
const netEnabledKey = "net.enabled"

// NetworkEnabledHint — «что сделать», общий для всех отказов предохранителя:
// DSL-ошибка (ui.ErrNetworkLocked), отказ регламентного задания
// (scheduler.ErrNetworkLocked), предупреждение при старте сервера. Живёт рядом с
// самим флагом, потому что три копии одной фразы в трёх пакетах уже разъехались
// с интерфейсом: текст звал в «Систему → Настройки», которых в конфигураторе
// нет вовсе (меню «Система» есть только в «Предприятии», и предохранителя там
// нет). Настоящий путь — Меню → «Параметры базы» → «Безопасность»
// (internal/launcher/configurator_tmpl_shell.go, cfgAdminSettings).
const NetworkEnabledHint = "включите «Разрешить сетевые операции конфигурации» " +
	"в конфигураторе (Меню → «Параметры базы» → «Безопасность») либо командой: " +
	"onebase settings set net.enabled вкл"

// GetNetworkEnabled сообщает, разрешены ли сетевые возможности конфигурации.
// Отсутствие ключа/таблицы → false (сеть заблокирована — secure by default).
func (db *DB) GetNetworkEnabled(ctx context.Context) bool {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1), netEnabledKey).Scan(&v)
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

// SaveNetworkEnabled устанавливает предохранитель сети.
func (db *DB) SaveNetworkEnabled(ctx context.Context, on bool) error {
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
	if _, err := db.Exec(ctx, q, netEnabledKey, v); err != nil {
		return fmt.Errorf("settings: save %s: %w", netEnabledKey, err)
	}
	return nil
}
