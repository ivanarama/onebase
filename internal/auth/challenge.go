package auth

// Промежуточное состояние входа между паролем и вторым фактором (план 84).
//
// Сессия НЕ создаётся, пока второй фактор не предъявлен: до этого момента
// пользователь опознан только наполовину, и сессионная кука дала бы доступ ко
// всему UI. Вместо неё выдаётся короткоживущий challenge — ссылка на «пароль
// уже проверен для этого пользователя», живущая в памяти процесса.
//
// Хранилище в памяти, а не в БД: challenge живёт минуты, переживать перезапуск
// ему незачем, а запись в БД на каждую попытку входа — лишний writer-трафик
// (для SQLite он единственный). Тот же приём, что у одноразовых кодов
// bootstrap (план 53).

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// challengeTTL — сколько есть времени на ввод кода. Минуты: код TOTP живёт 30
// секунд, но человеку нужно достать телефон, а при первой настройке — ещё и
// отсканировать QR.
const challengeTTL = 10 * time.Minute

// maxChallengeAttempts — попыток ввода кода на один challenge. Шестизначный
// код перебирается за 10^6 попыток; ограничение и здесь, и в лимитере входа
// делает перебор бессмысленным, не мешая опечататься пару раз.
const maxChallengeAttempts = 5

// Challenge — состояние наполовину выполненного входа.
type Challenge struct {
	UserID string
	Login  string
	// Enroll: у пользователя ещё нет второго фактора, но политика его требует —
	// вместо ввода кода показывается настройка (QR + подтверждение).
	Enroll bool
	// Secret — предлагаемый секрет TOTP при Enroll. В базу он попадёт только
	// после подтверждения кодом: иначе брошенная на полпути настройка оставила
	// бы учётку с секретом, которого нет ни в одном телефоне.
	Secret string
	// EnrollAuthorized: привязка второго фактора на входе разрешена — можно
	// показывать QR и секрет. При Enroll и false пользователю сперва предлагается
	// ввести одноразовый код привязки от администратора (issue #577): секрет не
	// генерируется, пока код не предъявлен. Разрешает привязку либо политика
	// SelfEnroll2FA, либо вход через SSO (личность подтверждена провайдером).
	EnrollAuthorized bool
	// bindCode — код привязки, предъявленный на первом шаге. Наружу не уходит
	// (поле неэкспортируемое, в шаблоны challenge не передаётся): он нужен,
	// чтобы погасить билет ПОСЛЕ того, как привязка состоялась.
	bindCode string
	// ReturnURL — куда вернуть после успешного входа.
	ReturnURL string
	// Configurator: вход в конфигуратор лаунчера, а не в Предприятие.
	Configurator bool
	// BaseID — база лаунчера (для возврата в её конфигуратор).
	BaseID string

	attempts int
	expires  time.Time
}

// Challenges — потокобезопасное хранилище challenge'ей с TTL.
type Challenges struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]*Challenge
}

func NewChallenges(ttl time.Duration) *Challenges {
	return &Challenges{ttl: ttl, items: make(map[string]*Challenge)}
}

var (
	defaultChallengesOnce sync.Once
	defaultChallengesVal  *Challenges
)

// DefaultChallenges — общее хранилище процесса. Лаунчер создаёт Repo на каждый
// запрос, поэтому привязывать challenge к экземпляру Repo нельзя.
func DefaultChallenges() *Challenges {
	defaultChallengesOnce.Do(func() { defaultChallengesVal = NewChallenges(challengeTTL) })
	return defaultChallengesVal
}

// Issue регистрирует challenge и возвращает его токен.
func (c *Challenges) Issue(ch Challenge) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	ch.expires = time.Now().Add(c.ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	c.items[token] = &ch
	return token, nil
}

// Get возвращает копию challenge'а. Неверный код challenge не гасит — иначе
// одна опечатка отправляла бы вводить пароль заново; за перебор отвечает
// счётчик попыток (см. Fail).
func (c *Challenges) Get(token string) (Challenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked()
	ch, ok := c.items[token]
	if !ok {
		return Challenge{}, false
	}
	return *ch, true
}

// Fail отмечает неудачную попытку. Возвращает false, когда попытки исчерпаны и
// challenge погашен — тогда вход начинается с пароля.
func (c *Challenges) Fail(token string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.items[token]
	if !ok {
		return false
	}
	ch.attempts++
	if ch.attempts >= maxChallengeAttempts {
		delete(c.items, token)
		return false
	}
	return true
}

// Update меняет challenge на месте, сохраняя тот же токен (и куку). Нужно, чтобы
// перевести привязку из шага «введите код от администратора» в шаг «отсканируйте
// QR», не переиздавая challenge. false — challenge истёк между чтением и правкой.
func (c *Challenges) Update(token string, fn func(*Challenge)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.items[token]
	if !ok {
		return false
	}
	fn(ch)
	return true
}

// Renew сдвигает срок жизни challenge”'а на полный TTL. Нужен на переходе к
// шагу сканирования QR: этот шаг длиннее предыдущего (поставить приложение,
// отсканировать код, дождаться следующего окна), а отсчёт шёл от ввода пароля
// (#615). false — challenge истёк между чтением и правкой.
func (c *Challenges) Renew(token string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.items[token]
	if !ok {
		return false
	}
	ch.expires = time.Now().Add(c.ttl)
	return true
}

// Delete гасит challenge (успешный вход или отказ от него).
func (c *Challenges) Delete(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, token)
}

func (c *Challenges) purgeLocked() {
	now := time.Now()
	for token, ch := range c.items {
		if now.After(ch.expires) {
			delete(c.items, token)
		}
	}
}
