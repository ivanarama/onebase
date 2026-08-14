package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5"
)

// ErrPostingDeletionMarked возвращается при попытке провести документ, помеченный
// на удаление. Проведённость и пометка на удаление взаимоисключающи (как в 1С).
var ErrPostingDeletionMarked = errors.New("документ помечен на удаление: проведение невозможно")

// IsMarkedForDeletion сообщает, выставлен ли deletion_mark у записи.
// Возвращает (false, nil), если записи нет.
func (db *DB) IsMarkedForDeletion(ctx context.Context, entityName string, id uuid.UUID) (bool, error) {
	d := db.dialect
	var mark bool
	err := db.QueryRow(ctx,
		fmt.Sprintf("SELECT deletion_mark FROM %s WHERE id = %s",
			metadata.TableName(entityName), d.Placeholder(1)),
		idArg(d, id),
	).Scan(&mark)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return mark, nil
}

// EnsureDeletionMark adds deletion_mark column to all entity tables if missing.
func (db *DB) EnsureDeletionMark(ctx context.Context, entities []*metadata.Entity) error {
	d := db.dialect
	typ := d.TypeBool() + " NOT NULL DEFAULT " + boolFalseLit(d)
	for _, e := range entities {
		table := metadata.TableName(e.Name)
		if err := db.AddColumnIfMissing(ctx, table, "deletion_mark", typ); err != nil {
			return fmt.Errorf("ensure deletion_mark %s: %w", e.Name, err)
		}
	}
	return nil
}

// MarkForDeletion sets or clears the deletion_mark flag for a record.
// Returns an error if the record is predefined (_is_predefined = TRUE).
func (db *DB) MarkForDeletion(ctx context.Context, entityName string, id uuid.UUID, mark bool) error {
	d := db.dialect
	table := metadata.TableName(entityName)
	if mark {
		isPredefined, err := db.isPredefinedRecord(ctx, table, id)
		if err != nil {
			return err
		}
		if isPredefined {
			return i18nerr.Errorf("нельзя пометить предопределённый элемент %s на удаление", entityName)
		}
	}
	// _version инкрементируем: пометка/снятие пометки — это изменение объекта,
	// и оптимистическая блокировка (и регистрация в планах обмена, план 86)
	// должны видеть новую ревизию.
	return db.exec(ctx,
		fmt.Sprintf("UPDATE %s SET deletion_mark = %s, _version = _version + 1 WHERE id = %s",
			table, d.Placeholder(1), d.Placeholder(2)),
		mark, idArg(d, id))
}

// RefInfo describes a referencing record.
type RefInfo struct {
	EntityName string
	FieldName  string
	Count      int
}

// RefSources — метаданные, в которых CheckRefs ищет ссылки на удаляемый объект.
//
// Интерфейс, а не набор срезов: до #855 сигнатура принимала только
// `[]*metadata.Entity`, и «полное покрытие», которое заявляли и комментарий в
// коде, и PR #801, физически не могло включать регистры — их просто некуда
// было передать. Через интерфейс вызывающий отдаёт весь реестр целиком, и
// «забыть» источник ссылок больше нельзя: под него нет параметра.
// *runtime.Registry реализует его как есть.
type RefSources interface {
	Entities() []*metadata.Entity
	Registers() []*metadata.Register
	InfoRegisters() []*metadata.InfoRegister
	AccountRegisters() []*metadata.AccountRegister
}

// CheckRefs returns all entities/fields that reference the given object.
//
// Это предохранитель перед удалением, поэтому он обязан быть fail-closed.
// Раньше ошибки Scan игнорировались: count оставался нулём, функция отвечала
// «ссылок нет», и объект удалялся, ломая ссылочную целостность. Теперь сбой
// любого подсчёта возвращается вызывающему коду, а тот отказывается удалять.
//
// Проверяются реквизиты сущностей, их табличные части И ссылочные поля всех
// трёх видов регистров (#855): измерения регистра накопления, измерения и
// ресурсы регистра сведений, субконто и ресурсы бухрегистра. Внешних ключей на
// уровне БД там нет, поэтому удалённый товар оставлял бы регистр с движениями
// по несуществующей ссылке — остатки и обороты «висели» бы на пустом месте.
func (db *DB) CheckRefs(ctx context.Context, entityName string, id uuid.UUID, src RefSources) ([]RefInfo, error) {
	d := db.dialect
	idA := idArg(d, id)
	var refs []RefInfo
	if src == nil {
		return nil, nil
	}
	for _, e := range src.Entities() {
		for _, f := range e.Fields {
			if f.RefEntity != entityName {
				continue
			}
			col := metadata.ColumnName(f)
			var count int
			if err := db.QueryRow(ctx,
				fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = %s",
					metadata.TableName(e.Name), col, d.Placeholder(1)),
				idA).Scan(&count); err != nil {
				return nil, fmt.Errorf("проверка ссылок %s.%s: %w", e.Name, f.Name, err)
			}
			if count > 0 {
				refs = append(refs, RefInfo{EntityName: e.Name, FieldName: f.Name, Count: count})
			}
		}
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if f.RefEntity != entityName {
					continue
				}
				col := metadata.ColumnName(f)
				table := metadata.TablePartTableName(e.Name, tp.Name)
				var count int
				if err := db.QueryRow(ctx,
					fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = %s", table, col, d.Placeholder(1)),
					idA).Scan(&count); err != nil {
					return nil, fmt.Errorf("проверка ссылок %s.%s.%s: %w", e.Name, tp.Name, f.Name, err)
				}
				if count > 0 {
					refs = append(refs, RefInfo{
						EntityName: e.Name + "." + tp.Name,
						FieldName:  f.Name,
						Count:      count,
					})
				}
			}
		}
	}

	// Регистры. Ссылка из измерения ничем не отличается от ссылки из реквизита:
	// объект, на который она указывает, удалять нельзя.
	type regRef struct {
		label  string // как показать источник пользователю
		table  string // где искать
		fields []metadata.Field
		column func(i int, f metadata.Field) string
	}
	byName := func(_ int, f metadata.Field) string { return metadata.ColumnName(f) }

	var sources []regRef
	for _, r := range src.Registers() {
		table := metadata.RegisterTableName(r.Name)
		sources = append(sources,
			regRef{"РегистрНакопления." + r.Name, table, r.Dimensions, byName},
			regRef{"РегистрНакопления." + r.Name, table, r.Resources, byName},
			regRef{"РегистрНакопления." + r.Name, table, r.Attributes, byName},
		)
	}
	for _, ir := range src.InfoRegisters() {
		table := metadata.InfoRegTableName(ir.Name)
		sources = append(sources,
			regRef{"РегистрСведений." + ir.Name, table, ir.Dimensions, byName},
			regRef{"РегистрСведений." + ir.Name, table, ir.Resources, byName},
		)
	}
	for _, ar := range src.AccountRegisters() {
		table := metadata.AccountRegTableName(ar.Name)
		sources = append(sources,
			regRef{"РегистрБухгалтерии." + ar.Name, table, ar.Resources, byName},
			// Субконто хранятся в колонках с номером, а не с именем поля:
			// имя колонки стабильно при переименовании субконто.
			regRef{"РегистрБухгалтерии." + ar.Name, table, ar.Subconto,
				func(i int, _ metadata.Field) string { return metadata.SubcontoColumn(i + 1) }},
		)
	}

	for _, s := range sources {
		for i, f := range s.fields {
			if f.RefEntity != entityName {
				continue
			}
			var count int
			if err := db.QueryRow(ctx,
				fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = %s", s.table, s.column(i, f), d.Placeholder(1)),
				idA).Scan(&count); err != nil {
				return nil, fmt.Errorf("проверка ссылок %s.%s: %w", s.label, f.Name, err)
			}
			if count > 0 {
				refs = append(refs, RefInfo{EntityName: s.label, FieldName: f.Name, Count: count})
			}
		}
	}
	return refs, nil
}

// ListMarked returns all records with deletion_mark=true for the given entity.
func (db *DB) ListMarked(ctx context.Context, entityName string, entity *metadata.Entity) ([]map[string]any, error) {
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	boolTrue := "TRUE"
	if db.dialect.Name() == "sqlite" {
		boolTrue = "1"
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE deletion_mark = %s", strings.Join(cols, ", "), table, boolTrue)
	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		row["id"] = normalizeValue(dest[0])
		for i, f := range entity.Fields {
			row[f.Name] = normalizeFieldValue(f, dest[i+1])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
