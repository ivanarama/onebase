package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// SchemaTypeWarning describes a physical type mismatch outside the field-ID
// restructuring plan. It does not guarantee preservation by other migration steps.
type SchemaTypeWarning struct {
	Table, Column, Field, ActualType, ExpectedType string
}

func (w SchemaTypeWarning) String() string {
	return fmt.Sprintf("%s.%s (поле %s без id): SQL-тип %s, ожидается %s; план реструктуризации не меняет тип поля без id. "+
		"Другие операции миграции могут изменить тип и данные: на SQLite перестройка таблицы регистра сведений при смене ключа делает это даже без --allow-destructive. "+
		"Сначала сделайте резервную копию и согласуйте схему и данные вручную; простое добавление id уже возникшее расхождение не исправит. "+
		"Для будущих смен типа задайте устойчивый id до изменения типа.", w.Table, w.Column, w.Field, w.ActualType, w.ExpectedType)
}

// UntrackedFieldTypeWarnings reads the physical schema without modifying it.
// Like PlanMigration, it covers entities, table parts, accumulation registers
// and information registers. Missing tables/columns are additive work, not a
// mismatch. SQL types cannot prove a change between logical types with the
// same storage (notably string/date/reference/number on SQLite).
func (db *DB) UntrackedFieldTypeWarnings(ctx context.Context, entities []*metadata.Entity, registers []*metadata.Register, infoRegs []*metadata.InfoRegister) ([]SchemaTypeWarning, error) {
	var out []SchemaTypeWarning
	check := func(table string, fields []metadata.Field, legacyRename bool) error {
		warnings, err := db.untrackedTableTypeWarnings(ctx, table, fields, legacyRename)
		if err != nil {
			return err
		}
		out = append(out, warnings...)
		return nil
	}
	for _, e := range entities {
		if err := check(metadata.TableName(e.Name), e.Fields, true); err != nil {
			return nil, err
		}
		for _, tp := range e.TableParts {
			if err := check(metadata.TablePartTableName(e.Name, tp.Name), tp.Fields, false); err != nil {
				return nil, err
			}
		}
	}
	for _, reg := range registers {
		fields := append(append(append([]metadata.Field{}, reg.Dimensions...), reg.Resources...), reg.Attributes...)
		if err := check(metadata.RegisterTableName(reg.Name), fields, true); err != nil {
			return nil, err
		}
	}
	for _, reg := range infoRegs {
		fields := append(append([]metadata.Field{}, reg.Dimensions...), reg.Resources...)
		if err := check(metadata.InfoRegTableName(reg.Name), fields, false); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (db *DB) untrackedTableTypeWarnings(ctx context.Context, table string, fields []metadata.Field, legacyRename bool) ([]SchemaTypeWarning, error) {
	hasUntracked := false
	for _, f := range fields {
		if f.ID == "" {
			hasUntracked = true
			break
		}
	}
	if !hasUntracked {
		return nil, nil
	}
	cols, err := db.tableColumns(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []SchemaTypeWarning
	for _, f := range fields {
		if f.ID != "" {
			continue
		}
		col := metadata.ColumnName(f)
		actual, exists := cols[col]
		// This is the exact legacy rename performed by renameSnakeCols on PG,
		// including string -> reference's added _id suffix. Do not infer arbitrary
		// renames or use it on SQLite, where that migration does not run.
		if !exists && legacyRename && !db.IsSQLite() {
			old := toSnakeCase(f.Name)
			if old != col {
				actual, exists = cols[old]
				if exists {
					col = old
				}
			}
		}
		if !exists {
			continue
		}
		expected := fieldType(db.dialect, f)
		if physicalTypeFamily(actual, db.IsSQLite()) == physicalTypeFamily(expected, db.IsSQLite()) {
			continue
		}
		out = append(out, SchemaTypeWarning{Table: table, Column: col, Field: f.Name, ActualType: actual, ExpectedType: expected})
	}
	return out, nil
}

func physicalTypeFamily(typ string, sqlite bool) string {
	typ = strings.ToLower(strings.Join(strings.Fields(typ), " "))
	if sqlite {
		// BOOLEAN/BOOL also store the platform's 0/1 values correctly, despite
		// having NUMERIC rather than INTEGER affinity.
		if typ == "boolean" || typ == "bool" {
			return "integer"
		}
		// SQLite's declared types select an affinity. Comparing names alone would
		// falsely warn for VARCHAR/CLOB versus TEXT or BIGINT versus INTEGER.
		switch {
		case strings.Contains(typ, "int"):
			return "integer"
		case strings.Contains(typ, "char"), strings.Contains(typ, "clob"), strings.Contains(typ, "text"):
			return "text"
		case typ == "", strings.Contains(typ, "blob"):
			return "blob"
		case strings.Contains(typ, "real"), strings.Contains(typ, "floa"), strings.Contains(typ, "doub"):
			return "real"
		default:
			return "numeric"
		}
	}
	// information_schema reports canonical names and omits numeric precision;
	// compare type families only, without claiming to diagnose typmod changes.
	if i := strings.IndexByte(typ, '('); i >= 0 {
		typ = strings.TrimSpace(typ[:i])
	}
	switch typ {
	case "varchar", "character varying", "text":
		return "text"
	case "decimal", "numeric":
		return "numeric"
	case "timestamptz", "timestamp with time zone":
		return "timestamptz"
	case "bool", "boolean":
		return "boolean"
	default:
		return typ
	}
}
