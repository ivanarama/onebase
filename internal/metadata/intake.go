package metadata

// Intake — входной шлюз (план 90, пробел G6): надёжная приёмка произвольного
// внешнего события с гарантией идемпотентности и карантином (DLQ). Загружается
// из intake/<имя>.yaml. В отличие от плана обмена (ExchangePlan, OneBase↔OneBase)
// приёмка обобщает идемпотентность на внешний контур (сайт/1С/партнёры) через
// единый JSON-конверт, поступающий любым транспортом.
//
// Ядро (internal/intake) даёт две гарантии, недостижимые прикладным паттерном:
//   - атомарный insert-if-new ключа идемпотентности (UNIQUE + ON CONFLICT), нет
//     гонки TOCTOU на конкурентных дублях;
//   - обработчик и отметка «обработано» в одной транзакции (storage.WithTx), нет
//     полу-записей при сбое обработчика.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Транспорты приёмки. http — эталонный (HTTP-сервис, план 61). ws — исходящее
// WebSocket-соединение (план 120): база сама подключается к внешнему серверу и
// принимает события тем же конвертом. amqp — за швом MessageSource (нативный
// consumer из G4/путь Б), подключается позже, не трогая ядро.
const (
	IntakeTransportHTTP = "http"
	IntakeTransportAMQP = "amqp"
	IntakeTransportWS   = "ws"
)

// Режимы проверки подлинности отправителя (http-транспорт).
const (
	IntakeAuthNone  = "none"  // без проверки (для доверенного контура)
	IntakeAuthToken = "token" // постоянный секрет в заголовке X-Webhook-Token
	IntakeAuthHMAC  = "hmac"  // подпись тела X-Webhook-Signature = hex(HMAC-SHA256(тело, secret))
)

// Причины отправки события в карантин (DLQ). Совпадают с политикой dlq.on.
const (
	DLQHandlerError   = "handler_error"   // обработчик вернул ошибку → откат бизнес-объекта
	DLQSchemaMismatch = "schema_mismatch" // тот же ключ, другой payload_hash
	DLQKeyConflict    = "key_conflict"    // ключ занят несовместимой записью
)

// Intake описывает один входной шлюз.
type Intake struct {
	Name          string            `yaml:"name"`
	Title         string            `yaml:"title"`
	Titles        map[string]string `yaml:"titles"`
	Transport     string            `yaml:"transport"`      // http (эталон) | amqp (за швом) | ws (план 120)
	Endpoint      string            `yaml:"endpoint"`       // для http-транспорта: /hs/<корень>/<путь>
	SchemaVersion string            `yaml:"schema_version"` // ожидаемая версия конверта
	Idempotency   IntakeIdempotency `yaml:"idempotency"`
	Handler       string            `yaml:"handler"` // имя обработчика: процедура Обработать(Конверт) → результат
	Auth          string            `yaml:"auth"`    // none (по умолч.) | token | hmac — проверка подлинности отправителя
	Secret        string            `yaml:"secret"`  // общий секрет для token/hmac; поддерживает ${env:VAR}
	DLQ           IntakeDLQ         `yaml:"dlq"`

	// Поля ws-транспорта (план 120). Для http/amqp не используются.
	URL       string          `yaml:"url"`       // адрес внешнего сервера: ws:// или wss://
	Subscribe map[string]any  `yaml:"subscribe"` // необязательное JSON-сообщение сразу после подключения
	Reconnect IntakeReconnect `yaml:"reconnect"`
}

// IntakeReconnect — параметры переподключения ws-транспорта: экспоненциальная
// выдержка от initial до max секунд (с джиттером; см. wsclient).
type IntakeReconnect struct {
	Initial int `yaml:"initial"` // секунды, дефолт 1
	Max     int `yaml:"max"`     // секунды, дефолт 60
}

// IntakeIdempotency — правило идемпотентности: какое поле конверта является
// ключом и в каком пространстве (scope) он уникален.
type IntakeIdempotency struct {
	Key   string   `yaml:"key"`   // поле конверта — ключ (например event_id)
	Scope []string `yaml:"scope"` // поля конверта, образующие пространство ключа (source, aggregate)
	TTL   string   `yaml:"ttl"`   // окно хранения записи идемпотентности (30d, 12h, 45m); пусто — бессрочно
}

// IntakeDLQ — политика карантина.
type IntakeDLQ struct {
	On         []string `yaml:"on"`          // handler_error | schema_mismatch | key_conflict
	MaxRetries int      `yaml:"max_retries"` // предел авто-повторов replay (0 — только вручную)
}

// DisplayName возвращает заголовок шлюза с учётом языка.
func (in *Intake) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := in.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if in.Title != "" {
		return in.Title
	}
	return in.Name
}

// Normalize приводит объявление к каноничному виду: тримует имена, дефолтит
// транспорт (http) и ключ идемпотентности (event_id). Вызывается загрузчиком;
// экспортирован для программного построения (в т.ч. в тестах).
func (in *Intake) Normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Transport = strings.ToLower(strings.TrimSpace(in.Transport))
	if in.Transport == "" {
		in.Transport = IntakeTransportHTTP
	}
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.SchemaVersion = strings.TrimSpace(in.SchemaVersion)
	in.Handler = strings.TrimSpace(in.Handler)
	in.Auth = strings.ToLower(strings.TrimSpace(in.Auth))
	if in.Auth == "" {
		in.Auth = IntakeAuthNone
	}
	in.Secret = strings.TrimSpace(in.Secret)
	in.Idempotency.Key = strings.TrimSpace(in.Idempotency.Key)
	if in.Idempotency.Key == "" {
		in.Idempotency.Key = "event_id"
	}
	for i := range in.Idempotency.Scope {
		in.Idempotency.Scope[i] = strings.TrimSpace(in.Idempotency.Scope[i])
	}
	in.Idempotency.TTL = strings.TrimSpace(in.Idempotency.TTL)
	for i := range in.DLQ.On {
		in.DLQ.On[i] = strings.ToLower(strings.TrimSpace(in.DLQ.On[i]))
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.Transport == IntakeTransportWS {
		if in.Reconnect.Initial == 0 {
			in.Reconnect.Initial = 1
		}
		if in.Reconnect.Max == 0 {
			in.Reconnect.Max = 60
		}
	}
}

// Validate проверяет объявление шлюза. Вызывается загрузчиком и configcheck.
func (in *Intake) Validate() error {
	if in.Name == "" {
		return fmt.Errorf("intake: пустое имя шлюза")
	}
	switch in.Transport {
	case IntakeTransportHTTP:
		if in.Endpoint == "" {
			return fmt.Errorf("intake %q: transport http требует endpoint", in.Name)
		}
	case IntakeTransportAMQP:
		// endpoint необязателен: адрес очереди — деплой-настройка за швом MessageSource.
	case IntakeTransportWS:
		if in.URL == "" {
			return fmt.Errorf("intake %q: transport ws требует url (ws:// или wss://)", in.Name)
		}
		if !strings.HasPrefix(in.URL, "ws://") && !strings.HasPrefix(in.URL, "wss://") {
			return fmt.Errorf("intake %q: url должен начинаться с ws:// или wss://, получено %q", in.Name, in.URL)
		}
		if in.Endpoint != "" {
			return fmt.Errorf("intake %q: endpoint не применим к transport ws (соединение исходящее, адрес — в url)", in.Name)
		}
		if in.Auth == IntakeAuthHMAC {
			return fmt.Errorf("intake %q: auth hmac подписывает тело HTTP-запроса и не применим к ws; используйте token", in.Name)
		}
		if in.Reconnect.Initial < 1 {
			return fmt.Errorf("intake %q: reconnect.initial должен быть не меньше 1 секунды", in.Name)
		}
		if in.Reconnect.Max < in.Reconnect.Initial {
			return fmt.Errorf("intake %q: reconnect.max (%d) меньше reconnect.initial (%d)", in.Name, in.Reconnect.Max, in.Reconnect.Initial)
		}
	default:
		return fmt.Errorf("intake %q: неизвестный transport %q (http|amqp|ws)", in.Name, in.Transport)
	}
	if in.Handler == "" {
		return fmt.Errorf("intake %q: не задан handler (процедура Обработать)", in.Name)
	}
	switch in.Auth {
	case IntakeAuthNone:
	case IntakeAuthToken, IntakeAuthHMAC:
		if in.Secret == "" {
			return fmt.Errorf("intake %q: auth %s требует secret", in.Name, in.Auth)
		}
	default:
		return fmt.Errorf("intake %q: неизвестный auth %q (none|token|hmac)", in.Name, in.Auth)
	}
	if in.Idempotency.Key == "" {
		return fmt.Errorf("intake %q: не задан idempotency.key", in.Name)
	}
	seenScope := make(map[string]struct{}, len(in.Idempotency.Scope))
	for _, field := range in.Idempotency.Scope {
		if field == "" {
			return fmt.Errorf("intake %q: idempotency.scope содержит пустое имя поля", in.Name)
		}
		if _, duplicate := seenScope[field]; duplicate {
			return fmt.Errorf("intake %q: idempotency.scope повторяет поле %q", in.Name, field)
		}
		seenScope[field] = struct{}{}
	}
	if _, err := in.TTLSeconds(); err != nil {
		return fmt.Errorf("intake %q: idempotency.ttl: %w", in.Name, err)
	}
	for _, r := range in.DLQ.On {
		switch r {
		case DLQHandlerError, DLQSchemaMismatch, DLQKeyConflict:
		default:
			return fmt.Errorf("intake %q: неизвестная причина dlq.on %q", in.Name, r)
		}
	}
	if in.DLQ.MaxRetries < 0 {
		return fmt.Errorf("intake %q: dlq.max_retries не может быть отрицательным", in.Name)
	}
	return nil
}

// QuarantineOn сообщает, включён ли карантин для причины reason. Пустой список
// dlq.on означает «карантинить любую причину» (безопасный дефолт — не терять
// события).
func (in *Intake) QuarantineOn(reason string) bool {
	if len(in.DLQ.On) == 0 {
		return true
	}
	for _, r := range in.DLQ.On {
		if r == reason {
			return true
		}
	}
	return false
}

// TTLSeconds разбирает idempotency.ttl в секунды. Поддерживает суффиксы d/h/m/s
// (time.ParseDuration не знает «d»). Пусто → 0 (бессрочно).
func (in *Intake) TTLSeconds() (int64, error) {
	s := strings.TrimSpace(in.Idempotency.TTL)
	if s == "" {
		return 0, nil
	}
	unit := s[len(s)-1]
	var mult int64
	switch unit {
	case 'd', 'D':
		mult = 86400
	case 'h', 'H':
		mult = 3600
	case 'm', 'M':
		mult = 60
	case 's', 'S':
		mult = 1
	default:
		return 0, fmt.Errorf("неизвестная единица %q (ожидается d|h|m|s)", string(unit))
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s[:len(s)-1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("не число: %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("отрицательный ttl: %q", s)
	}
	return n * mult, nil
}

// LoadIntakeFile читает один intake/<имя>.yaml.
func LoadIntakeFile(path string) (*Intake, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in Intake
	if err := yaml.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	if in.Name == "" {
		in.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	in.Normalize()
	return &in, nil
}

// LoadIntakeDir читает все шлюзы из каталога intake/. Отсутствие каталога — не
// ошибка (приёмка не настроена).
func LoadIntakeDir(dir string) ([]*Intake, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Intake
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		in, err := LoadIntakeFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}
