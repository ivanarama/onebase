package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestIsInMemorySQLite(t *testing.T) {
	cases := map[string]bool{
		":memory:":                       true,
		"file::memory:":                  true,
		"file::memory:?cache=shared":     true,
		"file:test?mode=memory":          true,
		"file:/tmp/x.db?mode=memory":     true,
		"prodbase.db":                    false,
		"/var/data/base.db":              false,
		"file:/tmp/real.db?cache=shared": false,
	}
	for path, want := range cases {
		if got := isInMemorySQLite(path); got != want {
			t.Errorf("isInMemorySQLite(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDisableFKForImportKeepsSQLiteSingleConnection(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "fk-import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cleanup, err := db.DisableFKForImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := db.sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections after FK import cleanup = %d, want 1", got)
	}
}

func TestBeginDurableTxPinsFullSynchronousAndRestoresMode(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "durable.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	var before int
	if err := db.QueryRow(ctx, "PRAGMA synchronous").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 1 { // SQLITE_SYNC_NORMAL
		t.Fatalf("initial synchronous = %d, want NORMAL (1)", before)
	}
	tx, txCtx, err := db.BeginDurableTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var during int
	if err := db.QueryRow(txCtx, "PRAGMA synchronous").Scan(&during); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	if during != 2 { // SQLITE_SYNC_FULL
		_ = tx.Rollback(txCtx)
		t.Fatalf("durable transaction synchronous = %d, want FULL (2)", during)
	}
	if _, err := db.Exec(txCtx, "PRAGMA synchronous=NORMAL"); err == nil {
		_ = tx.Rollback(txCtx)
		t.Fatal("SQLite allowed synchronous downgrade inside durable transaction")
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow(ctx, "PRAGMA synchronous").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("restored synchronous = %d, want previous %d", after, before)
	}
}

func TestBeginDurableTxRestoresModeAfterRollback(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "durable-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tx, txCtx, err := db.BeginDurableTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow(ctx, "PRAGMA synchronous").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 1 {
		t.Fatalf("synchronous after rollback = %d, want NORMAL (1)", after)
	}
}

func TestSQLiteDurableSessionCoversAutocommitAndTransactionBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "durable-session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, "CREATE TABLE durability_events (name TEXT NOT NULL, mode INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	session, err := db.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionCtx := session.Context()
	assertMode := func(queryCtx context.Context, boundary string, want int) {
		t.Helper()
		var mode int
		if err := db.QueryRow(queryCtx, "PRAGMA synchronous").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != want {
			t.Fatalf("synchronous at %s = %d, want %d", boundary, mode, want)
		}
	}
	assertMode(sessionCtx, "pending intent", 2)
	if _, err := db.Exec(sessionCtx, "INSERT INTO durability_events(name,mode) VALUES('pending',2)"); err != nil {
		t.Fatal(err)
	}
	tx, txCtx, err := db.BeginTx(sessionCtx)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(txCtx, "committed marker", 2)
	if _, err := db.Exec(txCtx, "INSERT INTO durability_events(name,mode) VALUES('transaction',2)"); err != nil {
		_ = tx.Rollback(txCtx)
		t.Fatal(err)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	assertMode(sessionCtx, "final marker delete", 2)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(ctx, "after session", 1)

	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM durability_events").Scan(&count); err != nil || count != 2 {
		t.Fatalf("durability events count = %d err=%v", count, err)
	}
}

func TestSQLiteDurableSessionRejectsForeignDatabaseContext(t *testing.T) {
	ctx := context.Background()
	first, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "first.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	second, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	session, err := first.BeginDurableSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close durable session: %v", err)
		}
	}()
	var secondMode int
	if err := second.QueryRow(session.Context(), "PRAGMA synchronous").Scan(&secondMode); err != nil {
		t.Fatal(err)
	}
	if secondMode != 1 {
		t.Fatalf("foreign DB inherited durable session: synchronous=%d", secondMode)
	}
}

// In-memory база подключается без создания файла на диске и работает как
// обычная (миграция + запись/чтение). Раннер тестов полагается на это для
// `onebase test --sqlite :memory:`.
func TestSQLiteInMemory(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, ":memory:")
	if err != nil {
		t.Fatalf("ConnectSQLite(:memory:): %v", err)
	}
	defer db.Close()

	if _, err := db.sqlDB.ExecContext(ctx, "CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.sqlDB.ExecContext(ctx, "INSERT INTO t(v) VALUES('x')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var n int
	if err := db.sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("select: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	// Файл с именем «:memory:» не должен появиться в рабочей папке.
	if _, err := os.Stat(":memory:"); err == nil {
		_ = os.Remove(":memory:")
		t.Fatal("in-memory подключение создало файл «:memory:» на диске")
	}
}

func TestSQLiteSmoke(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	if !db.IsSQLite() || db.IsPostgres() {
		t.Fatalf("unexpected backend flags: sqlite=%v pg=%v", db.IsSQLite(), db.IsPostgres())
	}
	if db.Dialect().Name() != "sqlite" {
		t.Fatalf("dialect name = %q, want sqlite", db.Dialect().Name())
	}
	if got := db.Dialect().Placeholder(3); got != "?" {
		t.Fatalf("Placeholder(3) = %q, want ?", got)
	}

	// DDL — use dialect types so the same source works on PG too.
	d := db.Dialect()
	createSQL := "CREATE TABLE t (id " + d.TypeUUID() + " PRIMARY KEY, name " + d.TypeText() +
		", amount " + d.TypeNumber(18, 4) + ", created_at " + d.TypeTimestamp() + " DEFAULT " + d.CurrentTimestampTZ() + ")"
	if _, err := db.Exec(ctx, createSQL); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert via placeholders.
	ph1, ph2, ph3 := d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)
	insertSQL := "INSERT INTO t(id,name,amount) VALUES(" + ph1 + "," + ph2 + "," + ph3 + ")"
	tag, err := db.Exec(ctx, insertSQL, "id-1", "alpha", "12.34")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if tag.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1", tag.RowsAffected)
	}

	// QueryRow single value.
	var name string
	if err := db.QueryRow(ctx, "SELECT name FROM t WHERE id="+ph1, "id-1").Scan(&name); err != nil {
		t.Fatalf("queryRow: %v", err)
	}
	if name != "alpha" {
		t.Fatalf("name = %q, want alpha", name)
	}

	// Query rows.
	rows, err := db.Query(ctx, "SELECT id, name FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, n string
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows count = %d, want 1", count)
	}

	// Transaction — insert two rows, rollback, expect still 1 total.
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := db.Exec(txCtx, insertSQL, "id-2", "beta", "0"); err != nil {
		t.Fatalf("tx insert 1: %v", err)
	}
	if _, err := db.Exec(txCtx, insertSQL, "id-3", "gamma", "0"); err != nil {
		t.Fatalf("tx insert 2: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var total int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&total); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if total != 1 {
		t.Fatalf("after rollback total = %d, want 1", total)
	}

	// ColumnExists via dialect.
	exists, err := d.ColumnExists(ctx, db, "t", "name")
	if err != nil {
		t.Fatalf("ColumnExists: %v", err)
	}
	if !exists {
		t.Fatal("column 'name' not found via PRAGMA")
	}
	exists, err = d.ColumnExists(ctx, db, "t", "missing")
	if err != nil {
		t.Fatalf("ColumnExists missing: %v", err)
	}
	if exists {
		t.Fatal("column 'missing' should not exist")
	}
}

func TestSQLiteMigrateMinimal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate.db")

	db, err := ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	if err := db.EnsureSeqTable(ctx); err != nil {
		t.Fatalf("EnsureSeqTable: %v", err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatalf("EnsureNumeratorSchema: %v", err)
	}

	// Run a real migration for two simple entities (catalog + document).
	entities := []*metadata.Entity{
		{
			Name: "Counterparty",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Name", Type: metadata.FieldTypeString},
				{Name: "INN", Type: metadata.FieldTypeString},
			},
		},
		{
			Name: "Invoice",
			Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Number", Type: metadata.FieldTypeString},
				{Name: "Date", Type: metadata.FieldTypeDate},
				{Name: "Counterparty", Type: "reference:Counterparty", RefEntity: "Counterparty"},
				{Name: "Amount", Type: metadata.FieldTypeNumber},
			},
		},
	}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Verify catalog table exists and has expected columns.
	exists, err := db.Dialect().ColumnExists(ctx, db, "counterparty", "inn")
	if err != nil {
		t.Fatalf("ColumnExists: %v", err)
	}
	if !exists {
		t.Fatal("counterparty.inn column missing after migrate")
	}
	exists, _ = db.Dialect().ColumnExists(ctx, db, "invoice", "posted")
	if !exists {
		t.Fatal("invoice.posted column missing after migrate")
	}
	exists, _ = db.Dialect().ColumnExists(ctx, db, "invoice", "deletion_mark")
	if !exists {
		t.Fatal("invoice.deletion_mark column missing after migrate")
	}

	// Verify system schemas (audit, attachments, scheduled, constants) work on SQLite.
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("EnsureAuditSchema: %v", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("EnsureAttachmentTable: %v", err)
	}
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		t.Fatalf("EnsureScheduledRunsTable: %v", err)
	}
	if err := db.MigrateConstants(ctx, nil); err != nil {
		t.Fatalf("MigrateConstants: %v", err)
	}

	// Test seq numbering — RETURNING + ON CONFLICT must work on SQLite.
	n1, err := db.NextNum(ctx, "Invoice")
	if err != nil {
		t.Fatalf("NextNum first: %v", err)
	}
	n2, _ := db.NextNum(ctx, "Invoice")
	if n2 != n1+1 {
		t.Fatalf("NextNum: %d → %d, expected sequential", n1, n2)
	}

	// Constant set/get with JSON-roundtrip.
	if err := db.SetConstant(ctx, "TestKey", "hello"); err != nil {
		t.Fatalf("SetConstant: %v", err)
	}
	v, err := db.GetConstant(ctx, "TestKey")
	if err != nil {
		t.Fatalf("GetConstant: %v", err)
	}
	if v != "hello" {
		t.Fatalf("constant = %v, want hello", v)
	}

	// End-to-end CRUD on SQLite: insert catalog entry, fetch by id, list with
	// search filter, count.
	cat := entities[0] // Counterparty
	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Name": "Alfa", "INN": "1234567890"}, cat); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := db.GetByID(ctx, cat.Name, id, cat)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got["Name"] != "Alfa" {
		t.Fatalf("GetByID Name = %v, want Alfa", got["Name"])
	}

	// Add second row and verify List + filter.
	id2 := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id2, map[string]any{"Name": "Beta", "INN": "9876543210"}, cat); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	rows, err := db.List(ctx, cat.Name, cat, ListParams{Search: "alfa"})
	if err != nil {
		t.Fatalf("List with search: %v", err)
	}
	if len(rows) != 1 || rows[0]["Name"] != "Alfa" {
		t.Fatalf("List search alfa: got %d rows, expected 1 with Name=Alfa: %v", len(rows), rows)
	}

	total, err := db.CountList(ctx, cat.Name, cat, ListParams{})
	if err != nil {
		t.Fatalf("CountList: %v", err)
	}
	if total != 2 {
		t.Fatalf("CountList = %d, want 2", total)
	}
}

func TestSQLiteMigrateCreatesEntityAndTablePartIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "indexes.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	cat := &metadata.Entity{
		Name: "Counterparty",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "INN", Type: metadata.FieldTypeString},
		},
		Indexes: []metadata.IndexSpec{{Fields: []string{"INN"}, Unique: true}},
	}
	doc := &metadata.Entity{
		Name: "Invoice",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Date", Type: metadata.FieldTypeDate},
			{Name: "Counterparty", Type: "reference:Counterparty", RefEntity: "Counterparty"},
		},
		Indexes: []metadata.IndexSpec{{Fields: []string{"Counterparty", "Date"}}},
		TableParts: []metadata.TablePart{{
			Name:   "Rows",
			Fields: []metadata.Field{{Name: "Qty", Type: metadata.FieldTypeNumber}},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat, doc}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	uniqueName := stableIndexName("counterparty", []string{"inn"}, true)
	if !sqliteIndexExists(t, db, "counterparty", uniqueName, true, []string{"inn"}) {
		t.Fatalf("unique index %s on counterparty(inn) not found", uniqueName)
	}
	docIndexName := stableIndexName("invoice", []string{"counterparty_id", "date"}, false)
	if !sqliteIndexExists(t, db, "invoice", docIndexName, false, []string{"counterparty_id", "date"}) {
		t.Fatalf("index %s on invoice(counterparty_id,date) not found", docIndexName)
	}
	tpTable := metadata.TablePartTableName("Invoice", "Rows")
	tpIndexName := stableIndexName(tpTable, []string{"parent_id", "строка"}, false)
	if !sqliteIndexExists(t, db, tpTable, tpIndexName, false, []string{"parent_id", "строка"}) {
		t.Fatalf("tablepart index %s on %s(parent_id,строка) not found", tpIndexName, tpTable)
	}
}

func TestSQLiteMigrateRegistersCreatesPeriodAndDimensionIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "reg-indexes.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	reg := &metadata.Register{
		Name: "ОстаткиТоваров",
		Dimensions: []metadata.Field{
			{Name: "Склад", Type: "reference:Склады", RefEntity: "Склады"},
			{Name: "Номенклатура", Type: "reference:Номенклатура", RefEntity: "Номенклатура"},
			{Name: "Серия", Type: metadata.FieldTypeString},
			{Name: "Качество", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatalf("MigrateRegisters: %v", err)
	}

	table := metadata.RegisterTableName(reg.Name)
	periodIndex := stableIndexName(table, []string{"period"}, false)
	if !sqliteIndexExists(t, db, table, periodIndex, false, []string{"period"}) {
		t.Fatalf("period index %s on %s(period) not found", periodIndex, table)
	}
	dimCols := []string{"склад_id", "номенклатура_id", "серия", "period"}
	dimIndex := stableIndexName(table, dimCols, false)
	if !sqliteIndexExists(t, db, table, dimIndex, false, dimCols) {
		t.Fatalf("dimension index %s on %s(%v) not found", dimIndex, table, dimCols)
	}
}

func sqliteIndexExists(t *testing.T, db *DB, table, index string, unique bool, wantCols []string) bool {
	t.Helper()
	rows, err := db.Query(context.Background(), "PRAGMA index_list("+sqliteIdent(table)+")")
	if err != nil {
		t.Fatalf("index_list %s: %v", table, err)
	}
	found := false
	for rows.Next() {
		var seq int
		var name, origin string
		var uniqueInt, partial int
		if err := rows.Scan(&seq, &name, &uniqueInt, &origin, &partial); err != nil {
			t.Fatalf("scan index_list: %v", err)
		}
		if name == index {
			if (uniqueInt == 1) != unique {
				t.Fatalf("index %s unique = %v, want %v", index, uniqueInt == 1, unique)
			}
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("index_list rows: %v", err)
	}
	rows.Close()
	if !found {
		return false
	}
	cols := sqliteIndexColumns(t, db, index)
	if len(cols) != len(wantCols) {
		t.Fatalf("index %s columns = %v, want %v", index, cols, wantCols)
	}
	for i := range cols {
		if cols[i] != wantCols[i] {
			t.Fatalf("index %s columns = %v, want %v", index, cols, wantCols)
		}
	}
	return true
}

func sqliteIndexColumns(t *testing.T, db *DB, index string) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), "PRAGMA index_info("+sqliteIdent(index)+")")
	if err != nil {
		t.Fatalf("index_info %s: %v", index, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatalf("scan index_info: %v", err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_info rows: %v", err)
	}
	return cols
}

// TestSQLiteCyrillicCaseInsensitive проверяет, что отбор и полнотекстовый
// поиск по кириллице регистронезависимы на SQLite. Встроенная LOWER() в
// SQLite приводит к нижнему регистру только ASCII — без ob_lower (см. init в
// sqlite.go) этот тест падал бы для русского текста.
func TestSQLiteCyrillicCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()

	cat := &metadata.Entity{
		Name: "Counterparty",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Name": "Иванов"}, cat); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Поиск в нижнем регистре должен найти запись «Иванов».
	rows, err := db.List(ctx, cat.Name, cat, ListParams{Search: "иванов"})
	if err != nil {
		t.Fatalf("List search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Search 'иванов': got %d rows, want 1 (Иванов)", len(rows))
	}

	// Отбор по полю Name в верхнем регистре должен найти ту же запись.
	rows, err = db.List(ctx, cat.Name, cat, ListParams{
		Filters: map[string]FilterValue{"Name": {Value: "ИВАН"}},
	})
	if err != nil {
		t.Fatalf("List filter: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Filter Name~'ИВАН': got %d rows, want 1 (Иванов)", len(rows))
	}
}

func TestSQLiteDialectLatestPerKey(t *testing.T) {
	d := SQLiteDialect{}
	sql := d.LatestPerKey(
		[]string{"k", "v"},
		[]string{"k"},
		[]string{"ts DESC"},
		"reg",
		"r",
		"k IS NOT NULL",
	)
	want := "SELECT k, v FROM (SELECT k, v, ROW_NUMBER() OVER (PARTITION BY k ORDER BY ts DESC) AS _rn FROM reg AS r WHERE k IS NOT NULL) _w WHERE _rn = 1"
	if sql != want {
		t.Fatalf("LatestPerKey:\n  got:  %s\n  want: %s", sql, want)
	}
}
