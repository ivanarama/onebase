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

		// «<ТЧ>.<Колонка>» — отбор по табличной части выбираемого справочника
		// (#1822): запись подходит, если в её ТЧ есть хотя бы одна строка с
		// нужной ссылкой. Так выражается связь многие-ко-многим, которой в
		// реквизитах записи нет (бренд обслуживает несколько направлений).
		// EXISTS, а не JOIN: запись с двумя подходящими строками не задваивается
		// ни в выдаче, ни в total. Только eq и только ссылочная колонка —
		// грамматика закрыта до появления сценария.
		if tpName, tpField, isTablePart := strings.Cut(fieldName, "."); isTablePart {
			tp, column := ChoiceTablePartColumn(entity, tpName, tpField)
			if tp == nil || column == nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part column %q does not exist", i, fieldName)
			}
			if strings.TrimSpace(column.RefEntity) == "" {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part column %q is not a reference", i, fieldName)
			}
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: table part column %q supports only eq", i, fieldName)
			}
			id, err := choiceUUID(predicate.Value)
			if err != nil {
				return "", nil, startArg, fmt.Errorf("choice filter %d field %q: %w", i, fieldName, err)
			}
			// Без алиаса: строки ТЧ квалифицируются именем её таблицы
			// «<цель>_<тч>», которое не совпадает с именем таблицы цели. Любой
			// фиксированный алиас мог совпасть с именем каталога и затенить
			// внешнюю таблицу — корреляция parent_id = id тогда сравнивала бы
			// поля одной строки ТЧ.
			tpTable := metadata.TablePartTableName(entity.Name, tp.Name)
			parts = append(parts, fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s.parent_id = %s.id AND %s.%s = %s)",
				tpTable, tpTable, metadata.TableName(entity.Name), tpTable,
				metadata.ColumnName(*column), d.Placeholder(next)))
			args = append(args, idArg(d, id))
			next++
			continue
		}

		// Existing configuration attributes take precedence over the new
		// pseudo-field, including in flat catalogs and case-insensitive lookup.
		field, column := choiceField(entity, fieldName)
		if field == nil && strings.EqualFold(fieldName, metadata.FormChoiceRootField) {
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
			rootSQL := choiceEmptyRefSQL(d, "parent_id")
			if !value {
				rootSQL = "NOT (" + rootSQL + ")"
			}
			parts = append(parts, rootSQL)
			continue
		}

		if field == nil {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q does not exist", i, fieldName)
		}
		// Булев литерал: «показывать только немуниципальные адреса». Значение
		// приходит из метаданных формы, а не от браузера, поэтому единственная
		// проверка здесь — что реквизит действительно булев.
		if value, isBool := predicate.Value.(bool); isBool {
			if field.Type != metadata.FieldTypeBool {
				return "", nil, startArg, fmt.Errorf("choice filter %d: field %q is not boolean", i, fieldName)
			}
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: boolean field %q supports only eq", i, fieldName)
			}
			parts = append(parts, column+" = "+d.Placeholder(next))
			args = append(args, value)
			next++
			continue
		}
		// Строковый реквизит сравнивается со строкой — значением строкового
		// конца пути: ВладелецКод дома хранит ИД улицы, а не ссылку на неё.
		// Решает тип поля, а не значения: ссылочное поле по-прежнему принимает
		// UUID и строкой. Значение — параметр запроса, а не текст SQL.
		if field.Type == metadata.FieldTypeString && strings.TrimSpace(field.RefEntity) == "" {
			if predicate.Op != metadata.FormChoiceOpEqual {
				return "", nil, startArg, fmt.Errorf("choice filter %d: string field %q supports only eq", i, fieldName)
			}
			value, isText := predicate.Value.(string)
			if !isText {
				return "", nil, startArg, fmt.Errorf("choice filter %d: string field %q requires a string value", i, fieldName)
			}
			parts = append(parts, column+" = "+d.Placeholder(next))
			args = append(args, value)
			next++
			continue
		}
		if strings.TrimSpace(field.RefEntity) == "" {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q is not a reference", i, fieldName)
		}
		// eq_or_empty без значения — пустой источник: только записи с пустой
		// ссылкой. Пустая ссылка — NULL; на SQLite встречается и пустая
		// строка (так её пишут импорты), на PostgreSQL колонка uuid и ''
		// в ней не бывает.
		if predicate.Op == metadata.FormChoiceOpEqualOrEmpty && predicate.Value == nil {
			parts = append(parts, choiceEmptyRefSQL(d, column))
			continue
		}
		id, err := choiceUUID(predicate.Value)
		if err != nil {
			return "", nil, startArg, fmt.Errorf("choice filter %d field %q: %w", i, fieldName, err)
		}
		switch predicate.Op {
		case metadata.FormChoiceOpEqual:
			parts = append(parts, column+" = "+d.Placeholder(next))
			args = append(args, idArg(d, id))
			next++
		case metadata.FormChoiceOpEqualOrEmpty:
			parts = append(parts, "("+column+" = "+d.Placeholder(next)+" OR "+choiceEmptyRefSQL(d, column)+")")
			args = append(args, idArg(d, id))
			next++
		case metadata.FormChoiceOpInHierarchy:
			parts = append(parts, column+" IN "+choiceSubtreeSQL(field.RefEntity, d.Placeholder(next)))
			args = append(args, idArg(d, id))
			next++
		case metadata.FormChoiceOpNotInHierarchy:
			// Точное дополнение in_hierarchy (#1821): пустая ссылка проходит.
			// Голый NOT IN её молча потерял бы: NULL NOT IN (...) — не истина.
			// В самом поддереве NULL не бывает (это id), поэтому NOT IN для
			// заполненной ссылки работает как задумано.
			parts = append(parts, "("+choiceEmptyRefSQL(d, column)+" OR "+column+" NOT IN "+
				choiceSubtreeSQL(field.RefEntity, d.Placeholder(next))+")")
			args = append(args, idArg(d, id))
			next++
		default:
			return "", nil, startArg, fmt.Errorf("choice filter %d: unsupported operator %q", i, predicate.Op)
		}
	}
	return "(" + strings.Join(parts, " AND ") + ")", args, next, nil
}

// choiceSubtreeSQL — подзапрос id поддерева записи placeholder (она сама
// включена) в иерархическом справочнике refEntity. UNION, а не UNION ALL:
// цикл в иерархии, какой оставляет битая загрузка, не зацикливает обход.
func choiceSubtreeSQL(refEntity, placeholder string) string {
	table := metadata.TableName(refEntity)
	return fmt.Sprintf(`(
				WITH RECURSIVE choice_tree(id) AS (
					SELECT id FROM %s WHERE id = %s
					UNION
					SELECT child.id FROM %s AS child
					JOIN choice_tree AS parent ON child.parent_id = parent.id
				)
				SELECT id FROM choice_tree
			)`, table, placeholder, table)
}

// choiceEmptyRefSQL — «ссылка пуста» для eq_or_empty, is_root и not_in_hierarchy.
func choiceEmptyRefSQL(d Dialect, column string) string {
	if d.Name() == "sqlite" {
		return "(" + column + " IS NULL OR " + column + " = '')"
	}
	return column + " IS NULL"
}

// choiceField возвращает реквизит условия и его колонку. parent_id
// иерархического справочника — ссылка на тот же справочник в служебной
// колонке parent_id (#1819): к ней применимы eq и in_hierarchy ссылочного
// реквизита, и поддерево строится по той же таблице.
func choiceField(entity *metadata.Entity, name string) (*metadata.Field, string) {
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i], metadata.ColumnName(entity.Fields[i])
		}
	}
	if strings.EqualFold(name, metadata.FormChoiceParentField) {
		if field := metadata.FormChoiceParentFieldOf(entity); field != nil {
			return field, "parent_id"
		}
	}
	return nil, ""
}

// ChoiceTablePartColumn находит табличную часть и её колонку условия
// «<ТЧ>.<Колонка>» без учёта регистра — так же, как реквизиты шапки. Общий
// разбор для SQL, сервера форм и onebase check.
func ChoiceTablePartColumn(entity *metadata.Entity, tpName, columnName string) (*metadata.TablePart, *metadata.Field) {
	if entity == nil {
		return nil, nil
	}
	tpName, columnName = strings.TrimSpace(tpName), strings.TrimSpace(columnName)
	for i := range entity.TableParts {
		tp := &entity.TableParts[i]
		if !strings.EqualFold(tp.Name, tpName) {
			continue
		}
		for j := range tp.Fields {
			if strings.EqualFold(tp.Fields[j].Name, columnName) {
				return tp, &tp.Fields[j]
			}
		}
		return tp, nil
	}
	return nil, nil
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
