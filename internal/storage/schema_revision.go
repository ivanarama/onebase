package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/version"
)

// Ревизия схемы (issue #1057).
//
// Платформа не проверяла, что схема базы ей по зубам. Старый бинарь молча
// открывал базу, обслуженную новой версией, и падал позже — на первой же
// операции, которой нужна незнакомая колонка, причём в произвольном месте: в
// #1053 это был вход (v0.9.3 не знает `_sessions.token_hash`, а база от v0.9.9
// объявляет колонку NOT NULL), но с тем же успехом мог быть провод документа
// посреди рабочего дня. Диагностика стоила двух заявок и переписки.
//
// Хранится именно монотонный счётчик, а не строка версии: в обращении две схемы
// именования — `vX.Y.Z` и `build-NNN`, и сравнивать их между собой некорректно
// (что новее, `build-918` или `v0.9.9`?). Гейт на таком сравнении либо врал бы,
// либо мешал.
//
// Ретроактивно это не лечит: выпущенные бинари проверки не содержат и содержать
// уже не будут. Гейт защищает будущие пары версий, начиная с этой.
//
// В универсальную выгрузку (.obz) ревизия НЕ входит намеренно — она свойство
// целевой базы, а не переносимых данных. Схему приёмника при восстановлении
// строит восстанавливающий бинарь, поэтому чужая ревизия из архива описывала бы
// не ту схему, которая получилась. Таблица начинается с подчёркивания и потому
// не попадает и в очистку прикладных таблиц: приёмник сохраняет собственную
// отметку, которая по-прежнему верно описывает его физическую схему.

// SchemaRevision — минимальная ревизия бинаря, способная безопасно работать со
// служебной схемой, которую может применить ЭТОТ бинарь.
//
// Поднимайте на единицу, когда изменение служебной схемы таково, что прежний
// бинарь на обновлённой базе работать не сможет: колонка NOT NULL без
// умолчания, переименование, сузившийся тип, новый обязательный индекс. Ревизия
// говорит не «схема изменилась», а «старый бинарь здесь сломается», поэтому
// добавление необязательной колонки её не двигает. Барьер публикуется до
// обычного Connect/DDL под exclusive lifetime lease: это намеренно
// консервативно и не оставляет crash-window со старой ревизией после ALTER.
//
// История:
//
//	1 — введение самой ревизии (#1057).
const SchemaRevision = 1

// schemaRevisionTable — служебная таблица с единственной строкой (id = 1).
const schemaRevisionTable = "_schema_revision"

// AllowNewerSchemaEnv снимает гейт там, где флага командной строки нет:
// лаунчер, GUI, сервис Windows. Флаг --allow-newer-schema делает то же самое
// для CLI.
const AllowNewerSchemaEnv = "ONEBASE_ALLOW_NEWER_SCHEMA"

// AllowNewerSchemaByEnv accepts only an explicit boolean true. A safety
// override must fail closed: values such as "0", "false", whitespace or a
// typo must not silently permit an older binary to touch a newer database.
func AllowNewerSchemaByEnv() bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(AllowNewerSchemaEnv)))
	return err == nil && allowed
}

// ErrNewerSchema — база обслуживалась платформой новее этого бинаря.
// Проверяется через errors.Is; подробности — в NewerSchemaError.
var ErrNewerSchema = errors.New("схема базы новее известной этому бинарю")

// ErrSchemaRevisionIncomplete means the marker table exists but its singleton
// row does not. Absence of the table is a legacy database; an empty table is an
// interrupted/invalid marker and must not be treated as legacy by consumers.
var ErrSchemaRevisionIncomplete = errors.New("маркер ревизии схемы не завершён")

// NewerSchemaError называет обе стороны расхождения и того, кто базу обслужил.
// Текст ошибки — то, что увидит администратор, поэтому он же и объясняет выход.
type NewerSchemaError struct {
	Base      int    // ревизия, записанная в базе
	Known     int    // ревизия, известная этому бинарю
	UpdatedBy string // чем база была обслужена (версия и платформа), если известно
}

// SchemaRevisionState is the read-only state of the durable compatibility
// marker. TableExists distinguishes a legacy database from an interrupted
// marker creation; Known reports whether the singleton row was read.
type SchemaRevisionState struct {
	Revision    int
	TableExists bool
	Known       bool
	UpdatedBy   string
}

// Check rejects malformed/incomplete markers and databases that require a
// newer binary. A missing table is the one intentional legacy case.
func (s SchemaRevisionState) Check() error {
	if !s.TableExists {
		return nil
	}
	if !s.Known {
		return ErrSchemaRevisionIncomplete
	}
	if s.Revision < 0 {
		return fmt.Errorf("storage: отрицательная ревизия схемы %d", s.Revision)
	}
	if s.Revision > SchemaRevision {
		return &NewerSchemaError{Base: s.Revision, Known: SchemaRevision, UpdatedBy: s.UpdatedBy}
	}
	return nil
}

// NeedsUpgrade reports whether this binary must durably publish its minimum
// reader revision before ordinary connection setup or any schema work. The
// caller must re-read and publish while holding the exclusive lifetime lease.
func (s SchemaRevisionState) NeedsUpgrade() bool {
	return !s.Known || s.Revision < SchemaRevision
}

func (e *NewerSchemaError) Error() string {
	msg := fmt.Sprintf("база обслуживалась платформой с ревизией схемы %d, этот бинарь знает %d (%s)",
		e.Base, e.Known, selfDescription())
	if e.UpdatedBy != "" {
		msg += fmt.Sprintf("\nбазу обслужил: %s", e.UpdatedBy)
	}
	msg += "\nОбновите платформу или откройте базу прежней версией." +
		"\nОсознанный запуск на свой страх: --allow-newer-schema (или ONEBASE_ALLOW_NEWER_SCHEMA=1)"
	return msg
}

// Is делает NewerSchemaError сравнимым с сигнальным ErrNewerSchema.
func (e *NewerSchemaError) Is(target error) bool { return target == ErrNewerSchema }

// selfDescription — «кто это говорит»: версия и путь исполняемого файла.
// Путь здесь не украшение: в #1052 пользователь скачал v0.9.9, а отвечал
// оставшийся в PATH старый бинарь, и по одной строке версии понять это было
// нельзя.
func selfDescription() string {
	s := "onebase " + version.String()
	if exe, err := os.Executable(); err == nil && exe != "" {
		s += ", " + exe
	}
	return s
}

// stamp — чем именно база обслужена: версия платформы и ОС/архитектура.
// Пишется в _schema_revision.updated_by, чтобы отказ старого бинаря называл не
// только числа, но и версию, которая базу подняла.
func stamp() string {
	return fmt.Sprintf("%s (%s/%s)", version.String(), runtime.GOOS, runtime.GOARCH)
}

// EnsureSchemaRevisionSchema создаёт таблицу ревизии. Отдельно от
// EnsureServiceSchema: гейт обязан отработать ДО того, как что-либо в базе
// будет создано, поэтому таблица заводится в момент подъёма ревизии, а не в
// общем списке служебных.
func (db *DB) EnsureSchemaRevisionSchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			revision   INTEGER NOT NULL CHECK (revision >= 0),
			updated_at %s NOT NULL,
			updated_by TEXT NOT NULL DEFAULT ''
		)`, schemaRevisionTable, d.TypeTimestamp())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("storage: create %s: %w", schemaRevisionTable, err)
	}
	return nil
}

// SchemaRevisionOf читает ревизию базы, ничего не создавая и не меняя.
//
// Читающей она обязана быть по существу: этот вызов решает, имеет ли бинарь
// право трогать базу вообще. База без таблицы (заведённая до #1057) отвечает
// ревизией 0 и known = false — отказывать таким нечем и не за что.
func (db *DB) SchemaRevisionStateOf(ctx context.Context) (SchemaRevisionState, error) {
	exists, err := db.TableExists(ctx, schemaRevisionTable)
	if err != nil {
		return SchemaRevisionState{}, fmt.Errorf("storage: ревизия схемы: %w", err)
	}
	if !exists {
		return SchemaRevisionState{}, nil
	}
	state := SchemaRevisionState{TableExists: true}
	q := fmt.Sprintf("SELECT revision, updated_by FROM %s WHERE id = %s",
		schemaRevisionTable, db.dialect.Placeholder(1))
	err = db.QueryRow(ctx, q, 1).Scan(&state.Revision, &state.UpdatedBy)
	if IsNotFound(err) {
		return state, nil
	}
	if err != nil {
		return SchemaRevisionState{}, fmt.Errorf("storage: чтение ревизии схемы: %w", err)
	}
	state.Known = true
	return state, nil
}

// SchemaRevisionOf is the compact compatibility view retained for callers
// that do not need to distinguish an absent table from an incomplete marker.
func (db *DB) SchemaRevisionOf(ctx context.Context) (revision int, known bool, updatedBy string, err error) {
	state, err := db.SchemaRevisionStateOf(ctx)
	return state.Revision, state.Known, state.UpdatedBy, err
}

// CheckSchemaRevision — гейт открытия базы. Возвращает *NewerSchemaError, если
// база обслуживалась платформой новее этого бинаря. Ревизия меньше или равная
// известной — обычный путь. Высокоуровневый протокол открытия поднимает
// минимальную ревизию до обычного Connect и любых миграций.
func (db *DB) CheckSchemaRevision(ctx context.Context) error {
	state, err := db.SchemaRevisionStateOf(ctx)
	if err != nil {
		return err
	}
	return state.Check()
}

// RaiseSchemaRevision поднимает ревизию базы до известной этому бинарю и
// возвращает ревизию, оставшуюся в базе после вызова.
//
// Монотонность держит сама СУБД: условие в ON CONFLICT не даёт понизить
// ревизию, поэтому старый бинарь, открывший базу с флагом обхода, не сможет
// молча «омолодить» её и снять защиту со следующего запуска.
func (db *DB) RaiseSchemaRevision(ctx context.Context) (int, error) {
	var revision int
	err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		if err := db.EnsureSchemaRevisionSchema(txCtx); err != nil {
			return err
		}
		d := db.dialect
		q := fmt.Sprintf(`
			INSERT INTO %s (id, revision, updated_at, updated_by)
			VALUES (1, %s, %s, %s)
			ON CONFLICT (id) DO UPDATE SET
				revision   = excluded.revision,
				updated_at = excluded.updated_at,
				updated_by = excluded.updated_by
			WHERE %s.revision < excluded.revision`,
			schemaRevisionTable, d.Placeholder(1), d.CurrentTimestampTZ(), d.Placeholder(2), schemaRevisionTable)
		if _, err := db.Exec(txCtx, q, SchemaRevision, stamp()); err != nil {
			return fmt.Errorf("storage: запись ревизии схемы: %w", err)
		}
		state, err := db.SchemaRevisionStateOf(txCtx)
		if err != nil {
			return err
		}
		if !state.Known {
			return ErrSchemaRevisionIncomplete
		}
		if state.Revision < 0 {
			return fmt.Errorf("storage: отрицательная ревизия схемы %d", state.Revision)
		}
		revision = state.Revision
		return nil
	})
	return revision, err
}
