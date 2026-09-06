package entityservice

// Порты хранилища (шаг 2 ARCH-01, issue #787).
//
// Интерфейсы объявлены на стороне потребителя: здесь перечислена ровно та
// поверхность *storage.DB, которой пользуется сам сервис, — 24 метода из 314.
// internal/storage о них не знает и не меняется, *storage.DB удовлетворяет им
// как есть (см. compile-time проверку в конце файла).
//
// Зачем: раньше поле Service.Store имело тип *storage.DB, и сигнатура ничего не
// сообщала о контракте — чтобы узнать, что сервису нужно от базы, приходилось
// читать сервис целиком. Теперь набор виден объявлением, а изменение любого из
// остальных 290 методов storage.DB сервиса не задевает.
//
// Роли объявлены раздельно, чтобы будущие потребители могли зависеть от узкой
// части; поле Store пока держит совокупный Storage — это оставляет все точки
// сборки (*storage.DB присваивается в поле-интерфейс) без единой правки.

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// EntityStore — CRUD объектов и их табличных частей: всё, что относится к
// строке сущности, включая проверки перед удалением.
type EntityStore interface {
	// GetByID читает объект по id (для документов — вместе с posted).
	GetByID(ctx context.Context, entityName string, id uuid.UUID, entity *metadata.Entity) (map[string]any, error)
	// Upsert пишет поля объекта (обычная запись, версия инкрементируется).
	Upsert(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error
	// UpsertProvisional вставляет предварительную строку родителя внутри
	// транзакции, чтобы хук нового объекта мог создать потомков по FK.
	UpsertProvisional(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error
	// UpsertPreserveVersion — финальная запись такой предварительной строки
	// без продвижения _version (наружу объект остаётся версии 1).
	UpsertPreserveVersion(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error
	// UpsertVersioned пишет с проверкой ожидаемой версии; при расхождении —
	// storage.ErrVersionConflict, и ничего не записано.
	UpsertVersioned(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity, expectedVersion *int64) error
	// GetTablePartRows читает строки табличной части родителя.
	GetTablePartRows(ctx context.Context, entityName, tpName string, parentID uuid.UUID, tp metadata.TablePart) ([]map[string]any, error)
	// UpsertTablePartRows заменяет все строки табличной части родителя.
	UpsertTablePartRows(ctx context.Context, entityName, tpName string, parentID uuid.UUID, rows []map[string]any, tp metadata.TablePart) error
	// Delete удаляет строку сущности (предопределённые — ошибка).
	Delete(ctx context.Context, entityName string, id uuid.UUID) error
	// DeleteVersioned удаляет строку только при совпадении _version; при
	// расхождении возвращает storage.ErrVersionConflict.
	DeleteVersioned(ctx context.Context, entityName string, id uuid.UUID, expectedVersion int64) error
	// SetPosted выставляет признак проведения документа.
	SetPosted(ctx context.Context, entityName string, id uuid.UUID, posted bool) error
	// IsMarkedForDeletion сообщает, помечен ли объект на удаление.
	IsMarkedForDeletion(ctx context.Context, entityName string, id uuid.UUID) (bool, error)
	// CheckRefs возвращает ссылки на объект из сущностей, ТЧ и регистров —
	// предохранитель перед удалением (fail-closed).
	CheckRefs(ctx context.Context, entityName string, id uuid.UUID, src storage.RefSources) ([]storage.RefInfo, error)
}

// MovementStore — запись движений документа во все три вида регистров.
// Пустой rows означает отмену проведения: движения регистратора снимаются.
type MovementStore interface {
	// WriteMovements перезаписывает движения документа в регистре накопления.
	WriteMovements(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, reg *metadata.Register, period *time.Time) error
	// WriteInfoMovements перезаписывает движения документа в регистре сведений.
	WriteInfoMovements(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, ir *metadata.InfoRegister, period *time.Time) error
	// WriteAccountMovements перезаписывает проводки документа в бухрегистре
	// (вместе с итогами в той же транзакции).
	WriteAccountMovements(ctx context.Context, regName, docType string, docID uuid.UUID, rows []map[string]any, ar *metadata.AccountRegister, period *time.Time) error
}

// TxManager — управление транзакцией вокруг записи и проведения.
type TxManager interface {
	// WithTxScope делает fn атомарным даже внутри чужой транзакции
	// (верхний уровень — транзакция, вложенный — savepoint).
	WithTxScope(ctx context.Context, fn func(context.Context) error) error
	// WithTx выполняет fn в транзакции: ошибка — откат, успех — фиксация.
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// Locker — advisory-блокировки на время транзакции (на SQLite — no-op).
type Locker interface {
	// AdvisoryXactLock берёт блокировки по логическим ключам до конца транзакции.
	AdvisoryXactLock(ctx context.Context, keys []string) error
}

// NumberGenerator — нумерация документов и кодов справочников.
type NumberGenerator interface {
	// GenerateNumber выдаёт следующий номер по блоку numerator сущности.
	GenerateNumber(ctx context.Context, entity *metadata.Entity, fields map[string]any) (string, error)
	// NextNum атомарно увеличивает и возвращает счётчик сущности.
	NextNum(ctx context.Context, entityName string) (int64, error)
}

// PostingLockReader — дата запрета проведения (закрытый период).
type PostingLockReader interface {
	// GetPostingLockDate возвращает дату запрета и признак её наличия.
	GetPostingLockDate(ctx context.Context) (time.Time, bool)
}

// RawExecutor — сырой SQL. Нужен ровно в одном месте: удаление строк табличных
// частей перед удалением родителя, потому что в storage нет метода вида
// DeleteTablePartRows. Как только он появится, этот порт удаляется целиком —
// это единственное место, где сервис ходит мимо CRUD-слоя.
type RawExecutor interface {
	// Exec выполняет не-выборку, уважая транзакцию в ctx.
	Exec(ctx context.Context, sqlText string, args ...any) (storage.CommandTag, error)
	// Dialect отдаёт диалект SQL — нужен для расстановки плейсхолдеров.
	Dialect() storage.Dialect
}

// DefaultsStore — чтение, нужное значениям по умолчанию (план 153): значение
// константы для `default: константа.X` и список элементов справочника для
// `default: единственный`.
type DefaultsStore interface {
	// GetConstant читает текущее значение константы.
	GetConstant(ctx context.Context, name string) (any, error)
	// List читает строки сущности; дефолтам нужен предел в две строки и
	// предикат строкового доступа.
	List(ctx context.Context, entityName string, entity *metadata.Entity, params storage.ListParams) ([]map[string]any, error)
}

// Storage — совокупный порт, который держит Service в поле Store.
type Storage interface {
	EntityStore
	MovementStore
	TxManager
	Locker
	NumberGenerator
	PostingLockReader
	RawExecutor
	DefaultsStore
}

// *storage.DB обязан удовлетворять порту без единой правки в internal/storage.
// Если сигнатура там разойдётся с портом, сломается эта строка с внятным
// сообщением, а не полсотни мест вызова.
var _ Storage = (*storage.DB)(nil)
