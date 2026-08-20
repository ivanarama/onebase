package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivantit66/onebase/internal/backup"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

// allowNewerSchema — осознанное снятие гейта: открыть базу, обслуженную
// платформой новее этого бинаря. Привязан к постоянному флагу корневой команды
// --allow-newer-schema; переменная окружения нужна там, где флага нет.
var allowNewerSchema bool

func newerSchemaAllowed() bool {
	return allowNewerSchema || storage.AllowNewerSchemaByEnv()
}

// guardSchemaRevision не даёт бинарю работать с базой, которую обслуживала
// платформа новее его самого (issue #1057).
//
// Отказ идёт до первой операции с данными, а не в произвольном месте позже:
// в #1053 старый бинарь дошёл до входа и ответил 500, но с тем же успехом мог
// упасть на проводе документа посреди рабочего дня.
//
// Сбой самой проверки — тоже отказ: гейт, который не смог прочитать ревизию,
// обязан считать базу непроверенной, иначе он защищает ровно до первой
// неполадки связи.
func guardSchemaRevision(ctx context.Context, db *storage.DB) error {
	return handleSchemaRevisionError(db.CheckSchemaRevision(ctx))
}

// guardProbedSchemaRevision applies the same explicit-bypass policy to a
// read-only preflight performed before normal storage.Connect* setup.
func guardProbedSchemaRevision(state storage.SchemaRevisionState) error {
	return handleSchemaRevisionError(state.Check())
}

func handleSchemaRevisionError(err error) error {
	if err == nil {
		return nil
	}
	var newer *storage.NewerSchemaError
	if errors.As(err, &newer) && newerSchemaAllowed() {
		// Флаг снимает отказ, но не молчание: администратор, который открыл базу
		// старым бинарём осознанно, всё равно должен видеть это в журнале — иначе
		// следующая непонятная ошибка снова будет стоить переписки.
		oblog.Component("cli").Warn("база обслуживалась платформой новее этого бинаря — открыта по явному разрешению",
			"ревизия_базы", newer.Base,
			"ревизия_бинаря", newer.Known,
			"обслужил", newer.UpdatedBy)
		return nil
	}
	return err
}

// stampSchemaRevision поднимает ревизию базы после успешных миграций. Ошибку
// возвращает: не проштампованная база молча теряет защиту следующего запуска,
// а «ошибку игнорируем» на PostgreSQL внутри транзакции всё равно иллюзия.
func stampSchemaRevision(ctx context.Context, db *storage.DB) error {
	revision, err := db.RaiseSchemaRevision(ctx)
	if err != nil {
		return fmt.Errorf("ревизия схемы: %w", err)
	}
	// Monotonic SQL can legitimately leave a future revision in place. Never
	// discard that returned value: it is the final TOCTOU backstop if another
	// upgrader published a newer minimum-reader barrier.
	if revision > storage.SchemaRevision {
		state, stateErr := db.SchemaRevisionStateOf(ctx)
		if stateErr != nil {
			return fmt.Errorf("ревизия схемы: повторная проверка: %w", stateErr)
		}
		if err := guardProbedSchemaRevision(state); err != nil {
			return err
		}
	}
	return nil
}

// openExclusiveRecoveryStorage is the narrow bridge for DemoReset paths. A
// known future/invalid schema marker is trusted even when recovery is pending
// and refuses the old binary before normal Connect. A missing marker or an
// incomplete marker paired with a trusted pending restore is recovered first;
// recovery publishes the barrier atomically with intent deletion. Without a
// pending marker, raw setup publishes it before
// storage.Connect* gets a chance to change the DB.
// The caller must already own the exclusive lifetime lease.
func openExclusiveRecoveryStorage(ctx context.Context, dbType, sqlitePath, dsn, filesDir string) (*storage.DB, error) {
	var (
		pending bool
		state   storage.SchemaRevisionState
		err     error
	)
	if dbType == "sqlite" {
		pending, err = backup.HasPendingRestoreSQLite(ctx, sqlitePath)
	} else {
		pending, err = backup.HasPendingRestorePostgres(ctx, dsn)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect restore marker before recovery open: %w", err)
	}
	prepareAfterOpen := dbType == "sqlite" && storage.IsInMemorySQLitePath(sqlitePath)
	if !prepareAfterOpen {
		if dbType == "sqlite" {
			state, err = storage.ProbeSQLiteSchemaRevision(ctx, sqlitePath)
		} else {
			state, err = storage.ProbePostgresSchemaRevision(ctx, dsn)
		}
		if err != nil {
			return nil, err
		}
		if state.Known {
			if err := guardProbedSchemaRevision(state); err != nil {
				return nil, err
			}
		}
		if !pending && state.NeedsUpgrade() {
			if dbType == "sqlite" {
				state, err = storage.PrepareSQLiteSchemaRevision(ctx, sqlitePath)
			} else {
				state, err = storage.PreparePostgresSchemaRevision(ctx, dsn)
			}
			if err != nil {
				return nil, err
			}
		}
		if !pending {
			if err := guardProbedSchemaRevision(state); err != nil {
				return nil, err
			}
		}
	}

	var db *storage.DB
	if dbType == "sqlite" {
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, dsn)
	}
	if err != nil {
		return nil, err
	}
	if filesDir != "" {
		db.SetFilesDir(filesDir)
	}
	if pending {
		if err := backup.RecoverPendingRestore(ctx, db, db.FilesDir()); err != nil {
			db.Close()
			return nil, fmt.Errorf("recover pending restore before schema gate: %w", err)
		}
	}
	state, err = db.SchemaRevisionStateOf(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	if state.Known && state.Revision < 0 {
		db.Close()
		return nil, guardProbedSchemaRevision(state)
	}
	if state.NeedsUpgrade() {
		if err := stampSchemaRevision(ctx, db); err != nil {
			db.Close()
			return nil, err
		}
	} else if err := guardProbedSchemaRevision(state); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
