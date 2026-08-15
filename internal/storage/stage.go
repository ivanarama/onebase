package storage

import (
	"context"
	"crypto/sha1" //nolint:gosec // G505: SHA1 берётся для СТАБИЛЬНОГО ИМЕНИ ИНДЕКСА, а не для защиты
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Этапы объекта (план 121): доступ к значению, гейт переходов и сериализация
// цикла «прочитать → проверить → записать → записать историю».
//
// Почему это лежит в слое storage. Путей записи сущности четыре
// (entityservice, DSL `Документы.X`, обмен данными, запись справочника), и
// сходятся они не в одну функцию, а в две: upsert (crud.go) и UpsertVersioned
// (optimistic_lock.go, все правки существующих объектов). Проверка, поставленная
// выше storage, была бы дефектом класса issue #611 — написана, зелёная в тестах,
// а на путях REST/DSL/импорта не вызывается.
//
// Почему цикл сериализуется. Проверка перехода читает старое значение, а решение
// принимает по прочитанному. Без блокировки два запроса читают один и тот же
// «Черновик», оба видят разрешённый переход и оба его выполняют — маршрут
// раздваивается там, где вся суть в том, что он один. На PostgreSQL строка
// берётся advisory-локом и `FOR UPDATE`, на SQLite — CAS по `_version`
// (`FOR UPDATE` там нет, а два независимых подключения к одному файлу читают
// каждый свой снимок).

// ErrStageConcurrentWrite возвращается, когда объект с объявленными этапами
// изменили между чтением и записью: решение о допустимости перехода принято по
// устаревшему состоянию, поэтому запись отвергается целиком.
var ErrStageConcurrentWrite = errors.New("storage: объект изменён параллельно, переход этапа не выполнен")

// Источники записи истории этапов.
const (
	// StageSourceLocal — обычная запись из формы, DSL или REST.
	StageSourceLocal = "local"
	// StageSourceExchange — переход приехал пакетом обмена (план 86).
	StageSourceExchange = "exchange"
	// StageSourceMigration — синтетический переход от синхронизации
	// предопределённых элементов.
	StageSourceMigration = "migration"
)

// stageWriteMode — семантика записи для доверенных writer-ов.
type stageWriteMode struct {
	// Source — что писать в историю (`local` по умолчанию).
	Source string
	// SourceRef — канонический JSON-массив с происхождением синтетического
	// перехода; пусто для обычной записи.
	SourceRef string
	// SkipAdjacency — не проверять список объявленных переходов. Ставится
	// доверенными writer-ами (обмен, синхронизация предопределённых): объект
	// прошёл маршрут в другой базе либо приезжает из конфигурации. Проверка
	// «значение объявлено» при этом остаётся.
	SkipAdjacency bool
}

type stageModeCtxKey struct{}

// withStageWriteMode помечает контекст записи доверенным writer-ом.
//
// Признак намеренно НЕ экспортируется: у обычного Upsert не должно быть
// параметра «обойти гейт», иначе обход становится доступен любому вызывающему —
// в том числе прикладному коду. Единственные, кто его ставит, — узкие writer-ы
// ниже, каждый со своим происхождением.
func withStageWriteMode(ctx context.Context, mode stageWriteMode) context.Context {
	return context.WithValue(ctx, stageModeCtxKey{}, mode)
}

// ApplyReplicatedEntity записывает объект, приехавший пакетом обмена (план 86).
//
// Это отдельная точка входа, а не флаг обычной записи: объект прошёл маршрут в
// базе-источнике по её правилам и её конфигурации, поэтому список объявленных
// переходов здесь не проверяется — иначе расхождение блоков `stages` между
// узлами рвало бы обмен. Всё остальное остаётся: значение обязано быть
// объявленным этапом, режим `strict` отвергает неизвестное, а история получает
// событие с источником `exchange` и структурным происхождением.
//
// sourceRef — канонический JSON-массив ["exchange", план, узел, номер
// сообщения]. Не строка с разделителем: имя плана или узла может содержать что
// угодно, и разбирать её обратно пришлось бы гаданием.
func (db *DB) ApplyReplicatedEntity(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity, sourceRef string) error {
	ctx = withStageWriteMode(ctx, stageWriteMode{
		Source:        StageSourceExchange,
		SourceRef:     sourceRef,
		SkipAdjacency: true,
	})
	return db.Upsert(ctx, entityName, id, fields, entity)
}

func stageModeFromCtx(ctx context.Context) stageWriteMode {
	if m, ok := ctx.Value(stageModeCtxKey{}).(stageWriteMode); ok {
		if m.Source == "" {
			m.Source = StageSourceLocal
		}
		return m
	}
	return stageWriteMode{Source: StageSourceLocal}
}

// canonicalFieldValue читает значение реквизита из карты полей записи так же,
// как это делает сам слой персистентности: сначала точное имя, затем его
// lowercase-вариант, затем любой EqualFold-вариант. Второй
// результат — присутствовал ли ключ вообще: отсутствие ключа и явное пустое
// значение — разные вещи, и гейт этапов обязан их различать.
//
// Единый accessor используют запись, аудит и проверка этапов, чтобы точное и
// lowercase-представления одного реквизита трактовались одинаково.
func canonicalFieldValue(fields map[string]any, name string) (any, bool) {
	present := false
	if v, ok := fields[name]; ok {
		present = true
		if v != nil {
			return v, true
		}
	}
	if v, ok := fields[strings.ToLower(name)]; ok {
		present = true
		if v != nil {
			return v, true
		}
	}
	for key, value := range fields {
		if strings.EqualFold(key, name) {
			present = true
			if value != nil {
				return value, true
			}
		}
	}
	return nil, present
}

// stageValueString приводит значение этапа к строке. Перечисление хранится
// строкой, но через DSL и обмен сюда доезжают и другие представления.
func stageValueString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	if p, ok := v.(*string); ok {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// stageFieldValue достаёт значение поля-этапа из карты полей записи.
func stageFieldValue(fields map[string]any, name string) (string, bool) {
	v, present := canonicalFieldValue(fields, name)
	if !present {
		return "", false
	}
	return stageValueString(v), true
}

// stageFieldID — устойчивая идентичность поля-этапа для истории.
//
// Имя реквизита идентичностью быть не может: его переименовывают, и накопленная
// история отвязалась бы от объекта. Если у поля объявлен `id` (план 81), берём
// его; иначе остаётся имя в нижнем регистре — другой устойчивой величины у
// такого поля просто нет.
func stageFieldID(f *metadata.Field) string {
	if f == nil {
		return ""
	}
	if id := strings.TrimSpace(f.ID); id != "" {
		return id
	}
	return strings.ToLower(f.Name)
}

// stageLockKey — ключ блокировки записи: одна строка одной сущности.
func stageLockKey(entityName string, id uuid.UUID) string {
	return "stage:" + strings.ToLower(entityName) + ":" + id.String()
}

// lockStageRecord берёт блокировку записи перед чтением старого значения.
//
// На PostgreSQL это transaction advisory lock: обычный `FOR UPDATE` не
// блокирует ОТСУТСТВУЮЩУЮ строку, а создание — такой же переход («» → начальный
// этап), и два параллельных создания одного id должны сериализоваться.
// На SQLite advisory-локов нет, роль защиты играет CAS по `_version` при записи.
func (db *DB) lockStageRecord(ctx context.Context, entityName string, id uuid.UUID) error {
	if !db.IsPostgres() {
		return nil
	}
	return db.AdvisoryXactLock(ctx, []string{stageLockKey(entityName, id)})
}

// StageRecordRef — ссылка на объект для пакетной блокировки.
type StageRecordRef struct {
	Entity string
	ID     uuid.UUID
}

// LockStageRecords берёт блокировки сразу на набор объектов в устойчивом
// порядке — до того, как начнётся их обработка.
//
// Нужно приёмке пакета обмена: она применяет много объектов в одной
// транзакции, и если два встречных пакета содержат одни и те же объекты в
// разном порядке, поочерёдная блокировка внутри цикла даёт классический
// взаимоблок. Порядок здесь задаётся сортировкой ключа, а не порядком объектов
// в пакете, поэтому оба пакета берут блокировки одинаково.
//
// На SQLite advisory-локов нет: там роль защиты играют CAS по ревизии и
// нормализация SQLITE_BUSY в конфликт (пакет целиком откатывается).
func (db *DB) LockStageRecords(ctx context.Context, refs []StageRecordRef) error {
	if len(refs) == 0 || !db.IsPostgres() {
		return nil
	}
	keys := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		key := stageLockKey(ref.Entity, ref.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return db.AdvisoryXactLock(ctx, keys)
}

// stageTransition — вычисленный переход поля-этапа одной записи.
type stageTransition struct {
	Field   string
	FieldID string
	From    string
	To      string
	// Violation — переход недопустим, но пропущен режимом `warn`. В историю он
	// попадает помеченным: отчёт обязан показывать то, что произошло, а «тихо
	// пропустить» значит потерять единственный след нарушения.
	Violation bool
	Source    string
	SourceRef string
}

// checkStageTransition — гейт переходов, общий для обеих точек записи.
//
// Возвращает вычисленный переход (nil — двигать нечего: сущность без этапов,
// поле не участвует в записи или значение не изменилось) и ошибку, если переход
// недопустим при enforce: strict.
func (db *DB) checkStageTransition(ctx context.Context, entityName string, entity *metadata.Entity, oldRow, fields map[string]any) (*stageTransition, error) {
	if entity == nil || entity.Stages == nil {
		return nil, nil
	}
	s := entity.Stages
	f := entity.StageField()
	if f == nil {
		return nil, nil
	}
	to, present := stageFieldValue(fields, f.Name)
	if !present {
		return nil, nil
	}
	from := ""
	if oldRow != nil {
		from = stageValueString(oldRow[f.Name])
	}
	if strings.EqualFold(from, to) {
		return nil, nil
	}
	mode := stageModeFromCtx(ctx)
	tr := &stageTransition{
		Field: f.Name, FieldID: stageFieldID(f), From: from, To: to,
		Source: mode.Source, SourceRef: mode.SourceRef,
	}

	// Доверенный writer обходит только список переходов: объект прошёл маршрут в
	// базе-источнике либо приезжает из конфигурации. Значение всё равно обязано
	// быть объявленным — иначе в базу приедет состояние, о котором ни гейт, ни
	// отчёт ничего не знают.
	if mode.SkipAdjacency {
		if to == "" || s.Known(to) {
			return tr, nil
		}
	} else if s.Allowed(from, to) {
		return tr, nil
	}

	if s.Strict() {
		return nil, stageRejectError(entityName, s, from, to)
	}
	tr.Violation = true
	// Лог — после коммита: откат транзакции или savepoint иначе оставил бы в
	// журнале запись о переходе, которого не было. Вне транзакции (хук не к чему
	// прицепить) пишем сразу — терять предупреждение нельзя.
	warn := func() {
		storageLog().Warn("недопустимый переход этапа (enforce: warn — запись пропущена)",
			"сущность", entityName, "реквизит", f.Name, "было", from, "стало", to, "источник", tr.Source)
	}
	if !DeferUntilTxCommit(ctx, warn) {
		warn()
	}
	return tr, nil
}

// stageRejectError — локализуемый отказ в переходе.
func stageRejectError(entityName string, s *metadata.Stages, from, to string) error {
	switch {
	case to == "":
		return i18nerr.Errorf("у объекта %s нельзя очистить этап «%s» — этап меняется только переходом", entityName, from)
	case !s.Known(to):
		return i18nerr.Errorf("этап «%s» не объявлен в маршруте объекта %s", to, entityName)
	case from == "":
		return i18nerr.Errorf("объект %s нельзя создать сразу на этапе «%s» — маршрут начинается с «%s»",
			entityName, to, s.Initial())
	default:
		return i18nerr.Errorf("переход «%s» → «%s» у объекта %s не разрешён", from, to, entityName)
	}
}

// stageConcurrencyErr нормализует отказ параллельной записи.
//
// SQLite при двух подключениях к одному файлу отдаёт SQLITE_BUSY/SQLITE_LOCKED —
// для вызывающего это тот же случай, что и ноль изменённых строк на CAS: между
// чтением и записью объект тронул кто-то ещё. Сырая ошибка драйвера здесь
// вредна: по ней ни форма, ни DSL не отличат «повторите» от настоящего сбоя.
// Автоматического повтора нет сознательно — выше по стеку у записи могли быть
// побочные эффекты (хук, движения), и повторять их платформа не вправе.
func stageConcurrencyErr(err error) error {
	if err == nil {
		return nil
	}
	if isSQLiteBusyErr(err) {
		return ErrStageConcurrentWrite
	}
	return err
}

// isSQLiteBusyErr распознаёт отказ по занятости: у драйвера modernc код лежит в
// ошибке (5 — SQLITE_BUSY, 6 — SQLITE_LOCKED, старший байт — уточнение),
// строковая проверка оставлена запасной на случай обёрнутой ошибки.
func isSQLiteBusyErr(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case 5, 6:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

// stageIndexName — имя индекса по полю-этапу.
//
// Идентичность строится из УСТОЙЧИВОГО идентификатора реквизита, а не из имени
// колонки: обычные индексы именуются хешем от колонок, и переименование
// реквизита оставило бы рядом второй индекс на прежнюю колонку — тот бы уже
// ничего не ускорял, но продолжал бы обновляться на каждой записи.
func stageIndexName(entityName, fieldID string) string {
	sum := sha1.Sum([]byte("stage|" + strings.ToLower(entityName) + "|" + strings.ToLower(fieldID))) //nolint:gosec // G401/G505: SHA1 берётся для СТАБИЛЬНОГО ИМЕНИ ИНДЕКСА, а не для защиты
	return "idx_stage_" + fmt.Sprintf("%x", sum[:6])
}

// ensureStageIndex создаёт (и при переименовании реквизита переносит) индекс по
// полю-этапу.
//
// Он нужен отчёту «где застряло»: тот группирует объекты по этапу, и без
// индекса по самой бизнес-таблице никакие индексы истории не спасают — сводка
// сканирует таблицу целиком.
func (db *DB) ensureStageIndex(ctx context.Context, e *metadata.Entity) error {
	if !stagedEntity(e) {
		return nil
	}
	f := e.StageField()
	table := metadata.TableName(e.Name)
	col := metadata.ColumnName(*f)
	name := stageIndexName(e.Name, stageFieldID(f))

	// Индекс с этим именем мог остаться от прежнего имени реквизита: имя
	// устойчиво, а колонка — нет.
	def, exists, err := db.indexDefinition(ctx, name)
	if err != nil {
		return fmt.Errorf("migrate %s: индекс этапа: %w", e.Name, err)
	}
	if exists && !strings.Contains(strings.ToLower(def), strings.ToLower(col)) {
		if _, err := db.Exec(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			return fmt.Errorf("migrate %s: перенос индекса этапа: %w", e.Name, err)
		}
	}
	if _, err := db.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+name+" ON "+table+" ("+col+")"); err != nil {
		return fmt.Errorf("migrate %s: индекс этапа: %w", e.Name, err)
	}
	return nil
}

// indexDefinition возвращает текст определения индекса по имени.
func (db *DB) indexDefinition(ctx context.Context, name string) (string, bool, error) {
	d := db.dialect
	q := "SELECT sql FROM sqlite_master WHERE type = 'index' AND name = " + d.Placeholder(1)
	if db.IsPostgres() {
		q = "SELECT indexdef FROM pg_indexes WHERE indexname = " + d.Placeholder(1) + " AND schemaname = ANY(current_schemas(false))"
	}
	var def *string
	if err := db.QueryRow(ctx, q, name).Scan(&def); err != nil {
		if IsNotFound(errors.Unwrap(err)) || IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if def == nil {
		return "", true, nil
	}
	return *def, true, nil
}

// stagedEntity сообщает, ведёт ли сущность этапы (и есть ли у неё поле-этап).
func stagedEntity(entity *metadata.Entity) bool {
	return entity != nil && entity.Stages != nil && entity.StageField() != nil
}
