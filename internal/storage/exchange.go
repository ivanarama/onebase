package storage

// Хранилище планов обмена (план 86): очередь регистрации изменений (outbox),
// счётчики сообщений по узлам и код текущего узла базы.
//
// changed_at хранится как Unix-миллисекунды (BIGINT) — портируемо между SQLite
// и PostgreSQL без плясок с форматами timestamp и путешествует в пакете как
// целое; правило конфликта by_time сравнивает эти числа напрямую.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/secrets"
)

func exchangeNoRows(err error) bool {
	return IsNotFound(err)
}

// ExchangeChange — одна строка очереди регистрации: объект из состава плана,
// ждущий отправки узлу NodeCode. SentNo — номер сообщения, в котором строка была
// в последний раз выгружена (0 = ещё не выгружалась); сбрасывается в 0 при новой
// правке объекта.
type ExchangeChange struct {
	Plan       string
	ObjectType string
	ObjectID   string
	NodeCode   string
	// Kind — категория объекта очереди: "" (сущность, справочник/документ),
	// ExchangeKindConstant или ExchangeKindInfoReg. Определяет, как строка
	// сериализуется в пакет и применяется на приёмнике (у констант и регистров
	// сведений нет _version — идемпотентность идёт через _exchange_applied).
	Kind      string
	Version   int64
	Deletion  bool
	ChangedAt int64 // Unix-миллисекунды
	SentNo    int64
}

// Категории строк очереди/объектов пакета для не-сущностного состава (план 86).
const (
	ExchangeKindConstant = "constant" // глобальная константа (Константа.X)
	ExchangeKindInfoReg  = "inforeg"  // запись регистра сведений (РегистрСведений.X)
)

// ExchangePeer — счётчики обмена с одним узлом плана.
type ExchangePeer struct {
	Plan     string
	NodeCode string
	SentNo   int64 // последний выданный номер сообщения этому узлу
	AckNo    int64 // номер сообщения, приём которого узел подтвердил
	RecvNo   int64 // последний номер сообщения, принятого ОТ этого узла
}

// EnsureExchangeSchema создаёт служебные таблицы обмена. Идемпотентно.
func (db *DB) EnsureExchangeSchema(ctx context.Context) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _exchange_changes (
			plan        TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_id   TEXT NOT NULL,
			node_code   TEXT NOT NULL,
			kind        TEXT NOT NULL DEFAULT '',
			version     BIGINT NOT NULL,
			deletion    INTEGER NOT NULL DEFAULT 0,
			changed_at  BIGINT NOT NULL,
			sent_no     BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (plan, object_type, object_id, node_code)
		)`); err != nil {
		return fmt.Errorf("exchange: create _exchange_changes: %w", err)
	}
	// kind добавлена позже сущностной версии таблицы — доводим существующие базы.
	if err := db.AddColumnIfMissing(ctx, "_exchange_changes", "kind", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("exchange: add _exchange_changes.kind: %w", err)
	}
	if _, err := db.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_exchange_changes_node ON _exchange_changes (plan, node_code)`); err != nil {
		return fmt.Errorf("exchange: index _exchange_changes: %w", err)
	}
	// _exchange_applied — «водяной знак» применённых версий для состава без
	// собственного _version (константы, регистры сведений): аналог сравнения
	// _version у сущностей. changed_at последнего применённого источникового
	// изменения по (план, тип, ключ); идемпотентность и by_time опираются на него.
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _exchange_applied (
			plan        TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_id   TEXT NOT NULL,
			changed_at  BIGINT NOT NULL,
			PRIMARY KEY (plan, object_type, object_id)
		)`); err != nil {
		return fmt.Errorf("exchange: create _exchange_applied: %w", err)
	}
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _exchange_peers (
			plan      TEXT NOT NULL,
			node_code TEXT NOT NULL,
			sent_no   BIGINT NOT NULL DEFAULT 0,
			ack_no    BIGINT NOT NULL DEFAULT 0,
			recv_no   BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (plan, node_code)
		)`); err != nil {
		return fmt.Errorf("exchange: create _exchange_peers: %w", err)
	}
	// Настройка «этот узел» живёт в _settings — создаём заодно.
	return db.EnsureSettingsSchema(ctx)
}

// exchangeThisNodeKey — ключ _settings для кода текущего узла в рамках плана.
// Код per-plan: одна база может быть «center» в одном плане и «hub» в другом.
func exchangeThisNodeKey(plan string) string {
	return "exchange.this_node." + strings.ToLower(plan)
}

// GetExchangeThisNode возвращает код текущего узла базы для плана ("" если не
// задан). Отсутствие ключа/таблицы — не ошибка.
func (db *DB) GetExchangeThisNode(ctx context.Context, plan string) (string, error) {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		exchangeThisNodeKey(plan)).Scan(&v)
	if exchangeNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("exchange: read this_node: %w", err)
	}
	return strings.TrimSpace(v), nil
}

// SaveExchangeThisNode задаёт код текущего узла базы для плана (onebase exchange
// init). Пустой код удаляет настройку.
func (db *DB) SaveExchangeThisNode(ctx context.Context, plan, code string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	code = strings.TrimSpace(code)
	if code == "" {
		_, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), exchangeThisNodeKey(plan))
		return err
	}
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, exchangeThisNodeKey(plan), code); err != nil {
		return fmt.Errorf("exchange: save this_node: %w", err)
	}
	return nil
}

// exchangeTokenKey — ключ _settings для общего секрета плана (Bearer онлайн-обмена).
func exchangeTokenKey(plan string) string {
	return "exchange.token." + strings.ToLower(plan)
}

// GetExchangeToken возвращает общий токен плана для онлайн-обмена ("" если не
// задан → сетевые эндпоинты плана закрыты). Отсутствие ключа/таблицы — не ошибка.
//
// Значение может быть записано ссылкой на секрет (env:/file:/enc:, план 83) —
// разыменовываем здесь, в единственной точке чтения: у токена ровно два
// потребителя-действия, сравнить с заголовком входящего запроса и отправить в
// заголовке исходящего, и обоим нужно значение. Неразворачиваемая ссылка —
// ошибка, а не пустой токен: пустой означал бы «обмен не настроен» и тихо
// открыл бы дверь другой ветке проверки.
func (db *DB) GetExchangeToken(ctx context.Context, plan string) (string, error) {
	d := db.dialect
	var v string
	err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1),
		exchangeTokenKey(plan)).Scan(&v)
	if exchangeNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("exchange: read token: %w", err)
	}
	token, err := secrets.Default().Resolve(strings.TrimSpace(v))
	if err != nil {
		return "", fmt.Errorf("exchange: токен плана %s: %w", plan, err)
	}
	return strings.TrimSpace(token), nil
}

// SaveExchangeToken задаёт общий токен плана (одинаковый на всех участниках).
// Пустой токен удаляет настройку (онлайн-обмен по плану выключен).
func (db *DB) SaveExchangeToken(ctx context.Context, plan, token string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	token = strings.TrimSpace(token)
	if token == "" {
		_, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), exchangeTokenKey(plan))
		return err
	}
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, exchangeTokenKey(plan), token); err != nil {
		return fmt.Errorf("exchange: save token: %w", err)
	}
	return nil
}

// RegisterExchangeChange добавляет/обновляет строку очереди для одного узла.
// Новая правка (upsert по PK) обновляет версию/пометку/время и сбрасывает
// sent_no в 0 — объект нужно выгрузить заново, даже если он уже был отправлен.
func (db *DB) RegisterExchangeChange(ctx context.Context, ch ExchangeChange) error {
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _exchange_changes (plan, object_type, object_id, node_code, kind, version, deletion, changed_at, sent_no)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, 0)
		ON CONFLICT (plan, object_type, object_id, node_code) DO UPDATE SET
			kind = EXCLUDED.kind,
			version = EXCLUDED.version,
			deletion = EXCLUDED.deletion,
			changed_at = EXCLUDED.changed_at,
			sent_no = 0`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8))
	if _, err := db.Exec(ctx, q, ch.Plan, ch.ObjectType, ch.ObjectID, ch.NodeCode,
		ch.Kind, ch.Version, boolToInt(ch.Deletion), ch.ChangedAt); err != nil {
		return fmt.Errorf("exchange: register change: %w", err)
	}
	return nil
}

// PendingExchangeChanges возвращает все строки очереди для узла (в т.ч. уже
// выгруженные, но не подтверждённые — чтобы потерянный пакет переотправлялся),
// в хронологическом порядке.
func (db *DB) PendingExchangeChanges(ctx context.Context, plan, nodeCode string) ([]ExchangeChange, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT plan, object_type, object_id, node_code, kind, version, deletion, changed_at, sent_no
		FROM _exchange_changes
		WHERE plan = %s AND node_code = %s
		ORDER BY changed_at ASC, object_type ASC, object_id ASC`,
		d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, q, plan, nodeCode)
	if err != nil {
		return nil, fmt.Errorf("exchange: pending changes: %w", err)
	}
	defer rows.Close()
	var out []ExchangeChange
	for rows.Next() {
		var ch ExchangeChange
		var del int64
		if err := rows.Scan(&ch.Plan, &ch.ObjectType, &ch.ObjectID, &ch.NodeCode,
			&ch.Kind, &ch.Version, &del, &ch.ChangedAt, &ch.SentNo); err != nil {
			return nil, fmt.Errorf("exchange: scan change: %w", err)
		}
		ch.Deletion = del != 0
		out = append(out, ch)
	}
	return out, rows.Err()
}

// GetExchangeChange возвращает строку очереди для конкретного объекта и узла
// (для обнаружения встречной правки при загрузке). Отсутствие строки означает
// «локальной неотправленной правки нет»; прочие ошибки возвращаются вызывающему.
func (db *DB) GetExchangeChange(ctx context.Context, plan, objectType, objectID, nodeCode string) (ExchangeChange, bool, error) {
	d := db.dialect
	var ch ExchangeChange
	var del int64
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT plan, object_type, object_id, node_code, kind, version, deletion, changed_at, sent_no
			FROM _exchange_changes
			WHERE plan = %s AND object_type = %s AND object_id = %s AND node_code = %s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4)),
		plan, objectType, objectID, nodeCode).
		Scan(&ch.Plan, &ch.ObjectType, &ch.ObjectID, &ch.NodeCode, &ch.Kind, &ch.Version, &del, &ch.ChangedAt, &ch.SentNo)
	if exchangeNoRows(err) {
		return ExchangeChange{}, false, nil
	}
	if err != nil {
		return ExchangeChange{}, false, fmt.Errorf("exchange: read change: %w", err)
	}
	ch.Deletion = del != 0
	return ch, true, nil
}

// MarkExchangeChangesSent проставляет sent_no только той версии строки, которая
// была выбрана в пакет. Если параллельная запись уже подняла version/changed_at
// и сбросила sent_no, оптимистическое условие оставит новую правку в очереди.
func (db *DB) MarkExchangeChangesSent(ctx context.Context, changes []ExchangeChange, messageNo int64) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _exchange_changes SET sent_no = %s
		WHERE plan = %s AND object_type = %s AND object_id = %s AND node_code = %s
		  AND version = %s AND changed_at = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7))
	for _, ch := range changes {
		if _, err := db.Exec(ctx, q, messageNo, ch.Plan, ch.ObjectType, ch.ObjectID, ch.NodeCode,
			ch.Version, ch.ChangedAt); err != nil {
			return fmt.Errorf("exchange: mark sent: %w", err)
		}
	}
	return nil
}

// DeleteExchangeChange снимает конкретную встречную правку из очереди. Это
// нужно, когда конфликт разрешён в пользу входящего объекта: проигравшее
// локальное изменение больше не должно уехать источнику следующим пакетом.
func (db *DB) DeleteExchangeChange(ctx context.Context, plan, objectType, objectID, nodeCode string) error {
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _exchange_changes
		WHERE plan = %s AND object_type = %s AND object_id = %s AND node_code = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	if _, err := db.Exec(ctx, q, plan, objectType, objectID, nodeCode); err != nil {
		return fmt.Errorf("exchange: delete change: %w", err)
	}
	return nil
}

// AckExchangeChanges удаляет из очережди строки узла, выгруженные в сообщениях с
// номером ≤ uptoMessageNo (подтверждён приём), и запоминает подтверждённый номер.
// Возвращает число снятых строк.
func (db *DB) AckExchangeChanges(ctx context.Context, plan, nodeCode string, uptoMessageNo int64) (int64, error) {
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _exchange_changes
		WHERE plan = %s AND node_code = %s AND sent_no > 0 AND sent_no <= %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := db.Exec(ctx, q, plan, nodeCode, uptoMessageNo)
	if err != nil {
		return 0, fmt.Errorf("exchange: ack changes: %w", err)
	}
	if err := db.setPeerCounter(ctx, plan, nodeCode, "ack_no", uptoMessageNo); err != nil {
		return 0, err
	}
	return tag.RowsAffected, nil
}

// NextExchangeMessageNo атомарно увеличивает и возвращает номер следующего
// сообщения для узла (аналог NextNum). Безопасно при конкурентных выгрузках.
func (db *DB) NextExchangeMessageNo(ctx context.Context, plan, nodeCode string) (int64, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _exchange_peers (plan, node_code, sent_no) VALUES (%s, %s, 1)
		ON CONFLICT (plan, node_code) DO UPDATE SET sent_no = _exchange_peers.sent_no + 1
		RETURNING sent_no`,
		d.Placeholder(1), d.Placeholder(2))
	var n int64
	if err := db.QueryRow(ctx, q, plan, nodeCode).Scan(&n); err != nil {
		return 0, fmt.Errorf("exchange: next message no: %w", err)
	}
	return n, nil
}

// SetExchangeRecvNo запоминает номер последнего сообщения, принятого от узла
// (монотонно: меньшие номера не откатывают счётчик).
func (db *DB) SetExchangeRecvNo(ctx context.Context, plan, nodeCode string, recvNo int64) error {
	return db.setPeerCounter(ctx, plan, nodeCode, "recv_no", recvNo)
}

// GetExchangePeer возвращает счётчики обмена с узлом (нули, если строки нет).
func (db *DB) GetExchangePeer(ctx context.Context, plan, nodeCode string) (ExchangePeer, error) {
	d := db.dialect
	p := ExchangePeer{Plan: plan, NodeCode: nodeCode}
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT sent_no, ack_no, recv_no FROM _exchange_peers WHERE plan = %s AND node_code = %s`,
			d.Placeholder(1), d.Placeholder(2)),
		plan, nodeCode).Scan(&p.SentNo, &p.AckNo, &p.RecvNo)
	if exchangeNoRows(err) {
		return ExchangePeer{Plan: plan, NodeCode: nodeCode}, nil
	}
	if err != nil {
		return ExchangePeer{}, fmt.Errorf("exchange: read peer: %w", err)
	}
	return p, nil
}

// ExchangePeers возвращает счётчики по всем узлам плана (для exchange status).
func (db *DB) ExchangePeers(ctx context.Context, plan string) ([]ExchangePeer, error) {
	d := db.dialect
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT node_code, sent_no, ack_no, recv_no FROM _exchange_peers WHERE plan = %s ORDER BY node_code`,
			d.Placeholder(1)), plan)
	if err != nil {
		return nil, fmt.Errorf("exchange: peers: %w", err)
	}
	defer rows.Close()
	var out []ExchangePeer
	for rows.Next() {
		p := ExchangePeer{Plan: plan}
		if err := rows.Scan(&p.NodeCode, &p.SentNo, &p.AckNo, &p.RecvNo); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ExchangePendingCounts возвращает число неотправленных/неподтверждённых строк
// очереди по каждому узлу-получателю плана (для exchange status).
func (db *DB) ExchangePendingCounts(ctx context.Context, plan string) (map[string]int64, error) {
	d := db.dialect
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT node_code, COUNT(*) FROM _exchange_changes WHERE plan = %s GROUP BY node_code`,
			d.Placeholder(1)), plan)
	if err != nil {
		return nil, fmt.Errorf("exchange: pending counts: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var node string
		var cnt int64
		if err := rows.Scan(&node, &cnt); err != nil {
			return nil, err
		}
		out[node] = cnt
	}
	return out, rows.Err()
}

// setPeerCounter монотонно поднимает один счётчик узла (ack_no/recv_no) до value.
// col — доверенное имя колонки из констант этого пакета, не пользовательский ввод.
func (db *DB) setPeerCounter(ctx context.Context, plan, nodeCode, col string, value int64) error {
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _exchange_peers (plan, node_code, %s) VALUES (%s, %s, %s)
		ON CONFLICT (plan, node_code) DO UPDATE SET %s = CASE
			WHEN _exchange_peers.%s > EXCLUDED.%s THEN _exchange_peers.%s ELSE EXCLUDED.%s END`,
		col, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3),
		col, col, col, col, col)
	if _, err := db.Exec(ctx, q, plan, nodeCode, value); err != nil {
		return fmt.Errorf("exchange: set %s: %w", col, err)
	}
	return nil
}

// EntityVersion возвращает текущую ревизию (_version) объекта. Регистрация
// обмена снимает по ней версию в момент записи (в той же транзакции, поэтому
// значение уже инкрементировано Upsert'ом).
func (db *DB) EntityVersion(ctx context.Context, entityName string, id uuid.UUID) (int64, error) {
	d := db.dialect
	var v int64
	err := db.QueryRow(ctx,
		fmt.Sprintf("SELECT _version FROM %s WHERE id = %s", metadata.TableName(entityName), d.Placeholder(1)),
		idArg(d, id)).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("exchange: read version %s: %w", entityName, err)
	}
	return v, nil
}

// EntityVersionExists возвращает (_version, true), если объект есть локально,
// иначе (0, false). Ошибки чтения не маскируются под отсутствие объекта.
func (db *DB) EntityVersionExists(ctx context.Context, entityName string, id uuid.UUID) (int64, bool, error) {
	d := db.dialect
	var v int64
	err := db.QueryRow(ctx,
		fmt.Sprintf("SELECT _version FROM %s WHERE id = %s", metadata.TableName(entityName), d.Placeholder(1)),
		idArg(d, id)).Scan(&v)
	if exchangeNoRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("exchange: read object version %s: %w", entityName, err)
	}
	return v, true, nil
}

// SetExchangeObjectState принудительно выставляет системные колонки объекта при
// загрузке пакета: точную ревизию источника (_version — чтобы повторная загрузка
// была идемпотентной), пометку удаления и, для документов, posted=false (данные
// приходят непроведёнными — движения на приёмнике не переносятся, план 86).
func (db *DB) SetExchangeObjectState(ctx context.Context, entity *metadata.Entity, id uuid.UUID, version int64, deletion bool) error {
	d := db.dialect
	sets := []string{
		fmt.Sprintf("_version = %s", d.Placeholder(1)),
		fmt.Sprintf("deletion_mark = %s", d.Placeholder(2)),
	}
	args := []any{version, deletion}
	if entity.Kind == metadata.KindDocument {
		sets = append(sets, "posted = "+boolFalseLit(d))
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = %s",
		metadata.TableName(entity.Name), strings.Join(sets, ", "), d.Placeholder(3))
	args = append(args, idArg(d, id))
	if _, err := db.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("exchange: set object state %s: %w", entity.Name, err)
	}
	return nil
}

// ExchangeAppliedAt возвращает changed_at последнего применённого изменения по
// (план, тип, ключ) из _exchange_applied — «локальную версию» для состава без
// собственного _version. (0, false, nil), если ничего не применялось.
func (db *DB) ExchangeAppliedAt(ctx context.Context, plan, objectType, objectID string) (int64, bool, error) {
	d := db.dialect
	var at int64
	err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT changed_at FROM _exchange_applied WHERE plan = %s AND object_type = %s AND object_id = %s`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)),
		plan, objectType, objectID).Scan(&at)
	if exchangeNoRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("exchange: read applied watermark: %w", err)
	}
	return at, true, nil
}

// SetExchangeApplied монотонно поднимает «водяной знак» применённого изменения до
// changedAt (меньшие значения не откатывают — защита от переупорядоченной доставки).
func (db *DB) SetExchangeApplied(ctx context.Context, plan, objectType, objectID string, changedAt int64) error {
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _exchange_applied (plan, object_type, object_id, changed_at) VALUES (%s, %s, %s, %s)
		ON CONFLICT (plan, object_type, object_id) DO UPDATE SET changed_at = CASE
			WHEN _exchange_applied.changed_at > EXCLUDED.changed_at
			THEN _exchange_applied.changed_at ELSE EXCLUDED.changed_at END`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	if _, err := db.Exec(ctx, q, plan, objectType, objectID, changedAt); err != nil {
		return fmt.Errorf("exchange: set applied: %w", err)
	}
	return nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
