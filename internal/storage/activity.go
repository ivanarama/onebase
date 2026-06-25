package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// SetActivity sets the configured activity field for one catalog record.
func (db *DB) SetActivity(ctx context.Context, entity *metadata.Entity, id uuid.UUID, active bool) error {
	if entity == nil || entity.Activity == nil {
		return fmt.Errorf("activity is not configured")
	}
	d := db.dialect
	table := metadata.TableName(entity.Name)
	col := metadata.ColumnName(metadata.Field{Name: entity.Activity.Field})
	return db.exec(ctx,
		fmt.Sprintf("UPDATE %s SET %s = %s, _version = _version + 1 WHERE id = %s",
			table, col, d.Placeholder(1), d.Placeholder(2)),
		active, idArg(d, id))
}
