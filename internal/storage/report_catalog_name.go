package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// ReadCatalogNameReference projects only the ID of a named catalog element.
// Its caller must validate the catalog and name from trusted configuration.
func (db *DB) ReadCatalogNameReference(ctx context.Context, catalog *metadata.Entity, name string) (uuid.UUID, int, error) {
	if catalog == nil || catalog.Kind != metadata.KindCatalog || name == "" {
		return uuid.Nil, 0, fmt.Errorf("invalid catalog name descriptor")
	}
	d := db.Dialect()
	column := metadata.ColumnName(metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString})
	where := column + " = " + d.Placeholder(1) + " AND " + deletionMarkWhere(d, true)
	if folders := folderScopeWhere(d, catalog, false, true); folders != "" {
		where += " AND " + folders
	}
	sqlText := fmt.Sprintf("SELECT id FROM %s WHERE %s LIMIT 2", metadata.TableName(catalog.Name), where)
	rows, err := db.Query(ctx, sqlText, name)
	if err != nil {
		return uuid.Nil, 0, err
	}
	defer rows.Close()
	var id uuid.UUID
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return uuid.Nil, 0, err
		}
		id, err = uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return uuid.Nil, 0, fmt.Errorf("invalid catalog element ID")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, 0, err
	}
	if count != 1 {
		return uuid.Nil, count, nil
	}
	return id, count, nil
}
