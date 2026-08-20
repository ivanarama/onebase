package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ErrVersionConflict возвращается из UpsertVersioned, когда фактическая
// ревизия (_version) объекта в БД не совпадает с ожидаемой — то есть
// между загрузкой формы и сохранением кто-то другой уже обновил объект.
//
// Вызывающий код (например, HTTP-handler формы редактирования) должен
// перехватывать через errors.Is и показывать пользователю сообщение
// «Объект был изменён другим пользователем, обновите форму».
var ErrVersionConflict = errors.New("storage: объект изменён другим пользователем")

// UpsertVersioned записывает объект с проверкой оптимистической блокировки.
//
// Если expectedVersion == nil — поведение идентично Upsert (без проверки).
// Используется для записи новых объектов из UI и для DSL-кода
// (Документы.X.Создать().Записать()), где код авторитативный.
//
// Если expectedVersion != nil — перед записью проверяется, что текущая
// ревизия объекта в БД совпадает с ожидаемой. Если объект уже изменён
// (другим пользователем или фоновой задачей) — возвращается
// ErrVersionConflict, ничего не пишется.
//
// Проверка версии и запись выполняются одним UPDATE ... WHERE id AND _version,
// поэтому конкурентная PostgreSQL-запись не может пройти между отдельным
// SELECT и последующим Upsert.
func (db *DB) UpsertVersioned(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity, expectedVersion *int64) error {
	// Отдельная ветка записи — отдельный вызов страховки: upsert() ниже по
	// коду сюда не заходит, и «одна точка» осталась бы заявлением.
	if err := db.enumBackstop(ctx, entity, fields); err != nil {
		return err
	}
	if expectedVersion == nil {
		return db.Upsert(ctx, entityName, id, fields, entity)
	}
	// Сущность с этапами (план 121) пишется сериализованным циклом, как и в
	// crud.go:upsert. Без этапов путь остаётся прежним — без транзакции-обёртки
	// и без блокировки.
	if !stagedEntity(entity) {
		return db.upsertVersionedInTx(ctx, entityName, id, fields, entity, expectedVersion,
			versionedWriteOptions{validateRequired: true, validateStage: true, durableEffects: true})
	}
	return db.WithTxScope(ctx, func(txCtx context.Context) error {
		return db.upsertVersionedInTx(txCtx, entityName, id, fields, entity, expectedVersion,
			versionedWriteOptions{validateRequired: true, validateStage: true, durableEffects: true})
	})
}

// UpsertPostingPreludeVersioned performs the CAS/version-bump write which a
// DSL document posting needs before OnPost. It is deliberately transaction-only
// and skips required validation only for this one intermediate row: nested hook
// writes cannot inherit the exemption. The caller must finish with
// UpsertAfterVersionBump, whose ordinary storage backstop validates the final
// post-hook state before commit.
func (db *DB) UpsertPostingPreludeVersioned(ctx context.Context, entityName string, id uuid.UUID,
	fields map[string]any, entity *metadata.Entity, expectedVersion *int64) error {
	if !HasTx(ctx) {
		return errors.New("storage: UpsertPostingPreludeVersioned requires an active transaction")
	}
	if expectedVersion == nil {
		return errors.New("storage: UpsertPostingPreludeVersioned requires an expected version")
	}
	if err := db.enumBackstop(ctx, entity, fields); err != nil {
		return err
	}
	state, err := beginWriteLifecycle(ctx,
		entityWriteLifecycleKey(db, "posting-prelude", entityName, id),
		"posting prelude "+entityName+" "+id.String())
	if err != nil {
		return err
	}
	var original map[string]any
	err = db.upsertVersionedInTx(ctx, entityName, id, fields, entity, expectedVersion,
		versionedWriteOptions{captureOld: &original})
	if err != nil {
		return err
	}
	state.original = original
	return armWriteLifecycle(ctx, state)
}

type versionedWriteOptions struct {
	validateRequired bool
	validateStage    bool
	durableEffects   bool
	captureOld       *map[string]any
}

func (db *DB) upsertVersionedInTx(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any,
	entity *metadata.Entity, expectedVersion *int64, options versionedWriteOptions) error {
	d := db.dialect
	table := metadata.TableName(entityName)
	staged := stagedEntity(entity)
	if staged {
		if err := db.lockStageRecord(ctx, entityName, id); err != nil {
			return err
		}
	}
	var oldRow map[string]any
	if existing, err := db.getByID(ctx, entityName, id, entity, staged); err != nil {
		// Как и в upsert: для сущности с этапами сбой чтения не вправе
		// притвориться отсутствием объекта — по нему принимается решение о
		// переходе.
		if staged && !IsNotFound(errors.Unwrap(err)) && !IsNotFound(err) {
			return fmt.Errorf("upsert versioned %s: чтение текущего этапа: %w", entityName, err)
		}
		if stageModeFromCtx(ctx).Source != StageSourceExchange && hasRequiredEntityFields(entity) && !IsNotFound(err) {
			return fmt.Errorf("upsert versioned %s: чтение текущих значений required-реквизитов: %w", entityName, err)
		}
	} else {
		oldRow = existing
	}

	// Устаревшая ревизия — это конфликт версий, и решается он ДО проверки
	// перехода: пользователь редактировал не то состояние, которое сейчас в
	// базе, и «недопустимый переход» сказало бы ему не про ту проблему. Ни
	// история, ни предупреждение при этом не пишутся.
	if oldRow != nil && stageReadVersion(oldRow) != *expectedVersion {
		return ErrVersionConflict
	}
	if options.captureOld != nil {
		*options.captureOld = oldRow
	}

	// UpsertVersioned — только update. Отсутствующие ключи сохраняют значения
	// прочитанной CAS-версии; проверяем полный effective-снимок. Если строки уже
	// нет, required не должен маскировать ErrVersionConflict: ноль затронутых
	// строк ниже вернёт именно конфликт версии.
	effectiveFields := effectiveEntityValues(entity, oldRow, fields)
	if options.validateRequired && oldRow != nil {
		if err := db.requiredBackstop(ctx, entity, effectiveFields); err != nil {
			return err
		}
	}

	// Гейт переходов между этапами (план 121). Точек записи две, и это вторая:
	// через неё идут ВСЕ правки существующих объектов (форма UI, REST с If-Match,
	// DSL), поэтому гейт только в upsert пропускал бы ровно тот случай, ради
	// которого он написан. Пара «проверка до записи + история после» здесь
	// обязана повторять crud.go:upsert.
	var stageTr *stageTransition
	if options.validateStage {
		var err error
		stageTr, err = db.checkStageTransition(ctx, entityName, entity, oldRow, fields)
		if err != nil {
			return err
		}
	}

	sets := []string{}
	args := []any{}
	argIdx := 1
	for _, f := range entity.Fields {
		_, given := canonicalFieldValue(fields, f.Name)
		if !given {
			continue
		}
		col := metadata.ColumnName(f)
		val, err := canonicalNumberArg(f, fieldValueDialect(d, f, fields))
		if err != nil {
			return err
		}
		sets = append(sets, fmt.Sprintf("%s = %s", col, d.Placeholder(argIdx)))
		args = append(args, val)
		argIdx++
	}
	if entity.Hierarchical {
		parentValue, parentGiven := hierarchyValue(fields, "parent_id", "родитель", "parent")
		folderValue, folderGiven := hierarchyValue(fields, "is_folder", "этогруппа", "isfolder")

		if parentGiven {
			parentIDStr := ""
			if parentValue != nil {
				parentIDStr = refUUIDString(parentValue)
			}
			if pID, err := uuid.Parse(parentIDStr); err == nil {
				if pID != id {
					if cycle, _ := db.WouldCycle(ctx, table, id, pID); cycle {
						return i18nerr.New("нельзя переместить группу в её подчинённую группу")
					}
				}
				sets = append(sets, fmt.Sprintf("parent_id = %s", d.Placeholder(argIdx)))
				args = append(args, idArg(d, pID))
				argIdx++
			} else {
				sets = append(sets, "parent_id = NULL")
			}
		}
		if folderGiven {
			isFolder := false
			switch tv := folderValue.(type) {
			case bool:
				isFolder = tv
			case string:
				isFolder = tv == "true" || tv == "Истина"
			}
			sets = append(sets, fmt.Sprintf("is_folder = %s", d.Placeholder(argIdx)))
			args = append(args, isFolder)
			argIdx++
		}
	}
	sets = append(sets, "_version = _version + 1")

	idPH := d.Placeholder(argIdx)
	args = append(args, idArg(d, id))
	argIdx++
	versionPH := d.Placeholder(argIdx)
	args = append(args, *expectedVersion)
	sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = %s AND _version = %s",
		table, strings.Join(sets, ", "), idPH, versionPH)
	tag, err := db.Exec(ctx, sql, args...)
	if err != nil {
		if staged {
			if conflict := stageConcurrencyErr(err); errors.Is(conflict, ErrStageConcurrentWrite) {
				return conflict
			}
		}
		return fmt.Errorf("upsert versioned %s: %w", entityName, classifyConstraintErr(err))
	}
	if tag.RowsAffected != 1 {
		return ErrVersionConflict
	}
	if !options.durableEffects {
		// The prelude exists only so OnPost and nested reads observe the OnWrite
		// snapshot after a successful CAS. Required, stage gates/history, FTS and
		// audit are all computed once by the matching final write from original
		// state to final state.
		return nil
	}
	// Полнотекстовый индекс (план 82) — в той же транзакции, что и запись.
	// Через этот путь идут ВСЕ правки существующих объектов (форма UI и REST
	// с If-Match всегда шлют версию, DSL выставляет её при чтении объекта), а
	// собственный UPDATE мимо upsert хук индексации не задевал: в выдаче
	// оставалось прежнее значение — в том числе стёртый пользователем телефон.
	if err := db.IndexObject(ctx, entity, id, effectiveFields); err != nil {
		return err
	}
	// История переходов (план 121) — безусловно и в той же транзакции, как в
	// crud.go:upsert.
	if err := db.logStageTransition(ctx, entityName, id, stageTr); err != nil {
		return err
	}
	if oldRow != nil {
		if changes := AuditDiff(oldRow, effectiveFields, entity); len(changes) > 0 {
			db.logUpdate(ctx, string(entity.Kind), entityName, id, changes)
		}
	}
	return nil
}
