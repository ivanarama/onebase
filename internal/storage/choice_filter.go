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

		field := choiceField(entity, fieldName)
		if field == nil {
			return "", nil, startArg, fmt.Errorf("choice filter %d: field %q does not exist", i, fieldName)
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
		case metadata.FormChoiceOpInHierarchy:
			table := metadata.TableName(field.RefEntity)
			placeholder := d.Placeholder(next)
			parts = append(parts, fmt.Sprintf(`%s IN (
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
