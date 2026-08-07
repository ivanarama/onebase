// Package secrets — единый резолвер секретов OneBase (план 83).
//
// Секрет (ключ ИИ-провайдера, пароль SMTP, токен вебхука или узла обмена, ключи
// S3) хранится в конфигурации не значением, а ссылкой одного из видов:
//
//	env:ИМЯ       — переменная окружения процесса;
//	file:/путь    — файл (docker/k8s secrets, смонтированный том);
//	enc:<base64>  — само значение, зашифрованное AES-256-GCM на мастер-ключе,
//	                который живёт вне базы (ONEBASE_MASTER_KEY).
//
// Те же три вида работают встроенными в строку — ${env:ИМЯ}, ${file:/путь},
// ${enc:...}: это нужно там, где секрет — часть значения, а не всё значение
// (URL вебхука с токеном, заголовок Authorization).
//
// Раньше разбор ${env:...} был продублирован в project-загрузчике и в llm, а у
// SMTP-пароля жила третья реализация с другим синтаксисом (env:VAR без скобок).
// Пакет собирает их в одном месте и добавляет file:/enc:, не меняя поведения
// уже работающих конфигураций: обе формы ссылки на окружение поддержаны, а
// отсутствующая переменная по-прежнему разыменовывается в пустую строку.
//
// Разыменование делается В МОМЕНТ ИСПОЛЬЗОВАНИЯ секрета, а не при загрузке
// конфигурации — тогда расшифрованное значение не оседает в _settings, в
// describe, в экспорте конфигурации и в дампе бэкапа.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Схемы ссылок на секрет.
const (
	SchemeEnv  = "env"
	SchemeFile = "file"
	SchemeEnc  = "enc"
)

// refPattern сопоставляет ссылку, встроенную в строку: ${env:ИМЯ},
// ${file:/путь}, ${enc:<base64>}. Синтаксис ${env:...} исторический — с ним уже
// написаны конфигурации, поэтому он и остался основным.
var refPattern = regexp.MustCompile(`\$\{(env|file|enc):([^}]*)\}`)

// Resolver разыменовывает ссылки на секреты. Значение без ссылки возвращается
// как есть — конфигурация, где секрет записан открытым текстом, продолжает
// работать (гигиену такого случая проверяет onebase check).
type Resolver struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	key      *Key
	keyErr   error
}

// Option — параметр конструктора (подмена окружения и файловой системы в тестах,
// явный мастер-ключ вместо взятого из окружения).
type Option func(*Resolver)

// WithEnv подменяет источник переменных окружения.
func WithEnv(fn func(string) string) Option {
	return func(r *Resolver) { r.getenv = fn }
}

// WithFileReader подменяет чтение файлов (ссылки file:).
func WithFileReader(fn func(string) ([]byte, error)) Option {
	return func(r *Resolver) { r.readFile = fn }
}

// WithKey задаёт мастер-ключ явно, минуя окружение.
func WithKey(k *Key) Option {
	return func(r *Resolver) { r.key, r.keyErr = k, nil }
}

// New собирает резолвер. Мастер-ключ читается из окружения один раз здесь:
// вывод ключа из парольной фразы (PBKDF2) стоит сотни миллисекунд, повторять его
// на каждое разыменование нельзя.
//
// Отсутствие мастер-ключа — НЕ ошибка конструктора: база может вообще не
// пользоваться enc:-значениями. Ошибка всплывёт при разыменовании конкретной
// enc:-ссылки — то есть выключится подсистема, которой этот секрет нужен, а не
// сервер целиком.
func New(opts ...Option) *Resolver {
	r := &Resolver{getenv: os.Getenv, readFile: os.ReadFile}
	for _, o := range opts {
		o(r)
	}
	if r.key == nil && r.keyErr == nil {
		r.key, r.keyErr = LoadKey(r.getenv, r.readFile)
	}
	return r
}

var (
	defaultMu  sync.Mutex
	defaultRes *Resolver
	defaultFP  string
)

// Default — общий резолвер процесса поверх реального окружения.
//
// Резолвер кэшируется по значениям переменных с мастер-ключом: пока они не
// менялись, возвращается один и тот же экземпляр, и вывод ключа из парольной
// фразы (PBKDF2, сотни миллисекунд) не повторяется. Смена переменной приводит к
// пересборке — иначе процесс, которому ключ выдали после старта, не увидел бы
// его до перезапуска, а тесты не могли бы задавать ключ через t.Setenv.
func Default() *Resolver {
	fp := os.Getenv(EnvMasterKey) + "\x00" + os.Getenv(EnvMasterKeyFile)
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultRes == nil || defaultFP != fp {
		defaultRes, defaultFP = New(), fp
	}
	return defaultRes
}

// Key возвращает загруженный мастер-ключ. Ключ не задан → (nil, ErrNoMasterKey).
func (r *Resolver) Key() (*Key, error) {
	if r.keyErr != nil {
		return nil, r.keyErr
	}
	if r.key == nil {
		return nil, ErrNoMasterKey
	}
	return r.key, nil
}

// HasKey сообщает, доступен ли мастер-ключ (для диагностики и подсказок CLI).
func (r *Resolver) HasKey() bool {
	return r.keyErr == nil && r.key != nil
}

// Resolve разыменовывает значение секрет-носящего поля.
//
// Ошибки различаются по смыслу:
//
//   - отсутствующая переменная окружения — не ошибка, подставляется пустая
//     строка (историческое поведение ${env:...}, на него опираются конфигурации,
//     где секрет задан не во всех окружениях);
//   - нечитаемый file: и неразворачиваемый enc: — ошибка, потому что здесь
//     администратор явно сказал «секрет вот тут», и молча продолжить с пустым
//     значением значило бы, например, уйти к провайдеру без ключа.
func (r *Resolver) Resolve(s string) (string, error) {
	if scheme, arg, ok := splitBare(s); ok {
		return r.resolveOne(scheme, arg)
	}
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var firstErr error
	out := refPattern.ReplaceAllStringFunc(s, func(m string) string {
		g := refPattern.FindStringSubmatch(m)
		v, err := r.resolveOne(g[1], g[2])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

func (r *Resolver) resolveOne(scheme, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	switch scheme {
	case SchemeEnv:
		if arg == "" {
			return "", errors.New("secrets: в ссылке env: не указано имя переменной")
		}
		return r.getenv(arg), nil
	case SchemeFile:
		if arg == "" {
			return "", errors.New("secrets: в ссылке file: не указан путь")
		}
		b, err := r.readFile(arg)
		if err != nil {
			return "", fmt.Errorf("secrets: чтение file:%s: %w", arg, err)
		}
		// Секрет в файле почти всегда дописан переводом строки редактором или
		// `echo`; пробелы внутри значения при этом законны, поэтому режем
		// только завершающий перевод строки.
		return strings.TrimRight(string(b), "\r\n"), nil
	case SchemeEnc:
		key, err := r.Key()
		if err != nil {
			return "", err
		}
		return key.Decrypt(SchemeEnc + ":" + arg)
	}
	return "", fmt.Errorf("secrets: неизвестная схема ссылки %q", scheme)
}

// splitBare распознаёт ссылку, занимающую ВСЁ значение: env:ИМЯ, file:/путь,
// enc:<base64>. Форма без ${} исторически принята у SMTP-пароля и удобна в YAML,
// где значение целиком и есть секрет.
func splitBare(s string) (scheme, arg string, ok bool) {
	t := strings.TrimSpace(s)
	for _, sc := range []string{SchemeEnv, SchemeFile, SchemeEnc} {
		if strings.HasPrefix(t, sc+":") {
			return sc, strings.TrimPrefix(t, sc+":"), true
		}
	}
	return "", "", false
}

// Kind — вид значения секрет-носящего поля: чем оно является для гигиены.
type Kind string

// Виды значений.
const (
	KindEmpty Kind = "empty" // не задано
	KindPlain Kind = "plain" // секрет открытым текстом — то, что мы хотим извести
	KindEnv   Kind = "env"
	KindFile  Kind = "file"
	KindEnc   Kind = "enc"
)

// Classify определяет вид значения. Ссылкой считается значение, которое ЦЕЛИКОМ
// является ссылкой: только тогда открытого секрета в конфигурации нет.
func Classify(s string) Kind {
	t := strings.TrimSpace(s)
	if t == "" {
		return KindEmpty
	}
	if scheme, _, ok := splitBare(t); ok {
		return Kind(scheme)
	}
	if m := refPattern.FindStringSubmatch(t); m != nil && m[0] == t {
		return Kind(m[1])
	}
	return KindPlain
}

// IsRef сообщает, что значение целиком — ссылка на секрет (открытого значения в
// конфигурации нет). Используется там, где решается, маскировать ли значение при
// показе: имя переменной окружения показывать админу полезно, ключ — нельзя.
func IsRef(s string) bool {
	k := Classify(s)
	return k == KindEnv || k == KindFile || k == KindEnc
}

// ContainsRef сообщает, что в значении есть встроенная ссылка. Нужно для полей,
// где секрет — часть строки (URL вебхука с токеном, заголовок авторизации):
// такое значение не является ссылкой целиком, но и открытым секретом не является.
func ContainsRef(s string) bool {
	return refPattern.MatchString(s)
}

// ReencryptEnc перешифровывает все enc:-секреты в значении со старого ключа на
// новый, сохраняя остальной текст и встроенные env:/file:-ссылки нетронутыми.
//
// Обрабатывает и значение-целиком (bare `enc:...` или `${enc:...}`), и ВСТРОЕННЫЕ
// `${enc:...}` — секрет как часть строки (`Bearer ${enc:…}`, URL вебхука с
// токеном): ротация обязана добраться и до них, иначе они молча останутся под
// старым ключом (#617). Форма каждого вхождения сохраняется (bare ↔ ${}).
//
// changed=false, если enc-секретов нет или все уже под новым ключом, — тогда
// вызывающему нечего записывать, и повторный прогон ротации безопаден.
func ReencryptEnc(value string, oldKey, newKey *Key) (out string, changed bool, err error) {
	// Значение-целиком без обёртки ${}: bare `enc:...` (env:/file: — не наше).
	if scheme, _, ok := splitBare(strings.TrimSpace(value)); ok {
		if scheme != SchemeEnc {
			return value, false, nil
		}
		return reencToken(strings.TrimSpace(value), oldKey, newKey)
	}
	if !refPattern.MatchString(value) {
		return value, false, nil
	}
	var firstErr error
	out = refPattern.ReplaceAllStringFunc(value, func(m string) string {
		sub := refPattern.FindStringSubmatch(m)
		if sub == nil || sub[1] != SchemeEnc {
			return m // env:/file: оставляем как есть
		}
		tok, ch, terr := reencToken(m, oldKey, newKey)
		if terr != nil {
			if firstErr == nil {
				firstErr = terr
			}
			return m
		}
		if ch {
			changed = true
		}
		return tok
	})
	if firstErr != nil {
		return "", false, firstErr
	}
	return out, changed, nil
}

// EncUnderOtherKey сообщает, что в значении есть enc:-секрет — целиком или
// встроенный `${enc:…}`, — зашифрованный НЕ ключом keyID. Нужно предупреждению
// об устаревшем мастер-ключе: встроенный enc тоже перестаёт открываться после
// смены ключа, а по одному Classify его не видно. Значение уже целиком под keyID
// (или enc в нём нет) даёт false — повторный прогон ротации не пугает зря.
func EncUnderOtherKey(value, keyID string) bool {
	underOther := func(tok string) bool {
		id, ok := RefKeyID(tok)
		return ok && id != keyID
	}
	if scheme, _, ok := splitBare(strings.TrimSpace(value)); ok {
		return scheme == SchemeEnc && underOther(strings.TrimSpace(value))
	}
	for _, m := range refPattern.FindAllStringSubmatch(value, -1) {
		if m[1] == SchemeEnc && underOther(m[0]) {
			return true
		}
	}
	return false
}

// reencToken перешифровывает один enc-токен (bare `enc:...` или `${enc:...}`),
// сохраняя его обёртку. changed=false, если он уже под новым ключом.
func reencToken(token string, oldKey, newKey *Key) (string, bool, error) {
	wrapped := strings.HasPrefix(strings.TrimSpace(token), "${")
	if id, ok := RefKeyID(token); ok && id == newKey.ID() {
		return token, false, nil // уже под новым ключом
	}
	plain, err := oldKey.Decrypt(token)
	if err != nil {
		return "", false, err
	}
	enc, err := newKey.Encrypt(plain) // возвращает bare `enc:...`
	if err != nil {
		return "", false, err
	}
	if wrapped {
		enc = "${" + enc + "}"
	}
	return enc, true, nil
}

// Mask возвращает значение в виде, пригодном для показа: последние четыре знака
// и звёздочки. Ссылки не маскируются — это не секрет, а указание, где он лежит.
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if IsRef(s) {
		return s
	}
	if len([]rune(s)) <= 4 {
		return "****"
	}
	r := []rune(s)
	return "****" + string(r[len(r)-4:])
}

// Describe описывает вид значения по-русски — для onebase secret list и отчётов
// линтера. Само значение не раскрывается никогда.
func Describe(s string) string {
	switch Classify(s) {
	case KindEmpty:
		return "не задано"
	case KindEnv:
		return "переменная окружения"
	case KindFile:
		return "файл"
	case KindEnc:
		if id, ok := RefKeyID(s); ok {
			return "зашифровано (ключ " + id + ")"
		}
		return "зашифровано"
	default:
		if ContainsRef(s) {
			return "ссылка в составе значения"
		}
		return "ОТКРЫТЫЙ ТЕКСТ"
	}
}
