package storage

// Entity index ownership is explicit. A generated name is never permission to
// drop an index; the catalog and the persisted definition must agree under a
// write lock. All entities reconcile in one transaction.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// EntityIndexDefinition is an exact, versioned catalog description. Definition
// is the database's own DDL, never SQL supplied by an adoption manifest.
type EntityIndexDefinition struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Name       string   `json:"name"`
	Unique     bool     `json:"unique"`
	Columns    []string `json:"columns"`
	KeyOptions []string `json:"key_options"`
	Definition string   `json:"definition"`
	Plain      bool     `json:"plain"`
	Opaque     bool     `json:"opaque"`
}

func (i EntityIndexDefinition) Fingerprint() string {
	b, _ := json.Marshal(i)
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("onebase-entity-index-v1\n"), b...)))
}

// IndexAdoption authorizes only this exact catalog object. Unknown JSON keys
// and duplicate entries must be rejected by the caller decoding a manifest.
type IndexAdoption struct {
	Index  EntityIndexDefinition `json:"index"`
	SHA256 string                `json:"sha256"`
}
type IndexManifest struct {
	Version int             `json:"version"`
	Indexes []IndexAdoption `json:"indexes"`
}
type EntityIndexAction struct {
	Index     EntityIndexDefinition `json:"index"`
	Ownership string                `json:"ownership"`
	Action    string                `json:"action"`
}

func (db *DB) EnsureManagedIndexSchema(ctx context.Context) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS _managed_entity_indexes (
 table_name TEXT NOT NULL, index_name TEXT NOT NULL, format_version INTEGER NOT NULL,
 definition TEXT NOT NULL, signature TEXT NOT NULL, source TEXT NOT NULL,
 PRIMARY KEY (table_name,index_name))`)
	return err
}

func (db *DB) entityIndexSchema(ctx context.Context) (string, error) {
	if db.IsSQLite() {
		return "main", nil
	}
	var s string
	err := db.QueryRow(ctx, "SELECT COALESCE(current_schema(),'')").Scan(&s)
	if err == nil && s == "" {
		err = errors.New("index catalog: current_schema is empty")
	}
	return s, err
}

func (db *DB) readEntityIndexes(ctx context.Context, table string) ([]EntityIndexDefinition, error) {
	schema, err := db.entityIndexSchema(ctx)
	if err != nil {
		return nil, err
	}
	var out []EntityIndexDefinition
	if db.IsSQLite() {
		rows, err := db.Query(ctx, `SELECT l.name, l."unique", l.origin, l.partial, COALESCE(s.sql,'')
 FROM pragma_index_list(?) l LEFT JOIN sqlite_schema s ON s.type='index' AND s.name=l.name ORDER BY l.name`, table)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			i := EntityIndexDefinition{Schema: schema, Table: table, Columns: []string{}}
			var origin string
			var partial bool
			if err := rows.Scan(&i.Name, &i.Unique, &origin, &partial, &i.Definition); err != nil {
				rows.Close()
				return nil, err
			}
			i.Plain = origin == "c" && !partial && i.Definition != ""
			i.Opaque = partial
			out = append(out, i)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for k := range out {
			rows, err := db.Query(ctx, `SELECT cid,name,"desc",coll,"key" FROM pragma_index_xinfo(?) ORDER BY seqno`, out[k].Name)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var cid int
				var name, coll *string
				var desc, key bool
				if err := rows.Scan(&cid, &name, &desc, &coll, &key); err != nil {
					rows.Close()
					return nil, err
				}
				if !key {
					continue
				}
				collation := ""
				if coll != nil {
					collation = *coll
				}
				out[k].KeyOptions = append(out[k].KeyOptions, fmt.Sprintf("desc=%t;collation=%q", desc, collation))
				if cid < 0 || name == nil {
					out[k].Plain = false
					out[k].Opaque = true
					out[k].Columns = append(out[k].Columns, "")
					continue
				}
				out[k].Columns = append(out[k].Columns, *name)
				if desc || coll == nil || !strings.EqualFold(*coll, "BINARY") {
					out[k].Plain = false
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	rows, err := db.Query(ctx, `SELECT ic.relname,i.indisunique,pg_get_indexdef(i.indexrelid),
 (am.amname='btree' AND i.indpred IS NULL AND i.indexprs IS NULL
 AND i.indisvalid AND i.indisready AND NOT i.indisprimary AND NOT i.indisexclusion
 AND NOT i.indnullsnotdistinct AND i.indnkeyatts=i.indnatts
 AND NOT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conindid=i.indexrelid)
 AND NOT EXISTS(SELECT 1 FROM unnest(i.indoption) o WHERE o<>0)
 AND NOT EXISTS(SELECT 1 FROM unnest(i.indclass) cl JOIN pg_opclass oc ON oc.oid=cl WHERE NOT oc.opcdefault)
 AND NOT EXISTS(SELECT 1 FROM unnest(i.indkey::smallint[],i.indcollation::oid[]) p(att,coll)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=p.att WHERE a.attcollation<>p.coll)),
 (i.indpred IS NOT NULL OR i.indexprs IS NOT NULL),
 COALESCE((SELECT json_agg(COALESCE(a.attname,'') ORDER BY k.ord)::text
 FROM unnest(i.indkey::smallint[]) WITH ORDINALITY k(att,ord)
 LEFT JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.att),'[]')
 FROM pg_index i JOIN pg_class t ON t.oid=i.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace
 JOIN pg_class ic ON ic.oid=i.indexrelid JOIN pg_am am ON am.oid=ic.relam
 WHERE n.nspname=$1 AND t.relname=$2 ORDER BY ic.relname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		i := EntityIndexDefinition{Schema: schema, Table: table}
		var cols string
		if err := rows.Scan(&i.Name, &i.Unique, &i.Definition, &i.Plain, &i.Opaque, &cols); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cols), &i.Columns); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (db *DB) registeredIndexes(ctx context.Context, table string) (map[string]EntityIndexDefinition, error) {
	out := map[string]EntityIndexDefinition{}
	// This read must also work on a pre-registry legacy database and in preview.
	exists, err := db.TableExists(ctx, "_managed_entity_indexes")
	if err != nil {
		return nil, err
	}
	if !exists {
		return out, nil
	}
	rows, err := db.Query(ctx, `SELECT index_name,format_version,definition,signature,source FROM _managed_entity_indexes WHERE table_name=`+db.dialect.Placeholder(1), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, raw, sig, source string
		var version int
		var i EntityIndexDefinition
		if err := rows.Scan(&name, &version, &raw, &sig, &source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &i); err != nil {
			return nil, err
		}
		if version != 1 || i.Table != table || i.Name != name || !i.Plain || i.Fingerprint() != sig || (source != "created" && source != "adopted") {
			return nil, fmt.Errorf("%s.%s: invalid managed index record", table, name)
		}
		out[name] = i
	}
	return out, rows.Err()
}

func (db *DB) registerEntityIndex(ctx context.Context, i EntityIndexDefinition, source string) error {
	raw, err := json.Marshal(i)
	if err != nil {
		return err
	}
	p := db.dialect.Placeholder
	_, err = db.Exec(ctx, `INSERT INTO _managed_entity_indexes (table_name,index_name,format_version,definition,signature,source) VALUES (`+p(1)+","+p(2)+",1,"+p(3)+","+p(4)+","+p(5)+")", i.Table, i.Name, string(raw), i.Fingerprint(), source)
	return err
}

func legacyEntityIndex(e *metadata.Entity, i EntityIndexDefinition) bool {
	if !i.Plain || len(i.Columns) == 0 || i.Name != stableIndexName(i.Table, i.Columns, i.Unique) {
		return false
	}
	allowed := map[string]bool{}
	for _, f := range e.Fields {
		allowed[metadata.ColumnName(f)] = true
	}
	for _, col := range i.Columns {
		if !allowed[col] {
			return false
		}
	}
	return true
}

func desiredEntityIndexes(e *metadata.Entity) (map[string]EntityIndexDefinition, error) {
	out := map[string]EntityIndexDefinition{}
	specs := append([]metadata.IndexSpec{}, e.Indexes...)
	if spec, ok := UniqueCodeIndexSpec(e); ok {
		specs = append(specs, spec)
	}
	for _, s := range specs {
		cols, err := entityIndexColumns(e, s)
		if err != nil {
			return nil, err
		}
		table := metadata.TableName(e.Name)
		name := stableIndexName(table, cols, s.Unique)
		i := EntityIndexDefinition{Table: table, Name: name, Unique: s.Unique, Columns: cols, Plain: true}
		if prev, ok := out[name]; ok && (prev.Unique != i.Unique || !stringSlicesEqual(prev.Columns, i.Columns)) {
			return nil, fmt.Errorf("%s.%s: incompatible index declarations", table, name)
		}
		out[name] = i
	}
	return out, nil
}

// PlanEntityIndexes reads the catalog and ownership without creating service
// tables. It reports legacy candidates even when they are no longer in YAML.
func (db *DB) PlanEntityIndexes(ctx context.Context, entities []*metadata.Entity) ([]EntityIndexAction, IndexManifest, error) {
	return db.planEntityIndexes(ctx, entities, nil)
}
func (db *DB) planEntityIndexes(ctx context.Context, entities []*metadata.Entity, adoptions []IndexAdoption) ([]EntityIndexAction, IndexManifest, error) {
	manifest := IndexManifest{Version: 1, Indexes: []IndexAdoption{}}
	actions := []EntityIndexAction{}
	used := map[string]bool{}
	adopt := map[string]IndexAdoption{}
	for _, a := range adoptions {
		key := a.Index.Table + "/" + a.Index.Name
		if _, ok := adopt[key]; ok {
			return nil, manifest, fmt.Errorf("duplicate adoption %s", key)
		}
		if a.SHA256 != a.Index.Fingerprint() {
			return nil, manifest, fmt.Errorf("invalid adoption fingerprint %s", key)
		}
		adopt[key] = a
	}
	schema, err := db.entityIndexSchema(ctx)
	if err != nil {
		return nil, manifest, err
	}
	for _, e := range entities {
		desired, err := desiredEntityIndexes(e)
		if err != nil {
			return nil, manifest, err
		}
		table := metadata.TableName(e.Name)
		catalog, err := db.readEntityIndexes(ctx, table)
		if err != nil {
			return nil, manifest, err
		}
		registered, err := db.registeredIndexes(ctx, table)
		if err != nil {
			return nil, manifest, err
		}
		actual := map[string]EntityIndexDefinition{}
		for _, i := range catalog {
			actual[i.Name] = i
		}
		for name, i := range registered {
			if a, ok := actual[name]; !ok || a.Fingerprint() != i.Fingerprint() {
				return nil, manifest, fmt.Errorf("%s.%s: managed index differs from catalog; no indexes changed", table, name)
			}
		}
		for _, i := range catalog {
			ownership := "external"
			action := "keep"
			_, managed := registered[i.Name]
			legacy := legacyEntityIndex(e, i)
			if managed {
				ownership = "managed"
			} else if legacy {
				ownership = "legacy"
				manifest.Indexes = append(manifest.Indexes, IndexAdoption{Index: i, SHA256: i.Fingerprint()})
			}
			key := table + "/" + i.Name
			if a, ok := adopt[key]; ok {
				if managed || !legacy || i.Schema != schema || a.SHA256 != i.Fingerprint() {
					return nil, manifest, fmt.Errorf("%s: adoption does not match eligible catalog object", key)
				}
				used[key] = true
				ownership = "adopted"
				managed = true
			}
			d, wanted := desired[i.Name]
			if wanted && (!i.Plain || i.Unique != d.Unique || !stringSlicesEqual(i.Columns, d.Columns)) {
				return nil, manifest, fmt.Errorf("%s.%s: index name collision; definition differs", table, i.Name)
			}
			if managed && !wanted {
				action = "drop"
			}
			if legacy && !managed && !wanted && i.Unique {
				action = "blocked-legacy"
			}
			actions = append(actions, EntityIndexAction{i, ownership, action})
			delete(desired, i.Name)
		}
		for _, i := range desired {
			i.Schema = schema
			i.Definition = CreateEntityIndexSQL(table, i.Columns, i.Unique)
			// Names are schema-wide. An index on another table (or a PG relation)
			// must not be hidden by IF NOT EXISTS.
			var count int
			if db.IsSQLite() {
				err = db.QueryRow(ctx, "SELECT COUNT(*) FROM sqlite_schema WHERE name=?", i.Name).Scan(&count)
			} else {
				err = db.QueryRow(ctx, "SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2", schema, i.Name).Scan(&count)
			}
			if err != nil {
				return nil, manifest, err
			}
			if count != 0 {
				return nil, manifest, fmt.Errorf("%s.%s: index name belongs to another catalog object", table, i.Name)
			}
			actions = append(actions, EntityIndexAction{i, "new", "create"})
		}
	}
	for key := range adopt {
		if !used[key] {
			return nil, manifest, fmt.Errorf("%s: adoption object missing or outside configured entities", key)
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].Index.Table+"/"+actions[i].Index.Name < actions[j].Index.Table+"/"+actions[j].Index.Name
	})
	sort.Slice(manifest.Indexes, func(i, j int) bool {
		return manifest.Indexes[i].Index.Table+"/"+manifest.Indexes[i].Index.Name < manifest.Indexes[j].Index.Table+"/"+manifest.Indexes[j].Index.Name
	})
	return actions, manifest, nil
}

func (db *DB) reconcileEntityIndexes(ctx context.Context, entities []*metadata.Entity) error {
	var notReady []error
	err := db.WithTxScope(ctx, func(ctx context.Context) error {
		// Lock all tables in a deterministic order before inspecting any catalog.
		tables := []string{}
		for _, e := range entities {
			tables = append(tables, metadata.TableName(e.Name))
		}
		sort.Strings(tables)
		for _, table := range tables {
			if err := db.lockEntityTable(ctx, table); err != nil {
				return err
			}
		}
		if err := db.EnsureManagedIndexSchema(ctx); err != nil {
			return err
		}
		actions, _, err := db.planEntityIndexes(ctx, entities, db.schemaOpts.AdoptIndexes)
		if err != nil {
			return err
		}
		for _, e := range entities {
			if _, ok := UniqueCodeIndexSpec(e); ok {
				if err := db.CheckCodeUniquePrecondition(ctx, e); errors.Is(err, ErrCodeUniqueNotReady) {
					notReady = append(notReady, err)
				} else if err != nil {
					return err
				}
			}
		}
		// Column preparation has completed for every entity, so renumber can
		// inspect them. Index reconciliation is still all-or-nothing: readiness
		// failure must not remove an old constraint on another entity or scope.
		if len(notReady) > 0 {
			return errors.Join(notReady...)
		}
		for _, a := range actions {
			if a.Action == "blocked-legacy" {
				return fmt.Errorf("%s.%s: legacy UNIQUE retained; inspect migrate --dry-run and explicitly --adopt-indexes after backup", a.Index.Table, a.Index.Name)
			}
		}
		// Adoption and CREATE precede every DROP. A failure anywhere rolls back
		// all entities, including registry writes and any earlier CREATE.
		for _, a := range actions {
			if a.Ownership == "adopted" {
				if err := db.registerEntityIndex(ctx, a.Index, "adopted"); err != nil {
					return err
				}
			}
			if a.Action != "create" {
				continue
			}
			ddl := strings.Replace(a.Index.Definition, " IF NOT EXISTS", "", 1)
			if _, err := db.Exec(ctx, ddl); err != nil {
				return err
			}
			actual, err := db.readEntityIndexes(ctx, a.Index.Table)
			if err != nil {
				return err
			}
			var found *EntityIndexDefinition
			for k := range actual {
				if actual[k].Name == a.Index.Name {
					found = &actual[k]
					break
				}
			}
			if found == nil || !found.Plain || found.Unique != a.Index.Unique || !stringSlicesEqual(found.Columns, a.Index.Columns) {
				return fmt.Errorf("%s.%s: created definition differs", a.Index.Table, a.Index.Name)
			}
			if err := db.registerEntityIndex(ctx, *found, "created"); err != nil {
				return err
			}
		}
		for _, a := range actions {
			if a.Action != "drop" {
				continue
			}
			// Catalog is still locked, and this signature was matched above.
			target := quoteIdent(a.Index.Name)
			if db.IsPostgres() {
				target = quoteIdent(a.Index.Schema) + "." + target
			}
			if _, err := db.Exec(ctx, "DROP INDEX "+target); err != nil {
				return err
			}
			p := db.dialect.Placeholder
			tag, err := db.Exec(ctx, "DELETE FROM _managed_entity_indexes WHERE table_name="+p(1)+" AND index_name="+p(2)+" AND signature="+p(3), a.Index.Table, a.Index.Name, a.Index.Fingerprint())
			if err != nil {
				return err
			}
			if tag.RowsAffected != 1 {
				return errors.New("managed index deletion lost ownership")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return errors.Join(notReady...)
}

func (db *DB) lockEntityTable(ctx context.Context, table string) error {
	if !HasTx(ctx) {
		return errors.New("entity DDL requires a transaction")
	}
	if db.IsSQLite() {
		_, err := db.Exec(ctx, "UPDATE "+quoteIdent(table)+" SET id=id WHERE 0")
		return err
	}
	schema, err := db.entityIndexSchema(ctx)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, "LOCK TABLE "+quoteIdent(schema)+"."+quoteIdent(table)+" IN ACCESS EXCLUSIVE MODE")
	return err
}

func (db *DB) guardEntityColumnChanges(ctx context.Context, e *metadata.Entity) error {
	table := metadata.TableName(e.Name)
	exists, err := db.TableExists(ctx, table)
	if err != nil || !exists {
		return err
	}
	changes, err := db.planTableChanges(ctx, table, e.Fields, standardProvenance(e))
	if err != nil {
		return err
	}
	cols, err := db.tableColumns(ctx, table)
	if err != nil {
		return err
	}
	if db.IsPostgres() {
		for _, f := range e.Fields {
			old := toSnakeCase(f.Name)
			if old != metadata.ColumnName(f) {
				if _, ok := cols[old]; ok {
					changes = append(changes, SchemaChange{Table: table, Kind: ChangeRename, From: old, To: metadata.ColumnName(f)})
				}
			}
		}
	}
	indexes, err := db.readEntityIndexes(ctx, table)
	if err != nil {
		return err
	}
	for _, c := range changes {
		if c.Kind == ChangeAdd || c.Kind == ChangeRebindFieldID {
			// Rebinding only changes the schema-map ID, not the physical column.
			continue
		}
		col := c.From
		if c.Kind == ChangeRetype {
			col = c.To
		}
		for _, i := range indexes {
			// Expressions, predicates and opaque definitions are conservatively
			// dependent on every column. No heuristic SQL parser authorizes DDL.
			depends := i.Opaque
			for _, key := range i.Columns {
				if key == col {
					depends = true
				}
			}
			if depends {
				return fmt.Errorf("%s.%s: %s blocked by index %s; remove the index in a separate migration before changing the column", table, col, c.Kind, i.Name)
			}
		}
	}
	return nil
}

// CheckEntityColumnMigration is the same read-only dependency gate used by
// Migrate. Preview reports its refusal alongside the proposed changes.
func (db *DB) CheckEntityColumnMigration(ctx context.Context, e *metadata.Entity) error {
	return db.guardEntityColumnChanges(ctx, e)
}
