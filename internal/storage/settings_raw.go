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
	// Секреты TOTP лежат не в _settings, а колонкой _users.totp_secret (план 84).
	// Без них `onebase secret list` показывал ноль носителей, а `secret rotate`
	// печатал «перешифровывать нечего» — и после штатной ротации мастер-ключа
	// код из приложения переставал приниматься у всех сразу, без диагностики.
	totp, err := db.TOTPSecretCarriers(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, totp...)
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

// TableExists — то же самое, но со СБОЕМ, отделённым от «таблицы нет».
//
// HasTable отвечает bool, и сбой соединения там неотличим от отсутствия
// таблицы. Для пропуска необязательной работы этого достаточно, но там, где за
// ответом следует «значит, делать нечего», такой ответ опасен: путь молча
// отчитывается об успехе, ничего не сделав (#611). Такие места спрашивают
// здесь.
func (db *DB) TableExists(ctx context.Context, table string) (bool, error) {
	return tableExistsErr(ctx, db, table)
}

// tableExistsIn — проверка наличия служебной таблицы без её создания: команды
// вида `onebase secret list` обязаны оставаться читающими.
func tableExistsIn(ctx context.Context, db *DB, table string) bool {
	exists, err := tableExistsErr(ctx, db, table)
	return err == nil && exists
}

// tableExistsErr — та же проверка, но ошибка возвращается вызывающему.
func tableExistsErr(ctx context.Context, db *DB, table string) (bool, error) {
	var exists bool
	var err error
	if db.IsSQLite() {
		err = db.QueryRow(ctx,
			`SELECT COUNT(*)>0 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists)
	} else {
		// current_schema(), а не литерал 'public': служебные таблицы создаются
		// неквалифицированно и потому ложатся в схему подключения (план 108).
		// Фильтр по 'public' заставлял loadSchemaMap отдавать в эфемерной схеме
		// пустую карту полей — реструктуризация плана 81 молча превращалась в
		// no-op (#638). При обычном подключении current_schema() и есть 'public'.
		err = db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1)`, table).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("storage: проверка наличия таблицы %s: %w", table, err)
	}
	return exists, nil
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

// TOTPSecretRow — секрет второго фактора одной учётной записи, как он записан.
type TOTPSecretRow struct {
	UserID string
	Login  string
	Value  string
}

// ListTOTPSecrets возвращает непустые секреты TOTP. Отсутствие таблицы или
// колонки — не ошибка: база могла не проходить миграцию плана 84.
//
// «Нет колонки» отделяем от прочих сбоев явной проверкой ColumnExists, а не
// глотанием любой ошибки Query: транзиентный сбой соединения иначе выглядел бы
// как «перешифровывать/гасить нечего», и secret rotate и disableUnreadableTOTP
// тихо не сделали бы ничего, отрапортовав об успехе (#611).
func (db *DB) ListTOTPSecrets(ctx context.Context) ([]TOTPSecretRow, error) {
	// Через TableExists, а не HasTable: «таблицы нет» и «спросить не удалось»
	// здесь ведут к разным ответам. Раньше оборванное соединение давало false,
	// и функция отвечала «секретов нет» — тот самый зелёный отчёт о ничего не
	// сделавшей ротации, ради которого и заведена проверка ниже (#611).
	hasUsers, err := db.TableExists(ctx, "_users")
	if err != nil {
		return nil, fmt.Errorf("settings: проверка таблицы пользователей: %w", err)
	}
	if !hasUsers {
		return nil, nil
	}
	hasCol, err := db.dialect.ColumnExists(ctx, db, "_users", "totp_secret")
	if err != nil {
		return nil, fmt.Errorf("settings: проверка колонки totp_secret: %w", err)
	}
	if !hasCol {
		return nil, nil // плана 84 в этой базе ещё не было
	}
	rows, err := db.Query(ctx, `SELECT id, login, totp_secret FROM _users WHERE totp_secret <> ''`)
	if err != nil {
		return nil, fmt.Errorf("settings: чтение секретов TOTP: %w", err)
	}
	defer rows.Close()
	var out []TOTPSecretRow
	for rows.Next() {
		var r TOTPSecretRow
		if err := rows.Scan(&r.UserID, &r.Login, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TOTPSecretCarriers — секреты TOTP в виде носителей секретов.
func (db *DB) TOTPSecretCarriers(ctx context.Context) ([]SecretCarrier, error) {
	rows, err := db.ListTOTPSecrets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SecretCarrier, 0, len(rows))
	for _, r := range rows {
		out = append(out, SecretCarrier{"auth.user." + r.Login + ".totp_secret", r.Value})
	}
	return out, nil
}

// SaveTOTPSecretRaw записывает секрет TOTP как есть — для перешифровки при
// ротации мастер-ключа. Прикладной путь включения второго фактора идёт через
// internal/auth.
func (db *DB) SaveTOTPSecretRaw(ctx context.Context, userID, value string) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _users SET totp_secret = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := db.Exec(ctx, q, value, userID)
	return err
}

// DisableTOTPRaw гасит второй фактор учётной записи и стирает нечитаемый
// секрет. Используется восстановлением из бэкапа: см. internal/backup.
func (db *DB) DisableTOTPRaw(ctx context.Context, userID string) error {
	d := db.dialect
	q := fmt.Sprintf(
		`UPDATE _users SET totp_secret = '', totp_enabled = %s, totp_last_step = 0 WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := db.Exec(ctx, q, false, userID)
	return err
}
