// Package intake — платформенный примитив надёжной приёмки входящих событий
// (план 90, пробел G6). Даёт две гарантии, недостижимые прикладным паттерном:
//
//   - идемпотентность через атомарный insert-if-new ключа (storage: UNIQUE +
//     ON CONFLICT DO NOTHING) — конкурентные дубли не создают двух бизнес-
//     объектов, нет гонки «проверил-потом-создал»;
//   - обработчик и отметка «обработано» в одной транзакции (storage.WithTx) —
//     сбой обработчика откатывает бизнес-объект целиком, полу-записи невозможны;
//     упавшее событие уходит в карантин (DLQ) для ручного replay.
//
// Транспорт остаётся за швом MessageSource: HTTP — эталон (план 61), нативный
// AMQP-consumer (G4/путь Б) подключается позже, не трогая это ядро.
package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Status — бизнес-исход приёмки (в отличие от транспортного ack).
type Status string

const (
	StatusAccepted    Status = "Принято"   // новое событие обработано, бизнес-объект создан
	StatusDuplicate   Status = "Дубль"     // событие уже принято ранее, повторной обработки нет
	StatusQuarantined Status = "Карантин"  // событие отложено в DLQ (сбой/несовпадение), ждёт replay
	StatusRejected    Status = "Отклонено" // событие отвергнуто без карантина (например нет ключа)
)

// Result — результат Ingest/Replay.
type Result struct {
	Status         Status
	ResultRef      string         // ссылка на созданный бизнес-объект (при Принято/Дубль)
	BusinessResult map[string]any // произвольный бизнес-результат обработчика
	Reason         string         // причина карантина/отклонения (dlq reason или тех. причина)
	DLQID          string         // id записи карантина, если создана
}

// HandlerResult — то, что обработчик возвращает движку.
type HandlerResult struct {
	Ref            string         // ссылка на созданный бизнес-объект (например UUID Заявки)
	BusinessResult map[string]any // произвольный бизнес-результат (для business_result и ответа)
}

// Handler — шов обработчика. Handle создаёт бизнес-объект, используя ctx (в нём
// уже открыта транзакция приёмки — все записи через storage к ней присоединятся,
// storage.WithTxIfNeeded). Ошибка Handle откатывает транзакцию и отправляет
// событие в карантин. Прикладная реализация запускает DSL-процедуру Обработать.
type Handler interface {
	Handle(ctx context.Context, env Envelope) (HandlerResult, error)
}

// HandlerFunc адаптирует функцию к интерфейсу Handler.
type HandlerFunc func(ctx context.Context, env Envelope) (HandlerResult, error)

// Handle реализует Handler.
func (f HandlerFunc) Handle(ctx context.Context, env Envelope) (HandlerResult, error) {
	return f(ctx, env)
}

// MessageSource — шов транспорта. Реализация принимает сырые сообщения из своего
// канала (HTTP-запрос, AMQP-доставка) и для каждого зовёт Engine.Ingest, затем
// ack/nack по Result. Ядро приёмки от транспорта не зависит; http реализуется
// первым (план 61), amqp (G4/путь Б) — позже, без правки Ingest.
type MessageSource interface {
	// Name — имя шлюза, чьи сообщения поставляет источник.
	Name() string
}

// Envelope — разобранный единый конверт входящего события.
type Envelope struct {
	Top     map[string]any // весь конверт (event_id, source, aggregate, payload, …)
	Payload map[string]any // тело события (конверт.payload)
	Raw     []byte         // исходные байты — сохраняются в карантин как есть
}

// ParseEnvelope разбирает сырой JSON-конверт.
func ParseEnvelope(raw []byte) (Envelope, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return Envelope{}, fmt.Errorf("intake: разбор конверта: %w", err)
	}
	env := Envelope{Top: top, Raw: raw}
	if p, ok := top["payload"].(map[string]any); ok {
		env.Payload = p
	} else {
		env.Payload = map[string]any{}
	}
	return env, nil
}

// Field возвращает строковое значение top-level поля конверта ("" если нет).
func (e Envelope) Field(name string) string {
	v, ok := e.Top[name]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// CorrelationID — сквозной идентификатор трассировки из конверта.
func (e Envelope) CorrelationID() string { return e.Field("correlation_id") }

// Engine исполняет приёмку поверх хранилища.
type Engine struct {
	Store        *storage.DB
	Now          func() time.Time // инъекция часов для тестов; nil → time.Now
	InFlightWait time.Duration    // ожидание конкурентного received; 0 → 5 секунд
}

// New создаёт движок приёмки.
func New(store *storage.DB) *Engine { return &Engine{Store: store} }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) inFlightWait() time.Duration {
	if e.InFlightWait > 0 {
		return e.InFlightWait
	}
	return 5 * time.Second
}

// Ingest — приём одного события. Машина состояний:
//  1. канонизировать payload → payload_hash;
//  2. атомарный insert-if-new строки идемпотентности по (intake, scope, key);
//  3. строка новая → в одной транзакции запустить обработчик и отметить
//     processed; ошибка обработчика → откат + карантин;
//  4. строка есть, hash совпал → прежний результат (Дубль, без обработки);
//  5. строка есть, hash не совпал → карантин (schema_mismatch).
func (e *Engine) Ingest(ctx context.Context, in *metadata.Intake, h Handler, env Envelope) (Result, error) {
	key := env.Field(in.Idempotency.Key)
	if key == "" {
		// Без ключа дедупликация невозможна — отвергаем, чтобы отправитель починил
		// конверт (а не молча схлопывать все события в одну строку).
		return Result{Status: StatusRejected, Reason: "missing_idempotency_key"}, nil
	}
	scope, err := e.resolveScope(in, env)
	if err != nil {
		return Result{Status: StatusRejected, Reason: err.Error()}, nil
	}
	hash, err := payloadHash(env.Payload)
	if err != nil {
		return Result{}, err
	}

	ttlSec, err := in.TTLSeconds()
	if err != nil {
		return Result{}, err
	}
	now := e.now().Unix()
	ttlExpires := int64(0)
	if ttlSec > 0 {
		ttlExpires = now + ttlSec
	}

	inserted, err := e.Store.InsertIntakeLogIfNew(ctx, storage.IntakeLogRow{
		Intake:        in.Name,
		Scope:         scope,
		Key:           key,
		PayloadHash:   hash,
		Status:        storage.IntakeStatusReceived,
		CorrelationID: env.CorrelationID(),
		ReceivedAt:    now,
		TTLExpiresAt:  ttlExpires,
	})
	if err != nil {
		return Result{}, err
	}

	if in.SchemaVersion != "" && env.Field("schema_version") != in.SchemaVersion {
		return e.quarantine(ctx, in, env, key, scope, metadata.DLQSchemaMismatch,
			fmt.Sprintf("schema_version: ожидалась %q, получена %q", in.SchemaVersion, env.Field("schema_version")),
			inserted)
	}
	if !inserted {
		return e.onExisting(ctx, in, h, env, key, scope, hash)
	}
	return e.process(ctx, in, h, env, key, scope)
}

// onExisting разбирает случай «ключ уже занят»: дубль, карантин или несовпадение.
func (e *Engine) onExisting(ctx context.Context, in *metadata.Intake, h Handler, env Envelope, key, scope, hash string) (Result, error) {
	row, found, err := e.Store.GetIntakeLog(ctx, in.Name, scope, key)
	if err != nil {
		return Result{}, err
	}
	if !found {
		// Гонка с TTL-очисткой между insert-конфликтом и чтением — редкий случай,
		// сообщаем ошибкой (транспорт переспросит).
		return Result{}, fmt.Errorf("intake %q: строка идемпотентности исчезла после конфликта", in.Name)
	}
	if row.PayloadHash != hash {
		// Тот же ключ, другое тело — несовпадение. Прежняя строка остаётся как есть,
		// оскорбительное тело — в карантин на разбор.
		return e.quarantine(ctx, in, env, key, scope, metadata.DLQSchemaMismatch,
			fmt.Sprintf("payload_hash: было %s, стало %s", shortHash(row.PayloadHash), shortHash(hash)), false)
	}
	if row.Status == storage.IntakeStatusQuarantined {
		if in.DLQ.RetryBySender {
			// Повтор отправителя после 503: переигрываем открытую запись
			// handler_error тем же атомарным Replay, что и ручной разбор.
			entry, found, err := e.Store.FindOpenIntakeDLQ(ctx, in.Name, scope, key, metadata.DLQHandlerError)
			if err != nil {
				return Result{}, err
			}
			if found {
				return e.Replay(ctx, in, h, entry.ID)
			}
		}
		return Result{Status: StatusQuarantined, Reason: "already_quarantined", ResultRef: row.ResultRef}, nil
	}
	if row.Status == storage.IntakeStatusReceived {
		return e.waitForExisting(ctx, in.Name, scope, key, hash)
	}
	if row.Status != storage.IntakeStatusProcessed {
		return Result{}, fmt.Errorf("intake %q: неизвестный статус ключа %q: %q", in.Name, key, row.Status)
	}
	return resultFromLog(row, StatusDuplicate), nil
}

func (e *Engine) waitForExisting(ctx context.Context, intakeName, scope, key, hash string) (Result, error) {
	timer := time.NewTimer(e.inFlightWait())
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-timer.C:
			return Result{}, fmt.Errorf("intake %q: обработка ключа %q ещё не завершена", intakeName, key)
		case <-ticker.C:
			row, found, err := e.Store.GetIntakeLog(ctx, intakeName, scope, key)
			if err != nil {
				return Result{}, err
			}
			if !found {
				return Result{}, fmt.Errorf("intake %q: строка ключа %q исчезла во время обработки", intakeName, key)
			}
			if row.PayloadHash != hash {
				return Result{}, fmt.Errorf("intake %q: строка ключа %q была заменена во время обработки", intakeName, key)
			}
			switch row.Status {
			case storage.IntakeStatusReceived:
				continue
			case storage.IntakeStatusProcessed:
				return resultFromLog(row, StatusDuplicate), nil
			case storage.IntakeStatusQuarantined:
				return Result{Status: StatusQuarantined, Reason: "already_quarantined", ResultRef: row.ResultRef}, nil
			default:
				return Result{}, fmt.Errorf("intake %q: неизвестный статус ключа %q: %q", intakeName, key, row.Status)
			}
		}
	}
}

func resultFromLog(row storage.IntakeLogRow, status Status) Result {
	var br map[string]any
	if row.BusinessResult != "" {
		_ = json.Unmarshal([]byte(row.BusinessResult), &br)
	}
	return Result{Status: status, ResultRef: row.ResultRef, BusinessResult: br}
}

// process запускает обработчик и отметку processed в одной транзакции. Ошибка
// обработчика откатывает бизнес-объект и отправляет событие в карантин.
func (e *Engine) process(ctx context.Context, in *metadata.Intake, h Handler, env Envelope, key, scope string) (Result, error) {
	var hr HandlerResult
	txErr := e.Store.WithTx(ctx, func(txctx context.Context) error {
		r, herr := h.Handle(txctx, env)
		if herr != nil {
			return herr
		}
		hr = r
		return e.Store.SetIntakeLogProcessed(txctx, in.Name, scope, key, r.Ref, marshalResult(r.BusinessResult))
	})
	if txErr != nil {
		return e.quarantine(ctx, in, env, key, scope, metadata.DLQHandlerError, txErr.Error(), true)
	}
	return Result{Status: StatusAccepted, ResultRef: hr.Ref, BusinessResult: hr.BusinessResult}, nil
}

// quarantine отправляет событие в карантин. ourRow=true — сбой на нашей свежей
// received-строке (её надо перевести в quarantined); false — несовпадение по
// чужой строке (её не трогаем). Если причина не входит в dlq.on — событие
// отклоняется без карантина, а осиротевшая received-строка снимается.
func (e *Engine) quarantine(ctx context.Context, in *metadata.Intake, env Envelope, key, scope, reason, errMsg string, ourRow bool) (Result, error) {
	if !in.QuarantineOn(reason) {
		if ourRow {
			if err := e.Store.DeleteIntakeLog(ctx, in.Name, scope, key); err != nil {
				return Result{}, err
			}
		}
		return Result{Status: StatusRejected, Reason: reason}, nil
	}
	var dlqID string
	err := e.Store.WithTx(ctx, func(txctx context.Context) error {
		id, err := e.Store.InsertIntakeDLQ(txctx, storage.IntakeDLQEntry{
			Intake:        in.Name,
			Key:           key,
			Scope:         scope,
			Payload:       string(env.Raw),
			Reason:        reason,
			Error:         errMsg,
			CorrelationID: env.CorrelationID(),
			QuarantinedAt: e.now().Unix(),
		})
		if err != nil {
			return err
		}
		dlqID = id
		if ourRow {
			return e.Store.SetIntakeLogStatus(txctx, in.Name, scope, key, storage.IntakeStatusQuarantined)
		}
		return nil
	})
	if err != nil {
		// Иначе транспорт получит 5xx, но его retry увидит вечную received-строку
		// и будет ошибочно подтверждён как дубль без записи в DLQ.
		if ourRow {
			if cleanupErr := e.Store.DeleteIntakeLog(ctx, in.Name, scope, key); cleanupErr != nil {
				return Result{}, errors.Join(err, cleanupErr)
			}
		}
		return Result{}, err
	}
	return Result{Status: StatusQuarantined, Reason: reason, DLQID: dlqID}, nil
}

var errReplayAlreadyClaimed = errors.New("intake: запись карантина уже обработана")

// Replay переигрывает открытую запись карантина по её уникальному ID.
// Безопасно повторяются только handler_error: у schema_mismatch/key_conflict
// исходная строка ключа может уже указывать на успешно созданный объект.
//
// Захват DLQ, обработчик и переход строки идемпотентности в processed входят в
// одну транзакцию. Поэтому сбой закрытия DLQ не может оставить созданный объект
// при открытой записи, а два конкурентных replay не выполнят обработчик дважды.
func (e *Engine) Replay(ctx context.Context, in *metadata.Intake, h Handler, dlqID string) (Result, error) {
	entry, found, err := e.Store.GetIntakeDLQ(ctx, in.Name, dlqID)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("intake %q: запись карантина %q не найдена", in.Name, dlqID)
	}
	if entry.ReplayState == storage.DLQStateReplayed {
		return e.replayDuplicate(ctx, entry)
	}
	if entry.ReplayState != storage.DLQStateOpen {
		return Result{}, fmt.Errorf("intake %q: запись карантина %q имеет состояние %q", in.Name, dlqID, entry.ReplayState)
	}
	if entry.Reason != metadata.DLQHandlerError {
		return Result{}, fmt.Errorf("intake %q: карантин %s нельзя повторить автоматически; требуется разбор конфликта",
			in.Name, entry.Reason)
	}
	env, err := ParseEnvelope([]byte(entry.Payload))
	if err != nil {
		return Result{}, err
	}
	hash, err := payloadHash(env.Payload)
	if err != nil {
		return Result{}, err
	}
	if err := e.validateReplayLog(ctx, entry, hash); err != nil {
		return Result{}, err
	}

	var hr HandlerResult
	handlerFailed := false
	txErr := e.Store.WithTx(ctx, func(txctx context.Context) error {
		claimed, err := e.Store.MarkIntakeDLQReplayedIfOpen(txctx, entry.ID)
		if err != nil {
			return err
		}
		if !claimed {
			return errReplayAlreadyClaimed
		}
		logClaimed, err := e.Store.ClaimIntakeLogForReplay(
			txctx, entry.Intake, entry.Scope, entry.Key, hash)
		if err != nil {
			return err
		}
		if !logClaimed {
			return fmt.Errorf("intake %q: строку ключа %q не удалось захватить для replay", entry.Intake, entry.Key)
		}
		r, herr := h.Handle(txctx, env)
		if herr != nil {
			handlerFailed = true
			return herr
		}
		hr = r
		return e.Store.SetIntakeLogProcessed(txctx, in.Name, entry.Scope, entry.Key, r.Ref, marshalResult(r.BusinessResult))
	})
	if txErr != nil {
		if errors.Is(txErr, errReplayAlreadyClaimed) {
			return e.replayDuplicate(ctx, entry)
		}
		if !handlerFailed {
			return Result{}, txErr
		}
		if err := e.Store.BumpIntakeDLQAttempts(ctx, entry.ID, txErr.Error()); err != nil {
			return Result{}, err
		}
		return Result{Status: StatusQuarantined, Reason: metadata.DLQHandlerError, DLQID: entry.ID}, nil
	}
	return Result{Status: StatusAccepted, ResultRef: hr.Ref, BusinessResult: hr.BusinessResult}, nil
}

func (e *Engine) validateReplayLog(ctx context.Context, entry storage.IntakeDLQEntry, hash string) error {
	row, found, err := e.Store.GetIntakeLog(ctx, entry.Intake, entry.Scope, entry.Key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("intake %q: строка идемпотентности для карантина %q не найдена", entry.Intake, entry.ID)
	}
	if row.Status != storage.IntakeStatusQuarantined {
		return fmt.Errorf("intake %q: ключ %q имеет статус %q, replay небезопасен", entry.Intake, entry.Key, row.Status)
	}
	if row.PayloadHash != hash {
		return fmt.Errorf("intake %q: payload карантина %q не совпадает со строкой идемпотентности", entry.Intake, entry.ID)
	}
	return nil
}

func (e *Engine) replayDuplicate(ctx context.Context, entry storage.IntakeDLQEntry) (Result, error) {
	row, found, err := e.Store.GetIntakeLog(ctx, entry.Intake, entry.Scope, entry.Key)
	if err != nil {
		return Result{}, err
	}
	if !found || row.Status != storage.IntakeStatusProcessed {
		return Result{}, fmt.Errorf("intake %q: карантин %q закрыт без обработанного результата", entry.Intake, entry.ID)
	}
	env, err := ParseEnvelope([]byte(entry.Payload))
	if err != nil {
		return Result{}, err
	}
	hash, err := payloadHash(env.Payload)
	if err != nil {
		return Result{}, err
	}
	if row.PayloadHash != hash {
		return Result{}, fmt.Errorf("intake %q: ключ карантина %q уже переиспользован после TTL", entry.Intake, entry.ID)
	}
	return resultFromLog(row, StatusDuplicate), nil
}

// resolveScope собирает пространство ключа из полей конверта, перечисленных в
// idempotency.scope. JSON-массив сохраняет границы значений: пары ["a|b","c"]
// и ["a","b|c"] не схлопываются в один ключ.
func (e *Engine) resolveScope(in *metadata.Intake, env Envelope) (string, error) {
	if len(in.Idempotency.Scope) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(in.Idempotency.Scope))
	for _, s := range in.Idempotency.Scope {
		value := env.Field(s)
		if value == "" {
			return "", fmt.Errorf("missing_idempotency_scope:%s", s)
		}
		parts = append(parts, value)
	}
	data, err := json.Marshal(parts)
	if err != nil {
		return "", fmt.Errorf("intake: канонизация scope: %w", err)
	}
	return string(data), nil
}

// payloadHash канонизирует payload (JSON с отсортированными ключами —
// encoding/json сортирует ключи map детерминированно) и берёт sha256.
func payloadHash(payload map[string]any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("intake: канонизация payload: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func marshalResult(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// RejectError — обработчик может обернуть в неё бизнес-отказ, чтобы отличить
// «событие отвергнуто по правилам» от технического сбоя. Зарезервировано для
// стыка с валидацией (план 89) на транспортном уровне (маппинг в HTTP 422).
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return "intake reject: " + e.Reason }

// AsReject сообщает, что ошибка — бизнес-отказ RejectError.
func AsReject(err error) (*RejectError, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}
