package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

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

// SchemaRevision — ревизия служебной схемы, известная ЭТОМУ бинарю.
//
// Поднимайте на единицу, когда изменение служебной схемы таково, что прежний
// бинарь на обновлённой базе работать не сможет: колонка NOT NULL без
// умолчания, переименование, сузившийся тип, новый обязательный индекс. Ревизия
// говорит не «схема изменилась», а «старый бинарь здесь сломается», поэтому
// добавление необязательной колонки её не двигает.
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

// ErrNewerSchema — база обслуживалась платформой новее этого бинаря.
// Проверяется через errors.Is; подробности — в NewerSchemaError.
var ErrNewerSchema = errors.New("схема базы новее известной этому бинарю")

// NewerSchemaError называет обе стороны расхождения и того, кто базу обслужил.
// Текст ошибки — то, что увидит администратор, поэтому он же и объясняет выход.
type NewerSchemaError struct {
	Base      int    // ревизия, записанная в базе
	Known     int    // ревизия, известная этому бинарю
	UpdatedBy string // чем база была обслужена (версия и платформа), если известно
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
			revision   INTEGER NOT NULL,
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
func (db *DB) SchemaRevisionOf(ctx context.Context) (revision int, known bool, updatedBy string, err error) {
	exists, err := db.TableExists(ctx, schemaRevisionTable)
	if err != nil {
		return 0, false, "", fmt.Errorf("storage: ревизия схемы: %w", err)
	}
	if !exists {
		return 0, false, "", nil
	}
	q := fmt.Sprintf("SELECT revision, updated_by FROM %s WHERE id = %s",
		schemaRevisionTable, db.dialect.Placeholder(1))
	err = db.QueryRow(ctx, q, 1).Scan(&revision, &updatedBy)
	if IsNotFound(err) {
		// Таблица есть, строки нет: базу успели завести, но не проштамповать.
		return 0, false, "", nil
	}
	if err != nil {
		return 0, false, "", fmt.Errorf("storage: чтение ревизии схемы: %w", err)
	}
	return revision, true, updatedBy, nil
}

// CheckSchemaRevision — гейт открытия базы. Возвращает *NewerSchemaError, если
// база обслуживалась платформой новее этого бинаря. Ревизия меньше или равная
// известной — обычный путь: миграции приведут схему в порядок и поднимут
// ревизию сами.
func (db *DB) CheckSchemaRevision(ctx context.Context) error {
	revision, known, updatedBy, err := db.SchemaRevisionOf(ctx)
	if err != nil {
		return err
	}
	if !known || revision <= SchemaRevision {
		return nil
	}
	return &NewerSchemaError{Base: revision, Known: SchemaRevision, UpdatedBy: updatedBy}
}

// RaiseSchemaRevision поднимает ревизию базы до известной этому бинарю и
// возвращает ревизию, оставшуюся в базе после вызова.
//
// Монотонность держит сама СУБД: условие в ON CONFLICT не даёт понизить
// ревизию, поэтому старый бинарь, открывший базу с флагом обхода, не сможет
// молча «омолодить» её и снять защиту со следующего запуска.
func (db *DB) RaiseSchemaRevision(ctx context.Context) (int, error) {
	if err := db.EnsureSchemaRevisionSchema(ctx); err != nil {
		return 0, err
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
	if _, err := db.Exec(ctx, q, SchemaRevision, stamp()); err != nil {
		return 0, fmt.Errorf("storage: запись ревизии схемы: %w", err)
	}
	revision, _, _, err := db.SchemaRevisionOf(ctx)
	if err != nil {
		return 0, err
	}
	return revision, nil
}
