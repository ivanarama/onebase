package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/llm"
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

// SecretCarrier — место в базе, где лежит секрет: логический путь и записанное
// значение (ссылка либо секрет открытым текстом).
type SecretCarrier struct {
	Path  string
	Value string
}

// SecretCarriers перечисляет носители секретов в _settings: ключи провайдеров
// ИИ внутри llm.config, токены планов обмена. Значения возвращаются как есть —
// решать, что с ними делать, вызывающему: `onebase secret list` описывает их
// вид, бэкап предупреждает о тех, что лежат открытым текстом.
//
// Место общее намеренно: иначе обход llm.config пришлось бы повторять в каждом
// инструменте, и они разошлись бы при добавлении новой подсистемы.
func (db *DB) SecretCarriers(ctx context.Context) ([]SecretCarrier, error) {
	entries, err := db.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	var out []SecretCarrier
	for _, e := range entries {
		switch {
		case e.Key == "llm.config":
			cfg, err := llm.ParseConfig(e.Value)
			if err != nil {
				continue // битый JSON — забота проверки конфигурации, не наша
			}
			for _, ep := range cfg.Endpoints {
				if strings.TrimSpace(ep.APIKey) != "" {
					out = append(out, SecretCarrier{"llm." + ep.Name + ".api_key", ep.APIKey})
				}
				for h, v := range ep.Headers {
					if strings.TrimSpace(v) != "" && secretHeaderName(h) {
						out = append(out, SecretCarrier{"llm." + ep.Name + ".headers." + h, v})
					}
				}
			}
		case e.Key == "auth.providers":
			// Клиентские секреты провайдеров единого входа (план 84).
			for id, secret := range providerSecrets(e.Value) {
				out = append(out, SecretCarrier{"auth.provider." + id + ".client_secret", secret})
			}
		case strings.HasPrefix(e.Key, "exchange.token."):
			if strings.TrimSpace(e.Value) != "" {
				out = append(out, SecretCarrier{e.Key, e.Value})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// providerSecrets достаёт client_secret каждого провайдера входа. Разбор
// нетипизированный намеренно: типы провайдеров живут в internal/auth, который
// сам зависит от storage, и типизировать их здесь означало бы цикл импортов.
// Битый JSON пропускается — им занимается админка, а не перечисление секретов.
func providerSecrets(raw string) map[string]string {
	var providers []map[string]any
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		return nil
	}
	out := make(map[string]string, len(providers))
	for i, p := range providers {
		secret, _ := p["client_secret"].(string)
		if strings.TrimSpace(secret) == "" {
			continue
		}
		id, _ := p["id"].(string)
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("#%d", i+1)
		}
		out[id] = secret
	}
	return out
}

// secretHeaderName отделяет заголовки с учётными данными от служебных
// (Content-Type, Accept): точного списка не существует, ориентируемся на имя.
func secretHeaderName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{"auth", "token", "key", "secret", "signature", "password"} {
		if strings.Contains(n, marker) {
			return true
		}
	}
	return false
}

// HasTable сообщает, есть ли в базе таблица с таким именем. Нужна проверкам
// состояния базы (план 114): объект может быть в конфигурации, а таблицы ещё
// нет — миграцию не выполняли.
func (db *DB) HasTable(ctx context.Context, table string) bool {
	return tableExistsIn(ctx, db, table)
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
