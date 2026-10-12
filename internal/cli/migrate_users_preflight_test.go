package cli

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func usersPreflightProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: preflight\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Records.yaml", `name: Records
fields:
  - name: Author
    id: author
    type: reference:_users
  - name: NewUser
    type: reference:_users
  - name: Business
    type: reference:Records
tableparts:
  - name: Lines
    fields:
      - name: Editor
        type: reference:_users
`)
	writeProcrunFixture(t, dir, "registers/Balance.yaml", `name: Balance
kind: balance
dimensions:
  - name: Owner
    type: reference:_users
resources:
  - name: Reviewer
    type: reference:_users
attributes:
  - name: Editor
    type: reference:_users
`)
	writeProcrunFixture(t, dir, "inforegs/Info.yaml", `name: Info
dimensions:
  - name: Owner
    type: reference:_users
resources:
  - name: Editor
    type: reference:_users
`)
	writeProcrunFixture(t, dir, "accountregs/Accounts.yaml", `name: Accounts
accounts: Chart
resources:
  - name: Reviewer
    type: reference:_users
subconto:
  - name: NamedUser
    type: reference:_users
`)
	return dir
}

func preflightExec(t *testing.T, db *storage.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}

// Snapshot every table's rows as well as physical columns, FK, indexes and triggers.
// This also catches unexpected schema-revision/configdb writes made while opening CLI storage.
func preflightSnapshot(t *testing.T, db *storage.DB) []string {
	t.Helper()
	ctx := context.Background()
	queries := []string{`SELECT type || ':' || name || ':' || COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`}
	tablesQ := `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`
	if !db.IsSQLite() {
		queries = []string{
			`SELECT c.relname || ':' || a.attname || ':' || a.atttypid::text || ':' || a.attnotnull::text || ':' || COALESCE(pg_get_expr(d.adbin,d.adrelid),'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname=current_schema() AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum`,
			`SELECT c.conname || ':' || pg_get_constraintdef(c.oid) || ':' || c.convalidated::text FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname=current_schema() ORDER BY c.conname`,
			`SELECT indexname || ':' || indexdef FROM pg_indexes WHERE schemaname=current_schema() ORDER BY indexname`,
			`SELECT tgname || ':' || pg_get_triggerdef(t.oid) || ':' || tgenabled::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() ORDER BY tgname`,
		}
		tablesQ = `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename`
	}
	var snapshot, tables []string
	read := func(q string, dest *[]string) {
		t.Helper()
		rows, err := db.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			*dest = append(*dest, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range queries {
		read(q, &snapshot)
	}
	read(tablesQ, &tables)
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		if db.IsSQLite() {
			var cols []string
			read(`SELECT name FROM pragma_table_info('`+strings.ReplaceAll(table, `'`, `''`)+`') ORDER BY cid`, &cols)
			for i, c := range cols {
				cols[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
			}
			read(`SELECT json_array(`+strings.Join(cols, ",")+`) FROM `+quoted+` ORDER BY 1`, &snapshot)
		} else {
			read(`SELECT row_to_json(t)::text FROM `+quoted+` AS t ORDER BY 1`, &snapshot)
		}
	}
	return snapshot
}

func preflightRun(t *testing.T, db *storage.DB, dir string, extra map[string]string) (string, error) {
	t.Helper()
	cmd := userReferenceCLICommand(t, db, dir, false)
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	for k, v := range extra {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	before := preflightSnapshot(t, db)
	out, err := captureStdout(t, func() error { return migrateCmd.RunE(cmd, nil) })
	if after := preflightSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run changed schema/rows\nbefore: %v\nafter: %v", before, after)
	}
	return out, err
}

func TestMigrateUsersPreflightInventoryMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if _, err := db.RaiseSchemaRevision(context.Background()); err != nil {
			t.Fatal(err)
		}
		dir := usersPreflightProject(t)
		typ := db.Dialect().TypeUUID()
		preflightExec(t, db, `CREATE TABLE _users (id `+typ+` PRIMARY KEY, login TEXT)`)
		user := uuid.NewString()
		preflightExec(t, db, `INSERT INTO _users(id,login) VALUES (`+db.Dialect().Placeholder(1)+`,'SECRET_LOGIN')`, user)
		// A legacy string->reference column without a FK, resolved through stable ID.
		preflightExec(t, db, `CREATE TABLE records (id `+typ+` PRIMARY KEY, old_author TEXT, secret TEXT)`)
		preflightExec(t, db, `INSERT INTO records VALUES (`+db.Dialect().Placeholder(1)+`,`+db.Dialect().Placeholder(2)+`,'SECRET_BUSINESS')`, uuid.NewString(), user)
		preflightExec(t, db, `INSERT INTO records VALUES (`+db.Dialect().Placeholder(1)+`,NULL,'SECRET_BUSINESS')`, uuid.NewString())
		if err := db.EnsureSchemaMapSchema(context.Background()); err != nil {
			t.Fatal(err)
		}
		preflightExec(t, db, `INSERT INTO _schema_fields VALUES ('records','author','old_author','string')`)
		preflightExec(t, db, `CREATE TABLE records_lines (id `+typ+` PRIMARY KEY, editor_id `+typ+` REFERENCES _users(id))`)
		preflightExec(t, db, `CREATE TABLE "рег_balance" (id `+typ+` PRIMARY KEY, owner_id `+typ+`, reviewer_id `+typ+`, editor_id `+typ+`)`)
		preflightExec(t, db, `CREATE TABLE "инфо_info" (owner_id `+typ+` PRIMARY KEY, editor_id `+typ+`)`)
		preflightExec(t, db, `CREATE TABLE "акк_accounts" (id `+typ+` PRIMARY KEY, reviewer_id `+typ+`, субконто1 `+typ+`)`)
		preflightExec(t, db, `CREATE INDEX custom_index ON records(old_author) WHERE old_author IS NOT NULL`)
		if db.IsSQLite() {
			preflightExec(t, db, `CREATE TRIGGER custom_trigger AFTER UPDATE ON records BEGIN SELECT 1; END`)
		}
		out, err := preflightRun(t, db, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"records.author_id (сейчас old_author)", "records.newuser", "колонка отсутствует", "records_lines.editor", "действующий FK", "рег_balance.owner", "рег_balance.reviewer", "рег_balance.editor", "инфо_info.owner", "инфо_info.editor", "акк_accounts.reviewer", "акк_accounts.субконто1", "отсутствует FK", "пока не довносит"} {
			if !strings.Contains(out, text) {
				t.Errorf("missing %q in CLI output:\n%s", text, out)
			}
		}
		if strings.Contains(out, "SECRET_") || strings.Contains(out, "records.business") {
			t.Fatalf("leaked data or included business refs: %s", out)
		}
		if db.IsSQLite() && !strings.Contains(out, "потребует перестройки SQLite") {
			t.Fatal(out)
		}
		again, err := preflightRun(t, db, dir, nil)
		if err != nil || again != out {
			t.Fatalf("repeat plan differs: %v\n%s", err, again)
		}
		// More than five invalid records, including zero UUID; --allow-destructive cannot repair them.
		ids := make([]string, 7)
		for i := range ids {
			ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
			value := uuid.NewString()
			if i == 0 {
				value = uuid.Nil.String()
			}
			if db.IsSQLite() && i == 1 {
				value = ""
			}
			preflightExec(t, db, `INSERT INTO records VALUES (`+db.Dialect().Placeholder(1)+`,`+db.Dialect().Placeholder(2)+`,'SECRET_BROKEN')`, ids[i], value)
		}
		out, err = preflightRun(t, db, dir, map[string]string{"allow-destructive": "true"})
		if err == nil || !strings.Contains(err.Error(), "битых ссылок: 7") {
			t.Fatalf("broken data accepted: %v\n%s", err, out)
		}
		for _, id := range ids[:5] {
			if !strings.Contains(out, id) {
				t.Errorf("missing source record %s", id)
			}
		}
		for _, id := range ids[5:] {
			if strings.Contains(out, id) {
				t.Errorf("example limit exceeded: %s", id)
			}
		}
		if strings.Contains(out, "SECRET_") {
			t.Fatalf("leaked business/user data: %s", out)
		}
		preflightExec(t, db, `INSERT INTO "инфо_info" VALUES (`+db.Dialect().Placeholder(1)+`,`+db.Dialect().Placeholder(2)+`)`, user, uuid.NewString())
		out, err = preflightRun(t, db, dir, nil)
		if err == nil || !strings.Contains(err.Error(), "битых ссылок: 8") || !strings.Contains(out, "Info.Editor") {
			t.Fatalf("info-register preflight failed: %v\n%s", err, out)
		}
		if strings.Contains(out, "SECRET_") || strings.Contains(out, user) {
			t.Fatalf("info register exposed business keys: %s", out)
		}
	})
}

func TestMigrateUsersPreflightFKSemanticsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if _, err := db.RaiseSchemaRevision(context.Background()); err != nil {
			t.Fatal(err)
		}
		dir := userReferenceProject(t)
		typ := db.Dialect().TypeUUID()
		preflightExec(t, db, `CREATE TABLE _users (id `+typ+` PRIMARY KEY)`)
		preflightExec(t, db, `CREATE TABLE сотрудники (id `+typ+` PRIMARY KEY, учетнаязапись_id `+typ+`, CONSTRAINT unusual_name FOREIGN KEY (учетнаязапись_id) REFERENCES _users(id) ON DELETE CASCADE)`)
		out, err := preflightRun(t, db, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "не эквивалентен") {
			t.Fatalf("cascade FK accepted: %s", out)
		}
		preflightExec(t, db, `DROP TABLE сотрудники`)
		preflightExec(t, db, `CREATE TABLE сотрудники (id `+typ+` PRIMARY KEY, учетнаязапись_id `+typ+`, CONSTRAINT unusual_name FOREIGN KEY (учетнаязапись_id) REFERENCES _users(id) ON DELETE RESTRICT)`)
		out, err = preflightRun(t, db, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "действующий FK") {
			t.Fatalf("equivalent FK with different name missed: %s", out)
		}
		preflightExec(t, db, `DROP TABLE сотрудники`)
		preflightExec(t, db, `CREATE TABLE сотрудники (id `+typ+` PRIMARY KEY, учетнаязапись_id `+typ+`, FOREIGN KEY (учетнаязапись_id) REFERENCES _users(id), FOREIGN KEY (учетнаязапись_id) REFERENCES _users(id) ON DELETE CASCADE)`)
		out, err = preflightRun(t, db, dir, nil)
		if err != nil || !strings.Contains(out, "не эквивалентен") {
			t.Fatalf("mixed destructive FK accepted: %v\n%s", err, out)
		}
		preflightExec(t, db, `DROP TABLE сотрудники`)
		preflightExec(t, db, `CREATE TABLE сотрудники (id `+typ+` PRIMARY KEY, учетнаязапись_id `+typ+`, CONSTRAINT unusual_name FOREIGN KEY (учетнаязапись_id) REFERENCES _users)`)
		out, err = preflightRun(t, db, dir, nil)
		if err != nil || !strings.Contains(out, "действующий FK") {
			t.Fatalf("implicit single-column PK missed: %v\n%s", err, out)
		}
		if !db.IsSQLite() {
			preflightExec(t, db, `ALTER TABLE сотрудники DROP CONSTRAINT unusual_name`)
			preflightExec(t, db, `ALTER TABLE сотрудники ADD CONSTRAINT unusual_name FOREIGN KEY (учетнаязапись_id) REFERENCES _users(id) NOT VALID`)
			out, err = preflightRun(t, db, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "не эквивалентен") {
				t.Fatalf("unvalidated FK accepted: %s", out)
			}
			preflightExec(t, db, `ALTER TABLE сотрудники VALIDATE CONSTRAINT unusual_name`)
			preflightExec(t, db, `ALTER TABLE сотрудники DISABLE TRIGGER ALL`)
			out, err = preflightRun(t, db, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "не эквивалентен") {
				t.Fatalf("disabled FK accepted: %s", out)
			}
		}
	})
}

func TestMigrateUsersPreflightMissingSchemaMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if _, err := db.RaiseSchemaRevision(context.Background()); err != nil {
			t.Fatal(err)
		}
		dir := userReferenceProject(t)
		_, err := preflightRun(t, db, dir, nil)
		if err == nil || !strings.Contains(err.Error(), "таблица _users отсутствует") {
			t.Fatalf("missing auth schema was hidden: %v", err)
		}
		// DB-config failure must not create the config repository tables either.
		_, err = preflightRun(t, db, dir, map[string]string{"config-source": "database"})
		if err == nil {
			t.Fatal("missing database configuration accepted")
		}
		preflightExec(t, db, `CREATE TABLE _users (id `+db.Dialect().TypeUUID()+` PRIMARY KEY)`)
		out, err := preflightRun(t, db, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "таблица отсутствует") {
			t.Fatal(out)
		}
		preflightExec(t, db, `CREATE TABLE сотрудники (id `+db.Dialect().TypeUUID()+` PRIMARY KEY)`)
		out, err = preflightRun(t, db, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "колонка отсутствует") {
			t.Fatal(out)
		}
	})
}

func TestMigrateUsersPreflightKeepsRevisionGatesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := userReferenceProject(t)
		_, err := preflightRun(t, db, dir, nil)
		if err == nil || !strings.Contains(err.Error(), "маркер ревизии схемы не завершён") {
			t.Fatalf("incomplete revision accepted: %v", err)
		}
		if _, err := db.RaiseSchemaRevision(context.Background()); err != nil {
			t.Fatal(err)
		}
		preflightExec(t, db, `UPDATE _schema_revision SET revision=999`)
		_, err = preflightRun(t, db, dir, nil)
		if err == nil || !strings.Contains(err.Error(), "ревизией схемы 999") {
			t.Fatalf("future revision accepted: %v", err)
		}
	})
}
