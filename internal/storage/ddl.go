package storage

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// fieldType maps a metadata field to its SQL column type using the dialect.
func fieldType(d Dialect, f metadata.Field) string {
	if f.RefEntity != "" {
		return d.TypeUUID()
	}
	switch f.Type {
	case metadata.FieldTypeDate:
		return d.TypeTimestamp()
	case metadata.FieldTypeNumber:
		return d.TypeNumber(f.Length, f.Scale)
	case metadata.FieldTypeBool:
		return d.TypeBool()
	case metadata.FieldTypeRichText:
		// richtext хранится как HTML в TEXT-колонке (поведение совпадает с
		// default; ветка явная — для читаемости и явной привязки типа).
		return d.TypeText()
	case metadata.FieldTypeImage:
		// image хранит ссылку (UUID бинарника в blob-хранилище) в TEXT-колонке.
		return d.TypeText()
	default:
		return d.TypeText()
	}
}

// boolFalseLit returns the literal for "false" in DEFAULT clauses.
// PG: FALSE; SQLite (INTEGER 0/1): 0.
func boolFalseLit(d Dialect) string {
	if d.Name() == "sqlite" {
		return "0"
	}
	return "FALSE"
}

func boolTrueLit(d Dialect) string {
	if d.Name() == "sqlite" {
		return "1"
	}
	return "TRUE"
}

func CreateTablePartSQL(d Dialect, e *metadata.Entity, tp metadata.TablePart) string {
	var sb strings.Builder
	table := metadata.TablePartTableName(e.Name, tp.Name)
	parent := metadata.TableName(e.Name)
	uuidT := d.TypeUUID()
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id ")
	sb.WriteString(uuidT)
	sb.WriteString(" PRIMARY KEY,\n    parent_id ")
	sb.WriteString(uuidT)
	sb.WriteString(" NOT NULL REFERENCES ")
	sb.WriteString(parent)
	sb.WriteString("(id) ON DELETE CASCADE,\n    строка INTEGER NOT NULL")
	for _, f := range tp.Fields {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, f))
	}
	sb.WriteString("\n)")
	return sb.String()
}

func CreateEntityIndexSQL(table string, cols []string, unique bool) string {
	prefix := "CREATE INDEX IF NOT EXISTS "
	if unique {
		prefix = "CREATE UNIQUE INDEX IF NOT EXISTS "
	}
	name := stableIndexName(table, cols, unique)
	return prefix + name + " ON " + table + " (" + strings.Join(cols, ", ") + ")"
}

func CreateTablePartParentIndexSQL(entityName, tpName string) string {
	table := metadata.TablePartTableName(entityName, tpName)
	return CreateEntityIndexSQL(table, []string{"parent_id", "строка"}, false)
}

func CreateRegisterPeriodIndexSQL(regName string) string {
	table := metadata.RegisterTableName(regName)
	return CreateEntityIndexSQL(table, []string{"period"}, false)
}

func CreateRegisterDimPeriodIndexSQL(reg *metadata.Register) string {
	table := metadata.RegisterTableName(reg.Name)
	cols := registerDimPeriodIndexColumns(reg)
	if len(cols) == 0 {
		return ""
	}
	return CreateEntityIndexSQL(table, cols, false)
}

func registerDimPeriodIndexColumns(reg *metadata.Register) []string {
	if reg == nil || len(reg.Dimensions) == 0 {
		return nil
	}
	limit := len(reg.Dimensions)
	if limit > 3 {
		limit = 3
	}
	cols := make([]string, 0, limit+1)
	for _, f := range reg.Dimensions[:limit] {
		cols = append(cols, metadata.ColumnName(f))
	}
	cols = append(cols, "period")
	return cols
}

func stableIndexName(table string, cols []string, unique bool) string {
	kind := "n"
	if unique {
		kind = "u"
	}
	sum := sha1.Sum([]byte(kind + "|" + table + "|" + strings.Join(cols, "|")))
	return "idx_ob_" + fmt.Sprintf("%x", sum[:6])
}

func CreateTableSQL(d Dialect, e *metadata.Entity) string {
	var sb strings.Builder
	table := metadata.TableName(e.Name)
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id ")
	sb.WriteString(d.TypeUUID())
	sb.WriteString(" PRIMARY KEY")
	for _, f := range e.Fields {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, f))
	}
	// posted flag for documents
	if e.Kind == metadata.KindDocument {
		sb.WriteString(",\n    posted ")
		sb.WriteString(d.TypeBool())
		sb.WriteString(" NOT NULL DEFAULT ")
		sb.WriteString(boolFalseLit(d))
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

func CreateRegisterSQL(d Dialect, reg *metadata.Register) string {
	var sb strings.Builder
	table := metadata.RegisterTableName(reg.Name)
	uuidT := d.TypeUUID()
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id ")
	sb.WriteString(uuidT)
	sb.WriteString(" PRIMARY KEY,\n    recorder ")
	sb.WriteString(uuidT)
	sb.WriteString(" NOT NULL,\n    recorder_type TEXT NOT NULL,\n    line_number INTEGER NOT NULL DEFAULT 0,\n    period ")
	sb.WriteString(d.TypeTimestamp())
	sb.WriteString(",\n    вид_движения TEXT NOT NULL DEFAULT 'Приход'")
	for _, f := range reg.Dimensions {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, f))
	}
	for _, f := range reg.Resources {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, f))
	}
	for _, f := range reg.Attributes {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(f))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, f))
	}
	sb.WriteString("\n)")
	return sb.String()
}

func CreateInfoRegisterSQL(d Dialect, ir *metadata.InfoRegister) string {
	var cols, pkParts []string
	if ir.Periodic {
		cols = append(cols, "period "+d.TypeTimestamp()+" NOT NULL")
		pkParts = append(pkParts, "period")
	}
	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		cols = append(cols, col+" "+fieldType(d, f)+" NOT NULL")
		pkParts = append(pkParts, col)
	}
	for _, f := range ir.Resources {
		cols = append(cols, metadata.ColumnName(f)+" "+fieldType(d, f))
	}
	// recorder/recorder_type — для записи из проведения документа.
	// без этих колонок Движения.X.Добавить() в info-регистр
	// негде «зацепиться» при перепроведении. NULL допускается — записи
	// могут быть и не от документа (миграция, seed, ручной ввод).
	cols = append(cols, "recorder "+d.TypeUUID(), "recorder_type "+d.TypeText())
	cols = append(cols, "updated_at "+d.TypeTimestamp())
	if len(pkParts) > 0 {
		cols = append(cols, "PRIMARY KEY ("+strings.Join(pkParts, ", ")+")")
	}
	return "CREATE TABLE IF NOT EXISTS " + metadata.InfoRegTableName(ir.Name) +
		" (\n    " + strings.Join(cols, ",\n    ") + "\n)"
}

// AddColumnIfMissing adds the column to the table if it doesn't already exist.
// SQLite has no native "ADD COLUMN IF NOT EXISTS" — we check via PRAGMA first.
func (db *DB) AddColumnIfMissing(ctx context.Context, table, col, typ string) error {
	if db.sqlDB != nil {
		exists, err := db.dialect.ColumnExists(ctx, db, table, col)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		_, err = db.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN "+col+" "+typ)
		return err
	}
	_, err := db.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN IF NOT EXISTS "+col+" "+typ)
	return err
}

// HierarchyColumnsSQL adds parent_id/is_folder columns and an index.
// Now executes against db directly (was returning raw SQL); use db.AddColumnIfMissing.
func (db *DB) AddHierarchyColumns(ctx context.Context, tableName string) error {
	d := db.dialect
	if err := db.AddColumnIfMissing(ctx, tableName, "parent_id", d.TypeUUID()); err != nil {
		return err
	}
	if err := db.AddColumnIfMissing(ctx, tableName, "is_folder", d.TypeBool()+" NOT NULL DEFAULT "+boolFalseLit(d)); err != nil {
		return err
	}
	_, err := db.Exec(ctx, "CREATE INDEX IF NOT EXISTS idx_"+tableName+"_parent ON "+tableName+" (parent_id)")
	return err
}
