package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// UserCardDescriptor contains only statically resolved metadata. It grants a
// narrow infrastructure read, independent of RBAC/RLS of the catalog itself.
type UserCardDescriptor struct {
	Catalog      *metadata.Entity
	AccountField metadata.Field
	Field        metadata.Field
	TablePart    *metadata.TablePart
}

type UserCardValue struct {
	Value     any
	Values    []any
	Available bool
}

// ReadUserCardSource reads uniqueness and values in ONE statement on the
// caller's connection/transaction. READ COMMITTED must not combine a card ID
// from one snapshot with table rows from another. Values outlive no decision.
func (db *DB) ReadUserCardSource(ctx context.Context, desc UserCardDescriptor, userID string) (UserCardValue, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return UserCardValue{}, fmt.Errorf("user_card requires an authenticated UUID")
	}
	if desc.Catalog == nil || desc.Catalog.Kind != metadata.KindCatalog || !strings.EqualFold(desc.AccountField.RefEntity, "_users") {
		return UserCardValue{}, fmt.Errorf("invalid user_card descriptor")
	}
	d := db.dialect
	where := fmt.Sprintf("%s = %s", metadata.ColumnName(desc.AccountField), d.Placeholder(1))
	where += " AND " + deletionMarkWhere(d, true)
	if folder := folderScopeWhere(d, desc.Catalog, false, true); folder != "" {
		where += " AND " + folder
	}
	table := metadata.TableName(desc.Catalog.Name)
	tpTable := ""
	if desc.TablePart != nil {
		tpTable = metadata.TablePartTableName(desc.Catalog.Name, desc.TablePart.Name)
	}
	cteName := func(name string) string {
		for strings.EqualFold(table, name) || strings.EqualFold(tpTable, name) {
			name += "_"
		}
		return name
	}
	matched, countCTE, valuesCTE := cteName("__ob_user_card_matched"), cteName("__ob_user_card_count"), cteName("__ob_user_card_values")
	projection := "id, " + metadata.ColumnName(desc.Field) + " AS value"
	if desc.TablePart != nil {
		projection = "id"
	}
	sqlText := fmt.Sprintf(`WITH %s AS (
 SELECT %s FROM %s WHERE %s ORDER BY id LIMIT 2
 ), %s AS (SELECT COUNT(*) AS n FROM %s)`, matched, projection, table, where, countCTE, matched)
	if desc.TablePart == nil {
		sqlText += fmt.Sprintf(` SELECT c.n, m.id, m.value
 FROM %s AS c LEFT JOIN %s AS m ON c.n = 1`, countCTE, matched)
	} else {
		// Limit RAW rows before deduplication. A NULL row ID after the left
		// join denotes an empty table part, not a selected empty value.
		sqlText += fmt.Sprintf(`, %s AS (
 SELECT v.id, v.строка AS row_number, v.%s AS value
 FROM %s AS v JOIN %s AS m ON v.parent_id = m.id
 CROSS JOIN %s AS c WHERE c.n = 1
 ORDER BY v.строка, v.id LIMIT 1001
 ) SELECT c.n, v.id, v.value
 FROM %s AS c LEFT JOIN %s AS v ON TRUE
 ORDER BY v.row_number, v.id`, valuesCTE, metadata.ColumnName(desc.Field), tpTable, matched, countCTE, countCTE, valuesCTE)
	}
	// Keep the ordinary read-only context gate: this statically read-only
	// statement starts with SELECT, with its CTEs inside the derived table.
	// No context flag is removed, and no write capability is introduced.
	sqlText = "SELECT * FROM (" + sqlText + ") AS __ob_user_card_source"
	rows, err := db.Query(ctx, sqlText, idArg(d, id))
	if err != nil {
		return UserCardValue{}, fmt.Errorf("read user_card: %w", err)
	}
	defer rows.Close()
	var result UserCardValue
	seen := map[string]bool{}
	invalid, rawCount := false, 0
	for rows.Next() {
		var count int
		var rowID, raw any
		if err := rows.Scan(&count, &rowID, &raw); err != nil {
			return UserCardValue{}, err
		}
		if count != 1 || rowID == nil {
			invalid = true
			continue
		}
		rawCount++
		value, valid, err := db.userCardFieldValue(desc.Field, raw)
		if err != nil {
			return UserCardValue{}, err
		}
		if !valid {
			invalid = true
			continue
		}
		if desc.TablePart == nil {
			result.Value = value
		} else {
			key := fmt.Sprintf("%T:%v", value, value)
			if date, ok := value.(time.Time); ok {
				key = date.UTC().Format(time.RFC3339Nano)
			}
			if !seen[key] {
				seen[key] = true
				result.Values = append(result.Values, value)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return UserCardValue{}, err
	}
	if invalid || rawCount == 0 || (desc.TablePart != nil && rawCount > 1000) {
		return UserCardValue{}, nil
	}
	result.Available = true
	return result, nil
}

func (db *DB) userCardFieldValue(field metadata.Field, raw any) (any, bool, error) {
	value := normalizeFieldValue(field, raw)
	if value == nil {
		return nil, false, nil
	}
	if s, ok := value.(string); ok && s == "" {
		return nil, false, nil
	}
	switch field.Type {
	case metadata.FieldTypeNumber:
		if _, ok := value.(decimal.Decimal); !ok {
			return nil, false, nil
		}
	case metadata.FieldTypeDate:
		if _, ok := value.(time.Time); !ok {
			return nil, false, nil
		}
	case metadata.FieldTypeBool:
		switch b := value.(type) {
		case bool:
		case int64:
			if b != 0 && b != 1 {
				return nil, false, nil
			}
			value = b == 1
		default:
			return nil, false, nil
		}
	case metadata.FieldTypeString:
		if _, ok := value.(string); !ok {
			return nil, false, nil
		}
	}
	if metadata.IsEnum(field.Type) {
		src := db.enumSourceOrNil()
		if src == nil {
			return nil, false, fmt.Errorf("user_card enumeration metadata is unavailable")
		}
		// Write validation accepts an unselected optional enum and trims its
		// identifier. An access source must contain a selected, exact identifier;
		// validate the same value that the policy will actually compare.
		selected, ok := value.(string)
		if !ok || selected == "<nil>" || selected != strings.TrimSpace(selected) {
			return nil, false, nil
		}
		en := src.GetEnum(metadata.EnumTypeName(field.Type))
		valid := false
		if en != nil {
			for _, id := range en.Values {
				if selected == id {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, false, nil
		}
	}
	if field.RefEntity != "" {
		parsed, ok := parseAnyUUID(value)
		if !ok || parsed == uuid.Nil {
			return nil, false, nil
		}
		value = parsed
	}
	return value, true, nil
}
