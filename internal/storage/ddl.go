package storage

import (
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

func CreateTablePartSQL(e *metadata.Entity, tp metadata.TablePart) string {
	var sb strings.Builder
	table := metadata.TablePartTableName(e.Name, tp.Name)
	parent := metadata.TableName(e.Name)
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id UUID PRIMARY KEY,\n    parent_id UUID NOT NULL REFERENCES ")
	sb.WriteString(parent)
	sb.WriteString("(id) ON DELETE CASCADE,\n    строка INT NOT NULL")
	for _, f := range tp.Fields {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(pgType(f))
	}
	sb.WriteString("\n)")
	return sb.String()
}

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
	// posted flag for documents
	if e.Kind == metadata.KindDocument {
		sb.WriteString(",\n    posted BOOLEAN NOT NULL DEFAULT FALSE")
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

func CreateRegisterSQL(reg *metadata.Register) string {
	var sb strings.Builder
	table := metadata.RegisterTableName(reg.Name)
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id UUID PRIMARY KEY,\n    recorder UUID NOT NULL,\n    recorder_type TEXT NOT NULL,\n    line_number INT NOT NULL DEFAULT 0,\n    period TIMESTAMPTZ,\n    вид_движения TEXT NOT NULL DEFAULT 'Приход'")
	for _, f := range reg.Dimensions {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(pgType(f))
	}
	for _, f := range reg.Resources {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(pgType(f))
	}
	for _, f := range reg.Attributes {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(pgType(f))
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
