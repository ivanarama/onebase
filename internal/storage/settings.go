package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/llm"
)

// DefaultListPageSize — сколько строк показывать на странице списков по
// умолчанию, если в _settings нет переопределения. Подобрано так, чтобы
// большинство справочников/документов открывалось одним экраном без скролла.
const DefaultListPageSize = 100

// MaxListPageSize — верхняя граница для валидации настройки. Совпадает с
// жёстким лимитом в parseListParams, чтобы UI и URL-параметры были согласованы.
const MaxListPageSize = 1000

// DefaultNavCollapsible — сворачиваемы ли группы левого меню по умолчанию.
// При true тяжёлые группы (Отчёты/Регистры/Обработки/…) сворачиваются, чтобы
// меню не растягивало страницу. Хранится в _settings (ui.collapsible_nav).
const DefaultNavCollapsible = true

// StandardReportPresetID is a reserved pseudo-preset used by the UI to request
// the report composition from configuration instead of a saved user preset.
const StandardReportPresetID = "__standard"

// ErrReportPresetNameExists is returned when a user tries to save a second
// preset with the same visible name for the same report.
var ErrReportPresetNameExists = errors.New("вариант отчёта с таким названием уже существует")

// ErrReservedReportPresetID is returned if caller tries to persist the
// pseudo-preset id as a real database row.
var ErrReservedReportPresetID = errors.New("зарезервированный идентификатор варианта отчёта")

// AuditSettings — настройки журнала регистрации (аналог «Журнала регистрации»
// в 1С). Это свойство конкретной информационной базы, а не git-версионируемой
// конфигурации, поэтому хранится в служебной таблице _settings.
type AuditSettings struct {
	Enabled bool // вести журнал регистрации вообще
	Create  bool // регистрировать создание объектов
	Update  bool // регистрировать изменение объектов
	Delete  bool // регистрировать удаление объектов
	Post    bool // регистрировать проведение / отмену проведения
	Login   bool // регистрировать вход / выход пользователей
}

// ReportPreset — именованный пользовательский вариант настроек отчёта.
// SettingsJSON хранит report.UserReportSettings в каноничном JSON, но слой
// storage не импортирует internal/report, чтобы не связывать пакеты циклом.
type ReportPreset struct {
	ID           string
	Report       string
	User         string
	Name         string
	SettingsJSON string
	IsDefault    bool
}

// DefaultAuditSettings — журнал включён, пишутся изменения данных и проведение;
// вход/выход по умолчанию не пишется (шумно для однопользовательских баз).
func DefaultAuditSettings() AuditSettings {
	return AuditSettings{Enabled: true, Create: true, Update: true, Delete: true, Post: true, Login: false}
}

// auditSettingKeys связывает ключи _settings с полями AuditSettings.
func auditSettingKeys(s *AuditSettings) map[string]*bool {
	return map[string]*bool{
		"audit.enabled": &s.Enabled,
		"audit.create":  &s.Create,
		"audit.update":  &s.Update,
		"audit.delete":  &s.Delete,
		"audit.post":    &s.Post,
		"audit.login":   &s.Login,
	}
}

// EnsureSettingsSchema создаёт служебную key-value таблицу _settings.
func (db *DB) EnsureSettingsSchema(ctx context.Context) error {
	if _, err := db.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		return fmt.Errorf("settings: create _settings: %w", err)
	}
	return nil
}

func (db *DB) EnsureReportPresetSchema(ctx context.Context) error {
	d := db.dialect
	q := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS _report_presets (
		id TEXT PRIMARY KEY,
		report_name TEXT NOT NULL,
		user_login TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		settings_json TEXT NOT NULL DEFAULT '',
		is_default %s NOT NULL DEFAULT %s,
		created_at %s NOT NULL DEFAULT %s,
		updated_at %s NOT NULL DEFAULT %s,
		UNIQUE(report_name, user_login, name)
	)`, d.TypeBool(), boolFalseLit(d), d.TypeTimestamp(), d.CurrentTimestampTZ(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, q); err != nil {
		return fmt.Errorf("settings: create _report_presets: %w", err)
	}
	if _, err := db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_report_presets_owner ON _report_presets (report_name, user_login, name)`); err != nil {
		return fmt.Errorf("settings: create _report_presets owner index: %w", err)
	}
	defaultLit := "TRUE"
	if d.Name() == "sqlite" {
		defaultLit = "1"
	}
	if _, err := db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_report_presets_default ON _report_presets (report_name, user_login) WHERE is_default = `+defaultLit); err != nil {
		return fmt.Errorf("settings: create _report_presets default index: %w", err)
	}
	return nil
}

// GetAuditSettings читает настройки журнала из _settings. Отсутствующие ключи
// (или отсутствующая таблица) дают значения по умолчанию.
func (db *DB) GetAuditSettings(ctx context.Context) AuditSettings {
	s := DefaultAuditSettings()
	rows, err := db.Query(ctx, `SELECT key, value FROM _settings WHERE key LIKE 'audit.%'`)
	if err != nil {
		return s
	}
	defer rows.Close()
	keys := auditSettingKeys(&s)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		if ptr, ok := keys[k]; ok {
			*ptr = v == "1" || strings.EqualFold(v, "true")
		}
	}
	return s
}

// GetListPageSize читает дефолтный размер страницы списков из _settings.
// Если ключа нет, таблицы нет или значение некорректное — возвращает
// DefaultListPageSize. Значение зажимается в [1; MaxListPageSize].
func (db *DB) GetListPageSize(ctx context.Context) int {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		"ui.list_page_size").Scan(&v)
	if err != nil {
		return DefaultListPageSize
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return DefaultListPageSize
	}
	if n > MaxListPageSize {
		return MaxListPageSize
	}
	return n
}

// SaveListPageSize сохраняет дефолтный размер страницы списков в _settings.
// Значение валидируется (1..MaxListPageSize); ноль или меньше трактуется как
// «вернуть к дефолту».
func (db *DB) SaveListPageSize(ctx context.Context, n int) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	if n <= 0 {
		n = DefaultListPageSize
	}
	if n > MaxListPageSize {
		n = MaxListPageSize
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, "ui.list_page_size", strconv.Itoa(n)); err != nil {
		return fmt.Errorf("settings: save ui.list_page_size: %w", err)
	}
	return nil
}

// GetNavCollapsible читает флаг сворачиваемых групп меню из _settings.
// Отсутствие ключа/таблицы или нераспознанное значение → DefaultNavCollapsible.
func (db *DB) GetNavCollapsible(ctx context.Context) bool {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		"ui.collapsible_nav").Scan(&v)
	if err != nil {
		return DefaultNavCollapsible
	}
	switch strings.TrimSpace(v) {
	case "1", "true", "True", "TRUE":
		return true
	case "0", "false", "False", "FALSE":
		return false
	default:
		return DefaultNavCollapsible
	}
}

// SaveNavCollapsible сохраняет флаг сворачиваемых групп меню в _settings.
func (db *DB) SaveNavCollapsible(ctx context.Context, on bool) error {
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
	if _, err := db.Exec(ctx, q, "ui.collapsible_nav", v); err != nil {
		return fmt.Errorf("settings: save ui.collapsible_nav: %w", err)
	}
	return nil
}

// Режимы открытия форм в Предприятии (issue #129/#130).
const (
	// FormModePages — формы открываются отдельными страницами (/ui). Дефолт.
	FormModePages = "pages"
	// FormModeTabs — формы открываются во вкладках (оболочка /ui/app).
	FormModeTabs = "tabs"
	// DefaultFormOpenMode — режим по умолчанию при отсутствии настройки.
	DefaultFormOpenMode = FormModePages
)

// normFormMode приводит значение к каноническому режиму; всё неизвестное —
// к дефолту pages.
func normFormMode(v string) string {
	switch strings.TrimSpace(v) {
	case FormModeTabs:
		return FormModeTabs
	case FormModePages:
		return FormModePages
	default:
		return DefaultFormOpenMode
	}
}

// GetFormOpenMode читает глобальный режим открытия форм из _settings
// (ui.form_open_mode). Отсутствие ключа/таблицы/битое значение → pages.
func (db *DB) GetFormOpenMode(ctx context.Context) string {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		"ui.form_open_mode").Scan(&v)
	if err != nil {
		return DefaultFormOpenMode
	}
	return normFormMode(v)
}

// SaveFormOpenMode сохраняет глобальный режим (нормализуется к pages/tabs).
func (db *DB) SaveFormOpenMode(ctx context.Context, mode string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, "ui.form_open_mode", normFormMode(mode)); err != nil {
		return fmt.Errorf("settings: save ui.form_open_mode: %w", err)
	}
	return nil
}

// userFormModeKey — ключ персонального режима пользователя в _settings.
func userFormModeKey(user string) string {
	return "ui.form_open_mode.user." + user
}

// GetUserFormOpenMode возвращает персональный режим пользователя или "" если
// не задан (пустой логин — всегда "", персонального режима нет).
func (db *DB) GetUserFormOpenMode(ctx context.Context, user string) string {
	if user == "" {
		return ""
	}
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		userFormModeKey(user)).Scan(&v)
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(v) {
	case FormModeTabs:
		return FormModeTabs
	case FormModePages:
		return FormModePages
	default:
		return ""
	}
}

// SaveUserFormOpenMode пишет персональный режим. Значение "" или "default"
// удаляет персональную настройку (вернуться к глобальному дефолту).
func (db *DB) SaveUserFormOpenMode(ctx context.Context, user, mode string) error {
	if user == "" {
		return nil
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	m := strings.TrimSpace(mode)
	if m == "" || m == "default" {
		_, err := db.Exec(ctx,
			`DELETE FROM _settings WHERE key = `+d.Placeholder(1),
			userFormModeKey(user))
		return err
	}
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, userFormModeKey(user), normFormMode(m)); err != nil {
		return fmt.Errorf("settings: save user form mode: %w", err)
	}
	return nil
}

// EffectiveFormOpenMode — итоговый режим: персональный, если задан, иначе
// глобальный (который при отсутствии — pages).
func (db *DB) EffectiveFormOpenMode(ctx context.Context, user string) string {
	if p := db.GetUserFormOpenMode(ctx, user); p != "" {
		return p
	}
	return db.GetFormOpenMode(ctx)
}

// Режимы хранения бинарников (картинки поля image). Аналог двух способов
// хранения файлов в 1С: «тома на диске» и «в информационной базе».
const (
	// FileStorageDisk — файл лежит на диске (filesDir/_blobs/<id>), в таблице
	// _blobs только метаданные. Режим по умолчанию.
	FileStorageDisk = "disk"
	// FileStorageDB — содержимое лежит в BLOB-колонке таблицы _blobs (в БД).
	FileStorageDB = "db"
)

// GetFileStorageMode читает режим хранения бинарников из _settings
// (ui.file_storage). Отсутствие ключа/таблицы или нераспознанное значение →
// FileStorageDisk.
func (db *DB) GetFileStorageMode(ctx context.Context) string {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		"ui.file_storage").Scan(&v)
	if err != nil {
		return FileStorageDisk
	}
	if strings.TrimSpace(v) == FileStorageDB {
		return FileStorageDB
	}
	return FileStorageDisk
}

// SaveFileStorageMode сохраняет режим хранения бинарников в _settings.
// Любое значение кроме FileStorageDB трактуется как FileStorageDisk.
func (db *DB) SaveFileStorageMode(ctx context.Context, mode string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	if mode != FileStorageDB {
		mode = FileStorageDisk
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, "ui.file_storage", mode); err != nil {
		return fmt.Errorf("settings: save ui.file_storage: %w", err)
	}
	return nil
}

// llmConfigKey — ключ _settings, под которым хранится весь LLM-конфиг (один JSON).
const llmConfigKey = "llm.config"

// GetLLMConfig читает конфиг ИИ-помощника из _settings. Отсутствие ключа/таблицы
// трактуется как пустой (выключенный) конфиг — это не ошибка. Ошибкой считается
// только повреждённый JSON.
func (db *DB) GetLLMConfig(ctx context.Context) (llm.Config, error) {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		llmConfigKey).Scan(&v)
	if err != nil {
		return llm.Config{}, nil
	}
	return llm.ParseConfig(v)
}

// SaveLLMConfig сохраняет конфиг ИИ-помощника в _settings одним JSON-значением.
func (db *DB) SaveLLMConfig(ctx context.Context, cfg llm.Config) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	raw, err := cfg.JSON()
	if err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, llmConfigKey, raw); err != nil {
		return fmt.Errorf("settings: save %s: %w", llmConfigKey, err)
	}
	return nil
}

// SaveAuditSettings сохраняет настройки журнала в _settings.
func (db *DB) SaveAuditSettings(ctx context.Context, s AuditSettings) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	for k, ptr := range auditSettingKeys(&s) {
		v := "0"
		if *ptr {
			v = "1"
		}
		if _, err := db.Exec(ctx, q, k, v); err != nil {
			return fmt.Errorf("settings: save %s: %w", k, err)
		}
	}
	return nil
}

// reportSettingsKey формирует ключ _settings для рантайм-настроек отчёта
// конкретного пользователя (план 70). Для анонимной/однопользовательской
// сессии user = "" — отдельный ключ, не пересекающийся с именованными.
//
// Имя отчёта и логин префиксуются их длиной ("<len>:<value>"), а не просто
// склеиваются через точку (issue #22): при наивной склейке точка внутри имени
// отчёта или логина давала бы коллизии — например (report="a.b",user="c") и
// (report="a",user="b.c") дали бы один и тот же ключ "report.settings.a.b.c".
// Длина-префикс делает кодировку однозначной: разобрать обратно нельзя спутать.
func reportSettingsKey(report, user string) string {
	return fmt.Sprintf("report.settings.%d:%s.%d:%s", len(report), report, len(user), user)
}

func journalSettingsKey(journal, user string) string {
	return fmt.Sprintf("journal.settings.%d:%s.%d:%s", len(journal), journal, len(user), user)
}

// GetReportUserSettings возвращает сырой JSON рантайм-настроек отчёта для
// пользователя. Отсутствие ключа/таблицы — не ошибка (как GetLLMConfig):
// возвращается ("", nil), что означает «использовать стандартный вид».
func (db *DB) GetReportUserSettings(ctx context.Context, report, user string) (string, error) {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		reportSettingsKey(report, user)).Scan(&v)
	if err != nil {
		return "", nil
	}
	return v, nil
}

// SaveReportUserSettings сохраняет рантайм-настройки отчёта пользователя одним
// JSON-значением (upsert). Конфигурацию (YAML) не трогает.
func (db *DB) SaveReportUserSettings(ctx context.Context, report, user, raw string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, reportSettingsKey(report, user), raw); err != nil {
		return fmt.Errorf("settings: save report settings: %w", err)
	}
	return nil
}

// DeleteReportUserSettings удаляет рантайм-настройки отчёта пользователя —
// сброс к стандартному виду из конфигурации.
func (db *DB) DeleteReportUserSettings(ctx context.Context, report, user string) error {
	d := db.dialect
	if _, err := db.Exec(ctx,
		`DELETE FROM _settings WHERE key = `+d.Placeholder(1),
		reportSettingsKey(report, user)); err != nil {
		return fmt.Errorf("settings: delete report settings: %w", err)
	}
	return nil
}

// GetJournalUserSettings возвращает сырой JSON пользовательских настроек
// журнала документов. Отсутствие ключа/таблицы — не ошибка.
func (db *DB) GetJournalUserSettings(ctx context.Context, journal, user string) (string, error) {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		journalSettingsKey(journal, user)).Scan(&v)
	if err != nil {
		return "", nil
	}
	return v, nil
}

// SaveJournalUserSettings сохраняет пользовательские настройки журнала одним
// JSON-значением (upsert). Конфигурацию (YAML) не трогает.
func (db *DB) SaveJournalUserSettings(ctx context.Context, journal, user, raw string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, journalSettingsKey(journal, user), raw); err != nil {
		return fmt.Errorf("settings: save journal settings: %w", err)
	}
	return nil
}

// DeleteJournalUserSettings удаляет пользовательские настройки журнала —
// сброс к стандартному виду из YAML.
func (db *DB) DeleteJournalUserSettings(ctx context.Context, journal, user string) error {
	d := db.dialect
	if _, err := db.Exec(ctx,
		`DELETE FROM _settings WHERE key = `+d.Placeholder(1),
		journalSettingsKey(journal, user)); err != nil {
		return fmt.Errorf("settings: delete journal settings: %w", err)
	}
	return nil
}

// ListReportPresets возвращает пользовательские варианты отчёта в стабильном
// порядке: дефолтный выше остальных, дальше по имени.
func (db *DB) ListReportPresets(ctx context.Context, report, user string) ([]ReportPreset, error) {
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return nil, err
	}
	d := db.dialect
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT id, report_name, user_login, name, settings_json, is_default
			FROM _report_presets
			WHERE report_name = %s AND user_login = %s AND id <> %s
			ORDER BY is_default DESC, name ASC`, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)),
		report, user, StandardReportPresetID)
	if err != nil {
		return nil, fmt.Errorf("settings: list report presets: %w", err)
	}
	defer rows.Close()
	var out []ReportPreset
	for rows.Next() {
		p, err := scanReportPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: list report presets rows: %w", err)
	}
	return out, nil
}

func (db *DB) GetReportPreset(ctx context.Context, report, user, id string) (*ReportPreset, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if id == StandardReportPresetID {
		return nil, nil
	}
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return nil, err
	}
	d := db.dialect
	row := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id, report_name, user_login, name, settings_json, is_default
			FROM _report_presets
			WHERE id = %s AND report_name = %s AND user_login = %s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)),
		id, report, user)
	p, err := scanReportPresetRow(row)
	if err != nil {
		return nil, nil
	}
	return &p, nil
}

func (db *DB) GetDefaultReportPreset(ctx context.Context, report, user string) (*ReportPreset, error) {
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return nil, err
	}
	d := db.dialect
	row := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id, report_name, user_login, name, settings_json, is_default
			FROM _report_presets
			WHERE report_name = %s AND user_login = %s AND is_default = %s AND id <> %s`,
			d.Placeholder(1), d.Placeholder(2), reportPresetBoolLit(d, true), d.Placeholder(3)),
		report, user, StandardReportPresetID)
	p, err := scanReportPresetRow(row)
	if err != nil {
		return nil, nil
	}
	return &p, nil
}

// SaveReportPreset создаёт или обновляет именованный вариант. Если p.IsDefault,
// предыдущий дефолтный вариант этого пользователя/отчёта сбрасывается в той же
// транзакции; частичный уникальный индекс дополнительно защищает инвариант.
func (db *DB) SaveReportPreset(ctx context.Context, p ReportPreset) (string, error) {
	p.Report = strings.TrimSpace(p.Report)
	p.User = strings.TrimSpace(p.User)
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	if p.Report == "" {
		return "", errors.New("report preset: empty report")
	}
	if p.Name == "" {
		return "", errors.New("report preset: empty name")
	}
	if p.ID == StandardReportPresetID {
		return "", ErrReservedReportPresetID
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return "", err
	}
	d := db.dialect
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		if reportPresetNameTaken(txCtx, db, d, p) {
			return ErrReportPresetNameExists
		}
		if p.IsDefault {
			if _, err := db.Exec(txCtx,
				fmt.Sprintf(`UPDATE _report_presets SET is_default = %s, updated_at = %s WHERE report_name = %s AND user_login = %s`,
					reportPresetBoolLit(d, false), d.Now(), d.Placeholder(1), d.Placeholder(2)),
				p.Report, p.User); err != nil {
				return fmt.Errorf("settings: clear report preset default: %w", err)
			}
		}
		q := fmt.Sprintf(`INSERT INTO _report_presets
			(id, report_name, user_login, name, settings_json, is_default, created_at, updated_at)
			VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				settings_json = EXCLUDED.settings_json,
				is_default = EXCLUDED.is_default,
				updated_at = EXCLUDED.updated_at`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
			d.Placeholder(5), d.Placeholder(6), d.Now(), d.Now())
		if _, err := db.Exec(txCtx, q, p.ID, p.Report, p.User, p.Name, p.SettingsJSON, p.IsDefault); err != nil {
			return fmt.Errorf("settings: save report preset: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func reportPresetNameTaken(ctx context.Context, db *DB, d Dialect, p ReportPreset) bool {
	var existingID string
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id FROM _report_presets
			WHERE report_name = %s AND user_login = %s AND name = %s AND id <> %s
			LIMIT 1`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4)),
		p.Report, p.User, p.Name, p.ID).Scan(&existingID)
	return err == nil && existingID != ""
}

func (db *DB) DeleteReportPreset(ctx context.Context, report, user, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if err := db.EnsureReportPresetSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	if _, err := db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM _report_presets WHERE id = %s AND report_name = %s AND user_login = %s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)),
		id, report, user); err != nil {
		return fmt.Errorf("settings: delete report preset: %w", err)
	}
	return nil
}

type presetScanner interface {
	Scan(dest ...any) error
}

func scanReportPreset(rows Rows) (ReportPreset, error) {
	return scanReportPresetRow(rows)
}

func scanReportPresetRow(row presetScanner) (ReportPreset, error) {
	var p ReportPreset
	var isDefault any
	if err := row.Scan(&p.ID, &p.Report, &p.User, &p.Name, &p.SettingsJSON, &isDefault); err != nil {
		return ReportPreset{}, err
	}
	p.IsDefault = normalizeBool(isDefault)
	return p, nil
}

func reportPresetBoolLit(d Dialect, v bool) string {
	if d.Name() == "sqlite" {
		if v {
			return "1"
		}
		return "0"
	}
	if v {
		return "TRUE"
	}
	return "FALSE"
}
