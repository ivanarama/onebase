package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// UsersReference describes a physical metadata column targeting _users(id).
// FieldID lets the read-only planner resolve a pending rename through the schema map.
type UsersReference struct {
	Object  string
	Table   string
	Column  string
	FieldID string
}

// UsersReferences inventories supported metadata columns, including numbered
// account-register subconto. It does not infer references from physical names.
func UsersReferences(entities []*metadata.Entity, registers []*metadata.Register, infoRegisters []*metadata.InfoRegister, accountRegisters []*metadata.AccountRegister) []UsersReference {
	var refs []UsersReference
	add := func(object, table string, fields []metadata.Field, subconto bool) {
		for i, f := range fields {
			if !metadata.IsSystemRefTarget(f.RefEntity) {
				continue
			}
			column := metadata.ColumnName(f)
			if subconto {
				column = metadata.SubcontoColumn(i + 1)
			}
			refs = append(refs, UsersReference{Object: object + "." + f.Name, Table: table, Column: column, FieldID: f.ID})
		}
	}
	for _, e := range entities {
		add(e.Name, metadata.TableName(e.Name), e.Fields, false)
		for _, tp := range e.TableParts {
			add(e.Name+"."+tp.Name, metadata.TablePartTableName(e.Name, tp.Name), tp.Fields, false)
		}
	}
	for _, r := range registers {
		table := metadata.RegisterTableName(r.Name)
		add(r.Name, table, r.Dimensions, false)
		add(r.Name, table, r.Resources, false)
		add(r.Name, table, r.Attributes, false)
	}
	for _, r := range infoRegisters {
		table := metadata.InfoRegTableName(r.Name)
		add(r.Name, table, r.Dimensions, false)
		add(r.Name, table, r.Resources, false)
	}
	for _, r := range accountRegisters {
		table := metadata.AccountRegTableName(r.Name)
		add(r.Name, table, r.Resources, false)
		add(r.Name, table, r.Subconto, true)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Table != refs[j].Table {
			return refs[i].Table < refs[j].Table
		}
		return refs[i].Column < refs[j].Column
	})
	return refs
}

// UsersReferencePlan reports observed schema and data, without applying DDL or repairs.
// An absent constraint is a proposal for later migration slices, not a promise
// that the current ordinary migrate command can install it.
type UsersReferencePlan struct {
	Reference    UsersReference
	ActualColumn string
	State        string // missing-table, missing-column, missing-fk, incompatible-fk, protected
	BrokenCount  int64
	RecordIDs    []string // at most five source record identifiers; never referenced user data
}

func (p UsersReferencePlan) String() string {
	where := p.Reference.Table + "." + p.Reference.Column
	if p.ActualColumn != "" && p.ActualColumn != p.Reference.Column {
		where += " (сейчас " + p.ActualColumn + ")"
	}
	description := map[string]string{
		"missing-table":   "таблица отсутствует; колонка и FK ещё не созданы",
		"missing-column":  "колонка отсутствует; FK ещё не создан",
		"missing-fk":      "отсутствует FK на _users(id)",
		"incompatible-fk": "существующий FK не эквивалентен проверенному FK на _users(id); требуется ручной разбор",
		"protected":       "действующий FK на _users(id) проверен",
	}[p.State]
	return fmt.Sprintf("%s (%s): %s", where, p.Reference.Object, description)
}

// PlanUsersReferenceConstraints reads both catalogs and non-NULL source values.
// Catalog/query errors fail closed; missing auth schema is never silently skipped.
// This is a read-only snapshot, not authorization to apply DDL after a concurrent write.
func (db *DB) PlanUsersReferenceConstraints(ctx context.Context, refs []UsersReference) ([]UsersReferencePlan, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	target, err := db.usersReferenceTableExists(ctx, metadata.SystemUsersEntity)
	if err != nil {
		return nil, err
	}
	if !target {
		return nil, fmt.Errorf("ссылки на _users: таблица _users отсутствует; сначала подготовьте auth-схему (dry-run её не создаёт)")
	}
	targetCols, err := db.usersReferenceColumns(ctx, metadata.SystemUsersEntity)
	if err != nil {
		return nil, err
	}
	if _, ok := targetCols["id"]; !ok {
		return nil, fmt.Errorf("ссылки на _users: колонка _users.id отсутствует")
	}
	var out []UsersReferencePlan
	for _, ref := range refs {
		p := UsersReferencePlan{Reference: ref, ActualColumn: ref.Column}
		exists, err := db.usersReferenceTableExists(ctx, ref.Table)
		if err != nil {
			return nil, err
		}
		if !exists {
			p.State = "missing-table"
			out = append(out, p)
			continue
		}
		cols, err := db.usersReferenceColumns(ctx, ref.Table)
		if err != nil {
			return nil, err
		}
		if ref.FieldID != "" {
			// Resolve only an explicit stable ID; never guess from a similar column name.
			mapExists, err := db.usersReferenceTableExists(ctx, "_schema_fields")
			if err != nil {
				return nil, err
			}
			if mapExists {
				rows, err := db.Query(ctx, "SELECT column_name FROM _schema_fields WHERE table_name = "+db.dialect.Placeholder(1)+" AND field_id = "+db.dialect.Placeholder(2), ref.Table, ref.FieldID)
				if err != nil {
					return nil, fmt.Errorf("%s: карта полей: %w", ref.Object, err)
				}
				var stored string
				for rows.Next() {
					if err = rows.Scan(&stored); err != nil {
						break
					}
				}
				readErr := rows.Err()
				rows.Close()
				if err != nil {
					return nil, err
				}
				if readErr != nil {
					return nil, readErr
				}
				if stored != "" && stored != ref.Column {
					_, oldExists := cols[stored]
					_, newExists := cols[ref.Column]
					if oldExists && newExists {
						return nil, fmt.Errorf("%s: колонки %s и %s одновременно существуют; разберите переименование вручную", ref.Object, stored, ref.Column)
					}
					if oldExists {
						p.ActualColumn = stored
					}
				}
			}
		}
		if _, ok := cols[p.ActualColumn]; !ok {
			p.State = "missing-column"
			out = append(out, p)
			continue
		}
		equivalent, other, err := db.usersReferenceFK(ctx, ref.Table, p.ActualColumn)
		if err != nil {
			return nil, fmt.Errorf("%s: каталог FK: %w", ref.Object, err)
		}
		p.State = "missing-fk"
		if other {
			p.State = "incompatible-fk"
		} else if equivalent {
			p.State = "protected"
		}
		column := "src." + quoteIdent(p.ActualColumn)
		comparison := "CAST(target.id AS TEXT) = CAST(" + column + " AS TEXT)"
		if !db.IsSQLite() && cols[p.ActualColumn] == "uuid" && targetCols["id"] == "uuid" {
			comparison = "target.id = " + column
		}
		where := column + " IS NOT NULL AND NOT EXISTS (SELECT 1 FROM " + quoteIdent(metadata.SystemUsersEntity) + " AS target WHERE " + comparison + ")"
		// Cast both sides: a string->reference retype may still have a TEXT source on PG.
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+quoteIdent(ref.Table)+" AS src WHERE "+where).Scan(&p.BrokenCount); err != nil {
			return nil, fmt.Errorf("%s (%s.%s): проверка ссылок: %w", ref.Object, ref.Table, p.ActualColumn, err)
		}
		if p.BrokenCount > 0 {
			if p.State == "protected" {
				p.State = "incompatible-fk"
			}
			record := "CAST(src.id AS TEXT)"
			if _, ok := cols["id"]; !ok {
				record = ""
				for _, alias := range []string{"rowid", "_rowid_", "oid"} {
					if _, shadowed := cols[alias]; !shadowed {
						record = "CAST(src." + alias + " AS TEXT)"
						break
					}
				}
				if !db.IsSQLite() {
					record = "CAST(src.ctid AS TEXT)"
				}
			}
			if record == "" {
				return nil, fmt.Errorf("%s: нет безопасного идентификатора исходной записи", ref.Object)
			}
			rows, err := db.Query(ctx, "SELECT "+record+" FROM "+quoteIdent(ref.Table)+" AS src WHERE "+where+" ORDER BY "+record+" LIMIT 5")
			if err != nil {
				return nil, fmt.Errorf("%s: идентификаторы битых записей: %w", ref.Object, err)
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					break
				}
				p.RecordIDs = append(p.RecordIDs, id)
			}
			readErr := rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			if readErr != nil {
				return nil, readErr
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func (db *DB) usersReferenceColumns(ctx context.Context, table string) (map[string]string, error) {
	if db.IsSQLite() {
		return db.tableColumns(ctx, table)
	}
	rows, err := db.Query(ctx, `SELECT a.attname, format_type(a.atttypid,a.atttypmod)
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped`, table)
	if err != nil {
		return nil, fmt.Errorf("каталог колонок %s: %w", table, err)
	}
	defer rows.Close()
	cols := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		cols[name] = typ
	}
	return cols, rows.Err()
}

func (db *DB) usersReferenceTableExists(ctx context.Context, table string) (bool, error) {
	q := `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`
	if !db.IsSQLite() {
		q = `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=$1 AND c.relkind IN ('r','p'))`
	}
	var exists bool
	if err := db.QueryRow(ctx, q, table).Scan(&exists); err != nil {
		return false, fmt.Errorf("каталог таблицы %s: %w", table, err)
	}
	return exists, nil
}

func (db *DB) usersReferenceFK(ctx context.Context, table, column string) (equivalent, other bool, err error) {
	if db.IsSQLite() {
		rows, e := db.Query(ctx, `SELECT id, seq, "table", "from", COALESCE("to",''), on_update, on_delete, match FROM pragma_foreign_key_list(?)`, table)
		if e != nil {
			return false, false, e
		}
		defer rows.Close()
		type fk struct {
			count                              int
			source, target, to, update, delete string
		}
		keys := map[int]*fk{}
		for rows.Next() {
			var id, seq int
			var target, from, to, update, del, match string
			if e := rows.Scan(&id, &seq, &target, &from, &to, &update, &del, &match); e != nil {
				return false, false, e
			}
			key := keys[id]
			if key == nil {
				key = &fk{}
				keys[id] = key
			}
			key.count++
			if strings.EqualFold(from, column) {
				key.source = from
				key.target = target
				key.to = to
				key.update = update
				key.delete = del
			}
		}
		if e := rows.Err(); e != nil {
			return false, false, e
		}
		var enabled int
		// Finish the cursor before querying: SQLite uses a single physical connection.
		rows.Close()
		if e := db.QueryRow(ctx, `PRAGMA foreign_keys`).Scan(&enabled); e != nil {
			return false, false, e
		}
		invalid := map[int]bool{}
		checked, e := db.Query(ctx, `SELECT DISTINCT fkid FROM pragma_foreign_key_check(?)`, table)
		if e != nil {
			return false, false, e
		}
		for checked.Next() {
			var id int
			if e = checked.Scan(&id); e != nil {
				break
			}
			invalid[id] = true
		}
		readErr := checked.Err()
		checked.Close()
		if e != nil {
			return false, false, e
		}
		if readErr != nil {
			return false, false, readErr
		}
		for id, key := range keys {
			if key.source == "" {
				continue
			}
			if key.count == 1 && strings.EqualFold(key.target, metadata.SystemUsersEntity) && (strings.EqualFold(key.to, "id") || key.to == "") && (key.delete == "NO ACTION" || key.delete == "RESTRICT") && (key.update == "NO ACTION" || key.update == "RESTRICT") && enabled == 1 && !invalid[id] {
				// An implicit target means its complete PK, which must be exactly id.
				if key.to == "" {
					var count int
					if e := db.QueryRow(ctx, `SELECT COUNT(*) FROM pragma_table_info('_users') WHERE pk>0 AND name='id'`).Scan(&count); e != nil {
						return false, false, e
					}
					var total int
					if e := db.QueryRow(ctx, `SELECT COUNT(*) FROM pragma_table_info('_users') WHERE pk>0`).Scan(&total); e != nil {
						return false, false, e
					}
					if count != 1 || total != 1 {
						other = true
						continue
					}
				}
				equivalent = true
			} else {
				other = true
			}
		}
		return equivalent, other, nil
	}
	// Check the full semantic target, validation, and all enforcement triggers.
	rows, e := db.Query(ctx, `SELECT cardinality(c.conkey), tn.nspname=current_schema(), target.relname,
  COALESCE(ta.attname,''), c.confdeltype::text, c.confupdtype::text, c.convalidated,
  NOT EXISTS(SELECT 1 FROM pg_trigger t WHERE t.tgconstraint=c.oid AND t.tgenabled NOT IN ('O','A'))
 FROM pg_constraint c
 JOIN pg_class src ON src.oid=c.conrelid JOIN pg_namespace sn ON sn.oid=src.relnamespace
 JOIN pg_attribute sa ON sa.attrelid=src.oid AND sa.attnum=ANY(c.conkey)
 JOIN pg_class target ON target.oid=c.confrelid JOIN pg_namespace tn ON tn.oid=target.relnamespace
 LEFT JOIN pg_attribute ta ON ta.attrelid=target.oid AND ta.attnum=c.confkey[1]
 WHERE c.contype='f' AND sn.nspname=current_schema() AND src.relname=$1 AND sa.attname=$2`, table, column)
	if e != nil {
		return false, false, e
	}
	defer rows.Close()
	for rows.Next() {
		var size int
		var schema, validated, enabled bool
		var target, to, del, update string
		if e := rows.Scan(&size, &schema, &target, &to, &del, &update, &validated, &enabled); e != nil {
			return false, false, e
		}
		if size == 1 && schema && target == metadata.SystemUsersEntity && to == "id" && (del == "a" || del == "r") && (update == "a" || update == "r") && validated && enabled {
			equivalent = true
		} else {
			other = true
		}
	}
	return equivalent, other, rows.Err()
}
