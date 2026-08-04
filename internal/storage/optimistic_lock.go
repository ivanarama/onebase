package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
	if expectedVersion == nil {
		return db.Upsert(ctx, entityName, id, fields, entity)
	}

	d := db.dialect
	table := metadata.TableName(entityName)
	var oldRow map[string]any
	if existing, err := db.GetByID(ctx, entityName, id, entity); err == nil {
		oldRow = existing
	}

	sets := []string{}
	args := []any{}
	argIdx := 1
	for _, f := range entity.Fields {
		col := metadata.ColumnName(f)
		val, err := applyNumberSpec(f, fieldValueDialect(d, f, fields))
		if err != nil {
			return err
		}
		sets = append(sets, fmt.Sprintf("%s = %s", col, d.Placeholder(argIdx)))
		args = append(args, val)
		argIdx++
	}
	if entity.Hierarchical {
		parentIDStr := ""
		if v := fields["parent_id"]; v != nil {
			parentIDStr = fmt.Sprintf("%v", v)
		}
		if pID, err := uuid.Parse(parentIDStr); err == nil {
			if pID != id {
				if cycle, _ := db.WouldCycle(ctx, table, id, pID); cycle {
					return fmt.Errorf("нельзя переместить группу в её подчинённую группу")
				}
			}
			sets = append(sets, fmt.Sprintf("parent_id = %s", d.Placeholder(argIdx)))
			args = append(args, idArg(d, pID))
			argIdx++
		} else {
			sets = append(sets, "parent_id = NULL")
		}
		isFolder := false
		if v := fields["is_folder"]; v != nil {
			switch tv := v.(type) {
			case bool:
				isFolder = tv
			case string:
				isFolder = tv == "true"
			}
		}
		sets = append(sets, fmt.Sprintf("is_folder = %s", d.Placeholder(argIdx)))
		args = append(args, isFolder)
		argIdx++
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
		return fmt.Errorf("upsert versioned %s: %w", entityName, classifyConstraintErr(err))
	}
	if tag.RowsAffected != 1 {
		return ErrVersionConflict
	}
	// Полнотекстовый индекс (план 82) — в той же транзакции, что и запись.
	// Через этот путь идут ВСЕ правки существующих объектов (форма UI и REST
	// с If-Match всегда шлют версию, DSL выставляет её при чтении объекта), а
	// собственный UPDATE мимо upsert хук индексации не задевал: в выдаче
	// оставалось прежнее значение — в том числе стёртый пользователем телефон.
	if err := db.IndexObject(ctx, entity, id, fields); err != nil {
		return err
	}
	if oldRow != nil {
		if changes := AuditDiff(oldRow, fields, entity); len(changes) > 0 {
			db.logUpdate(ctx, string(entity.Kind), entityName, id, changes)
		}
	}
	return nil
}
