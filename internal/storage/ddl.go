package storage

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

func CreateTableSQL(e *metadata.Entity) string {
	var sb strings.Builder
	table := metadata.TableName(e.Name)
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id UUID PRIMARY KEY")
	for _, f := range e.Fields {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(pgType(f))
	}
	// foreign key constraints
	for _, f := range e.Fields {
		if f.RefEntity != "" {
			sb.WriteString(",\n    FOREIGN KEY (")
			sb.WriteString(metadata.ColumnName(f))
			sb.WriteString(") REFERENCES ")
			sb.WriteString(metadata.TableName(f.RefEntity))
			sb.WriteString("(id)")
		}
	}
	sb.WriteString("\n)")
	return sb.String()
}

func AddColumnSQL(table, col, pgtype string) string {
	return "ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS " + col + " " + pgtype
}

func pgType(f metadata.Field) string {
	if f.RefEntity != "" {
		return "UUID"
	}
	switch f.Type {
	case metadata.FieldTypeDate:
		return "TIMESTAMPTZ"
	case metadata.FieldTypeNumber:
		return "NUMERIC"
	case metadata.FieldTypeBool:
		return "BOOLEAN"
	default:
		return "TEXT"
	}
}
