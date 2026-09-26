package storage

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ChoicePredicate is a trusted, server-built choice_filter condition. HTTP
// clients must never populate Field or Op directly: the UI layer resolves a
// form element and constructs these values from metadata.
type ChoicePredicate struct {
	Field string
	Op    metadata.FormChoiceOperator
	Value any
}

func hasExplicitChoiceFolderScope(predicates []ChoicePredicate) bool {
	for _, predicate := range predicates {
		if strings.EqualFold(strings.TrimSpace(predicate.Field), "is_folder") {
			return true
		}
	}
	return false
}

// choicePredicateSQL compiles the deliberately small plan-170 grammar. It
// accepts only metadata field names and typed values, and always binds values
// as parameters. The returned next index follows the PredicateSQL convention.
func choicePredicateSQL(d Dialect, entity *metadata.Entity, predicates []ChoicePredicate, startArg int) (string, []any, int, error) {
	if len(predicates) == 0 {
		return "", nil, startArg, nil
	}
	if entity == nil {
		return "", nil, startArg, fmt.Errorf("choice filter: entity is nil")
	}
	if entity.Kind != metadata.KindCatalog {
		return "", nil, startArg, fmt.Errorf("choice filter: target %q is not a catalog", entity.Name)
	}
	if len(predicates) > 8 {
		return "", nil, startArg, fmt.Errorf("choice filter: %d conditions, maximum is 8", len(predicates))
	}

	parts := make([]string, 0, len(predicates))
	args := make([]any, 0, len(predicates))
	next := startArg
	seenFields := make(map[string]bool, len(predicates))
	for i, predicate := range predicates {
		fieldName := strings.TrimSpace(predicate.Field)
		if fieldName == "" {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field is empty", i)
		}
		fieldKey := strings.ToLower(fieldName)
		if seenFields[fieldKey] {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q is duplicated", i, fieldName)
		}
		seenFields[fieldKey] = true
		if strings.EqualFold(fieldName, "is_folder") {
			if !entity.Hierarchical {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_folder requires a hierarchical catalog", i)
			}
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_folder supports only eq", i)
			}
			value, ok := predicate.Value.(bool)
			if !ok {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_folder value must be boolean", i)
			}
			parts = append(parts, "is_folder = "+d.Placeholder(next))
			args = append(args, value)
			next++
			continue
		}

		// is_root — запись верхнего уровня: у неё нет родителя. Служебное поле
		// наравне с is_folder: выразить «только корневые» через ссылочные
		// реквизиты нечем, а именно так задаются регионы в адресном дереве.
		if strings.EqualFold(fieldName, "is_root") {
			if !entity.Hierarchical {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_root requires a hierarchical catalog", i)
			}
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_root supports only eq", i)
			}
			value, ok := predicate.Value.(bool)
			if !ok {
				return "", nil, startArg, fmt.Errorf("choice filter %d: is_root value must be boolean", i)
			}
			if value {
				parts = append(parts, "parent_id IS NULL")
			} else {
				parts = append(parts, "parent_id IS NOT NULL")
			}
			continue
		}

		// «ТЧ.Поле» — отбор по табличной части: запись подходит, если в её ТЧ
		// есть хотя бы одна строка с нужным значением. Так выражается связь
		// многие-ко-многим, которой в самой записи нет: бренд обслуживает
		// несколько направлений, и одним реквизитом это не описать.
		if tpName, tpField, ok := strings.Cut(fieldName, "."); ok {
			tp := choiceTablePart(entity, tpName)
			if tp == nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part %q does not exist", i, tpName)
			}
			column := choiceTablePartField(tp, tpField)
			if column == nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part %q has no field %q", i, tpName, tpField)
			}
			if strings.TrimSpace(column.RefEntity) == "" {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part field %q is not a reference", i, fieldName)
			}
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part supports only eq", i)
			}
			id, err := choiceUUID(predicate.Value)
			if err != nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d field %q: %w", i, fieldName, err)
			}
			parts = append(parts, fmt.Sprintf(`EXISTS (SELECT 1 FROM %s AS tp WHERE tp.parent_id = %s.id AND tp.%s = %s)`,
				metadata.TablePartTableName(entity.Name, tp.Name),
				metadata.TableName(entity.Name),
				metadata.ColumnName(*column),
				d.Placeholder(next)))
			args = append(args, idArg(d, id))
			next++
			continue
		}

		// parent_id — собственная иерархия справочника-цели, а не ссылочный
		// реквизит. Отбирает по МЕСТУ САМОЙ ЗАПИСИ в дереве: «эта запись лежит
		// (или не лежит) внутри такой-то группы». Без этого нельзя было
		// исключить архивную папку: is_folder убирает саму группу, но не её
		// содержимое, а обойти иерархию через ссылочные реквизиты нечем.
		if strings.EqualFold(fieldName, "parent_id") {
			if !entity.Hierarchical {
				return "", nil, startArg, fmt.Errorf("choice filter %d: parent_id requires a hierarchical catalog", i)
			}
			if predicate.Op != metadata.FormChoiceOpInHierarchy && predicate.Op != metadata.FormChoiceOpNotInHierarchy {
				return "", nil, startArg, fmt.Errorf("choice filter %d: parent_id supports only in_hierarchy and not_in_hierarchy", i)
			}
			id, err := choiceUUID(predicate.Value)
			if err != nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d field %q: %w", i, fieldName, err)
			}
			table := metadata.TableName(entity.Name)
			op := "IN"
			if predicate.Op == metadata.FormChoiceOpNotInHierarchy {
				op = "NOT IN"
			}
			parts = append(parts, fmt.Sprintf(`id `+op+` (
				WITH RECURSIVE choice_tree(id) AS (
					SELECT id FROM %s WHERE id = %s
					UNION
					SELECT child.id FROM %s AS child
					JOIN choice_tree AS parent ON child.parent_id = parent.id
				)
				SELECT id FROM choice_tree
			)`, table, d.Placeholder(next), table))
			args = append(args, idArg(d, id))
			next++
			continue
		}

		field := choiceField(entity, fieldName)
		if field == nil {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q does not exist", i, fieldName)
		}
		// Булев реквизит сравнивается с литералом: «показывать только
		// немуниципальные» не зависит от того, что выбрано на форме.
		if field.Type == metadata.FieldTypeBool {
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: boolean field supports only eq", i)
			}
			value, ok := predicate.Value.(bool)
			if !ok {
				return "", nil, startArg, fmt.Errorf("choice filter %d: field %q requires boolean value", i, fieldName)
			}
			parts = append(parts, metadata.ColumnName(*field)+" = "+d.Placeholder(next))
			args = append(args, value)
			next++
			continue
		}
		// Строковый реквизит сравнивается со строкой: так дом связан с улицей
		// в адресном классификаторе — ВладелецКод хранит ИД записи иерархии,
		// а не ссылку, и связь есть у всех домов, тогда как ссылочная — лишь
		// у половины.
		if field.Type == metadata.FieldTypeString {
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: string field supports only eq", i)
			}
			value, ok := predicate.Value.(string)
			if !ok {
				return "", nil, startArg, fmt.Errorf("choice filter %d: field %q requires string value", i, fieldName)
			}
			parts = append(parts, metadata.ColumnName(*field)+" = "+d.Placeholder(next))
			args = append(args, value)
			next++
			continue
		}
		if strings.TrimSpace(field.RefEntity) == "" {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q is not a reference", i, fieldName)
		}
		id, err := choiceUUID(predicate.Value)
		if err != nil {
			return "", nil, startArg, fmt.Errorf("choice filter %d field %q: %w", i, fieldName, err)
		}
		column := metadata.ColumnName(*field)
		switch predicate.Op {
		case metadata.FormChoiceOpEqual:
			parts = append(parts, column+" = "+d.Placeholder(next))
			args = append(args, idArg(d, id))
			next++
		case metadata.FormChoiceOpInHierarchy, metadata.FormChoiceOpNotInHierarchy:
			table := metadata.TableName(field.RefEntity)
			placeholder := d.Placeholder(next)
			// Отрицание — тот же рекурсивный обход поддерева, только NOT IN.
			оператор := "IN"
			if predicate.Op == metadata.FormChoiceOpNotInHierarchy {
				оператор = "NOT IN"
			}
			parts = append(parts, fmt.Sprintf(`%s `+оператор+` (
				WITH RECURSIVE choice_tree(id) AS (
					SELECT id FROM %s WHERE id = %s
					UNION
					SELECT child.id FROM %s AS child
					JOIN choice_tree AS parent ON child.parent_id = parent.id
				)
				SELECT id FROM choice_tree
			)`, column, table, placeholder, table))
			args = append(args, idArg(d, id))
			next++
		default:
			return "", nil, startArg, fmt.Errorf("choice filter %d: unsupported operator %q", i, predicate.Op)
		}
	}
	return "(" + strings.Join(parts, " AND ") + ")", args, next, nil
}

func choiceField(entity *metadata.Entity, name string) *metadata.Field {
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i]
		}
	}
	return nil
}

func choiceUUID(value any) (uuid.UUID, error) {
	switch typed := value.(type) {
	case uuid.UUID:
		if typed == uuid.Nil {
			return uuid.Nil, fmt.Errorf("UUID is empty")
		}
		return typed, nil
	case *uuid.UUID:
		if typed == nil || *typed == uuid.Nil {
			return uuid.Nil, fmt.Errorf("UUID is empty")
		}
		return *typed, nil
	case string:
		id, err := uuid.Parse(strings.TrimSpace(typed))
		if err != nil || id == uuid.Nil {
			return uuid.Nil, fmt.Errorf("invalid UUID %q", typed)
		}
		return id, nil
	default:
		return uuid.Nil, fmt.Errorf("value must be UUID, got %T", value)
	}
}

func choiceTablePart(entity *metadata.Entity, name string) *metadata.TablePart {
	for i := range entity.TableParts {
		if strings.EqualFold(entity.TableParts[i].Name, name) {
			return &entity.TableParts[i]
		}
	}
	return nil
}

func choiceTablePartField(tp *metadata.TablePart, name string) *metadata.Field {
	for i := range tp.Fields {
		if strings.EqualFold(tp.Fields[i].Name, name) {
			return &tp.Fields[i]
		}
	}
	return nil
}
