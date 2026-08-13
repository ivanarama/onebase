// Package wsclient — исходящее WebSocket-соединение с переподключением
// (план 120A). Чистый транспорт: не знает ни про приёмку, ни про метаданные.
// Владелец (ui.wsIntakeSupervisor) даёт колбэки: gate (предохранитель сети),
// onMessage (доставка входящего), logf (журнал).
//
// Обратное давление — синхронной обработкой: следующее сообщение не читается,
// пока onMessage не вернулся. Очереди в памяти нет намеренно — при медленном
// обработчике замедляется чтение (TCP-окно давит на сервер), а не растёт память
// до OOM (план 120, решение 6).
package wsclient

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ErrNotConnected возвращается Send, когда соединение не установлено (обрыв,
// пауза переподключения или блокировка предохранителем). Гарантий доставки нет
// по контракту (#738): не отправилось — ошибка сразу, без буфера и повтора.
var ErrNotConnected = errors.New("websocket: соединение не установлено")

// dialTimeout ограничивает рукопожатие; writeTimeout — одну отправку.
const (
	dialTimeout  = 30 * time.Second
	writeTimeout = 10 * time.Second
	// steadyReset: соединение, прожившее дольше этого срока, считается
	// состоявшимся — выдержка переподключения сбрасывается к начальной.
	// Сбрасывать по факту подключения нельзя: сервер, принимающий и сразу
	// рвущий соединение, дал бы горячий цикл без выдержки.
	steadyReset = 30 * time.Second
)

// Config — параметры одного соединения.
type Config struct {
	Name string // имя шлюза, для журнала и состояния
	URL  string // ws:// или wss://
	// Header строит заголовки рукопожатия перед каждым подключением: секрет
	// разыменовывается на dial, а не на старте — появившаяся env-переменная
	// подхватывается очередной попыткой без рестарта. nil — без заголовков.
	Header           func() (http.Header, error)
	Subscribe        []byte // сообщение сразу после подключения; nil — не слать
	ReconnectInitial time.Duration
	ReconnectMax     time.Duration
	MaxMessageBytes  int64 // лимит входящего сообщения

	// Gate вызывается перед каждой попыткой подключения и перед обработкой
	// каждого сообщения. Непустая строка — причина блокировки (предохранитель
	// сети): соединение не поднимается / закрывается. nil — без блокировок.
	Gate func() string
	// OnMessage обрабатывает входящее синхронно. Ошибка пишется в журнал и
	// состояние, но соединение НЕ рвёт: решения о карантине принимает приёмка.
	OnMessage func(ctx context.Context, raw []byte) error
	// Logf — журнал владельца (формат log.Printf). nil — молча.
	Logf func(format string, args ...any)
}

// Status — снимок состояния соединения для админки и DSL (Подключён()).
type Status struct {
	Connected      bool
	BlockedReason  string    // непустая — заблокировано предохранителем
	ConnectedSince time.Time // нулевое время, если не подключено
	LastMessageAt  time.Time
	LastError      string
	Reconnects     int64 // завершившиеся попытки подключения после первой
	Received       int64 // принятых сообщений (до обработки)
	HandlerErrors  int64 // ошибок onMessage (сообщение потеряно для приёмки)
	Sent           int64
	SendErrors     int64
}

// Client — одно управляемое соединение. Создаётся New, живёт в Run.
type Client struct {
	cfg Config

	mu     sync.Mutex
	conn   *websocket.Conn
	wmu    sync.Mutex // сериализует Write: websocket допускает одного писателя
	status Status
}

// New создаёт клиента. Запуск — отдельно, Run(ctx).
func New(cfg Config) *Client {
	if cfg.ReconnectInitial <= 0 {
		cfg.ReconnectInitial = time.Second
	}
	if cfg.ReconnectMax < cfg.ReconnectInitial {
		cfg.ReconnectMax = cfg.ReconnectInitial
	}
	return &Client{cfg: cfg}
}

// Status возвращает снимок состояния.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Send отправляет текстовое сообщение в живое соединение. Не подключено —
// ErrNotConnected немедленно (без буфера — контракт #738). Разрыв, замеченный
// на отправке, соединение не закрывает: его увидит и обслужит цикл чтения.
func (c *Client) Send(ctx context.Context, data []byte) error {
	// Порядок принципиален. В coder/websocket отмена ctx во время записи
	// ЗАКРЫВАЕТ соединение целиком (setupWriteTimeout → c.close()): кадр мог
	// уйти частично, и это единственный честный выход. Поэтому:
	//   1) таймаут отсчитывается ПОСЛЕ захвата права записи — иначе очередь
	//      за wmu сжигала бы чужой бюджет, и истёкший в очереди ctx убивал бы
	//      живое соединение;
	//   2) запись идёт на контексте, отвязанном от отмены вызвавшего
	//      (WithoutCancel): общий канал не должен умирать из-за того, что один
	//      из отправителей бросил ждать. Отмена вызвавшего проверяется явно до
	//      начала записи.
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	conn := c.conn
	blocked := c.status.BlockedReason
	c.mu.Unlock()
	if conn == nil {
		c.noteSendError(ErrNotConnected)
		if blocked != "" {
			return errors.New(blocked)
		}
		return ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		c.noteSendError(err)
		return err
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, data); err != nil {
		c.noteSendError(err)
		return err
	}
	c.mu.Lock()
	c.status.Sent++
	c.mu.Unlock()
	return nil
}

// Run держит соединение до отмены ctx: подключение → подписка → цикл чтения →
// переподключение с экспоненциальной выдержкой и джиттером. Блокирующий.
func (c *Client) Run(ctx context.Context) {
	defer c.setDisconnected()
	delay := c.cfg.ReconnectInitial
	attempt := 0
	for ctx.Err() == nil {
		if reason := c.gateReason(); reason != "" {
			c.setBlocked(reason)
			// Предохранитель переключается без рестарта — перепроверяем с
			// постоянным шагом, выдержку переподключения не растим.
			sleepCtx(ctx, c.cfg.ReconnectInitial)
			continue
		}
		// Предохранитель открыт — «заблокировано» снимается до попытки dial,
		// иначе при недоступном сервере монитор бесконечно показывал бы
		// причину-предохранитель вместо настоящей ошибки подключения.
		c.clearBlocked()
		if attempt > 0 {
			c.mu.Lock()
			c.status.Reconnects++
			c.mu.Unlock()
		}
		attempt++
		started := time.Now()
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		c.noteError(err)
		if time.Since(started) >= steadyReset {
			delay = c.cfg.ReconnectInitial
		}
		sleepCtx(ctx, jitter(delay))
		delay *= 2
		if delay > c.cfg.ReconnectMax {
			delay = c.cfg.ReconnectMax
		}
	}
}

// runOnce — одна жизнь соединения: dial, подписка, чтение до ошибки.
func (c *Client) runOnce(ctx context.Context) error {
	var header http.Header
	if c.cfg.Header != nil {
		var herr error
		header, herr = c.cfg.Header()
		if herr != nil {
			return herr
		}
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, resp, err := websocket.Dial(dctx, c.cfg.URL, &websocket.DialOptions{HTTPHeader: header})
	cancel()
	// Библиотека закрывает тело сама («You never need to close resp.Body
	// yourself»), явный Close — идемпотентная страховка для линтера bodyclose.
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	if c.cfg.MaxMessageBytes > 0 {
		conn.SetReadLimit(c.cfg.MaxMessageBytes)
	}
	if len(c.cfg.Subscribe) > 0 {
		wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
		err = conn.Write(wctx, websocket.MessageText, c.cfg.Subscribe)
		wcancel()
		if err != nil {
			return err
		}
	}
	c.setConnected(conn)
	defer c.setDisconnected()
	c.logf("вебсокет %s: подключено к %s", c.cfg.Name, c.cfg.URL)

	for {
		_, data, rerr := conn.Read(ctx)
		if rerr != nil {
			return rerr
		}
		c.mu.Lock()
		c.status.Received++
		c.status.LastMessageAt = time.Now()
		c.mu.Unlock()
		// Предохранитель могли выключить на живом соединении: рвём его и не
		// обрабатываем уже прочитанное — рубильник важнее одного сообщения.
		if reason := c.gateReason(); reason != "" {
			c.logf("вебсокет %s: соединение закрыто предохранителем, сообщение отброшено", c.cfg.Name)
			return errors.New(reason)
		}
		if herr := c.callOnMessage(ctx, data); herr != nil {
			c.mu.Lock()
			c.status.HandlerErrors++
			c.status.LastError = herr.Error()
			c.mu.Unlock()
			c.logf("вебсокет %s: сообщение не принято: %v", c.cfg.Name, herr)
		}
	}
}

// callOnMessage прикрывает обработчик recover-ом: OnMessage исполняет
// произвольный прикладной код, а горутина соединения — вершина стека. Без
// recover Go-паника обработчика (не DSL-исключение — их гасит интерпретатор)
// валила бы весь процесс базы; у http-транспорта ту же роль играет
// per-request recover HTTP-сервера. Crash-loop при повторной доставке того же
// конверта после рестарта — отдельная причина не давать панике выйти наружу.
func (c *Client) callOnMessage(ctx context.Context, raw []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("паника обработчика: %v\n%s", r, debug.Stack())
		}
	}()
	return c.cfg.OnMessage(ctx, raw)
}

func (c *Client) gateReason() string {
	if c.cfg.Gate == nil {
		return ""
	}
	return c.cfg.Gate()
}

func (c *Client) setConnected(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.status.Connected = true
	c.status.BlockedReason = ""
	c.status.ConnectedSince = time.Now()
	c.mu.Unlock()
}

func (c *Client) setDisconnected() {
	c.mu.Lock()
	c.conn = nil
	c.status.Connected = false
	c.status.ConnectedSince = time.Time{}
	c.mu.Unlock()
}

// clearBlocked снимает «заблокировано предохранителем», не трогая остальное
// состояние: дальше причиной простоя может стать обычная ошибка подключения.
func (c *Client) clearBlocked() {
	c.mu.Lock()
	c.status.BlockedReason = ""
	c.mu.Unlock()
}

func (c *Client) setBlocked(reason string) {
	c.mu.Lock()
	changed := c.status.BlockedReason != reason
	c.conn = nil
	c.status.Connected = false
	c.status.BlockedReason = reason
	c.mu.Unlock()
	if changed {
		c.logf("вебсокет %s: не подключаемся: %s", c.cfg.Name, reason)
	}
}

func (c *Client) noteError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.status.LastError = err.Error()
	c.mu.Unlock()
	c.logf("вебсокет %s: обрыв: %v", c.cfg.Name, err)
}

func (c *Client) noteSendError(err error) {
	c.mu.Lock()
	c.status.SendErrors++
	c.status.LastError = err.Error()
	c.mu.Unlock()
}

func (c *Client) logf(format string, args ...any) {
	if c.cfg.Logf != nil {
		c.cfg.Logf(format, args...)
	}
}

// jitter размывает выдержку в [d/2; d]: одновременный рестарт множества баз не
// даёт синхронной волны переподключений к одному серверу. crypto/rand — не ради
// криптостойкости (паузе она не нужна), а чтобы не заводить исключение G404.
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return d
	}
	n := binary.LittleEndian.Uint64(b[:]) % uint64(d/2+1)
	return d/2 + time.Duration(n)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
