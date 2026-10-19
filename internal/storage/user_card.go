package storage

import (
	"context"
	"fmt"
	"github.com/shopspring/decimal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// UserCardDescriptor contains only statically resolved metadata. It grants a
// narrow infrastructure read, independent of RBAC/RLS of the catalog itself.
type UserCardDescriptor struct {
	Catalog      *metadata.Entity
	AccountField metadata.Field
	Field        metadata.Field
}

type UserCardValue struct {
	Value     any
	Available bool
}

// ReadUserCardSource reads uniqueness and the selected scalar in one statement
// on the caller's connection/transaction. Values never outlive the decision.
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
	matched, countCTE := "__ob_user_card_matched", "__ob_user_card_count"
	if strings.EqualFold(table, matched) {
		matched += "_"
	}
	if strings.EqualFold(table, countCTE) {
		countCTE += "_"
	}
	sqlText := fmt.Sprintf(`WITH %s AS (
 SELECT id, %s AS value FROM %s WHERE %s ORDER BY id LIMIT 2
 ), %s AS (SELECT COUNT(*) AS n FROM %s)
 SELECT c.n, m.id, m.value
 FROM %s AS c LEFT JOIN %s AS m ON c.n = 1`,
		matched, metadata.ColumnName(desc.Field), table, where, countCTE, matched, countCTE, matched)
	rows, err := db.Query(ctx, sqlText, idArg(d, id))
	if err != nil {
		return UserCardValue{}, fmt.Errorf("read user_card: %w", err)
	}
	defer rows.Close()
	var result UserCardValue
	for rows.Next() {
		var count int
		var cardID, raw any
		if err := rows.Scan(&count, &cardID, &raw); err != nil {
			return UserCardValue{}, err
		}
		if count != 1 || cardID == nil || raw == nil {
			continue
		}
		value := normalizeFieldValue(desc.Field, raw)
		if value == nil {
			continue
		}
		if s, ok := value.(string); ok && s == "" {
			continue
		}
		switch desc.Field.Type {
		case metadata.FieldTypeNumber:
			if _, ok := value.(decimal.Decimal); !ok {
				continue
			}
		case metadata.FieldTypeDate:
			if _, ok := value.(time.Time); !ok {
				continue
			}
		case metadata.FieldTypeBool:
			switch b := value.(type) {
			case bool:
			case int64:
				if b != 0 && b != 1 {
					continue
				}
				value = b == 1
			default:
				continue
			}
		case metadata.FieldTypeString:
			if _, ok := value.(string); !ok {
				continue
			}
		}
		if metadata.IsEnum(desc.Field.Type) {
			src := db.enumSourceOrNil()
			if src == nil {
				return UserCardValue{}, fmt.Errorf("user_card enumeration metadata is unavailable")
			}
			// Write validation accepts an unselected optional enum and trims its
			// identifier. An access source must contain a selected, exact identifier;
			// validate the same value that the policy will actually compare.
			selected, ok := value.(string)
			if !ok || selected == "<nil>" || selected != strings.TrimSpace(selected) {
				continue
			}
			en := src.GetEnum(metadata.EnumTypeName(desc.Field.Type))
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
				continue
			}
		}
		if desc.Field.RefEntity != "" {
			parsed, ok := parseAnyUUID(value)
			if !ok || parsed == uuid.Nil {
				continue
			}
			value = parsed
		}
		result = UserCardValue{Value: value, Available: true}
	}
	if err := rows.Err(); err != nil {
		return UserCardValue{}, err
	}
	return result, nil
}
