package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUniversalPortableSystemStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "portable-system-src")

	if err := src.EnsureSeqTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureSchemaMapSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureIntakeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureAIAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureRollupTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureWebhookLogSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := configdb.New(src).EnsureVersionSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := extform.New(src).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := extform.NewReports(src).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := extform.NewProcessors(src).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewRepo(src).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatal(err)
	}

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO _sequences(entity_name,last_num) VALUES (?,?)`, []any{"Orders", int64(41)}},
		{`INSERT INTO _schema_fields(table_name,field_id,column_name,field_type) VALUES (?,?,?,?)`, []any{"legacy", "field-1", "old_name", "string"}},
		{`INSERT INTO _intake_log(intake,scope,key,status) VALUES (?,?,?,?)`, []any{"Orders", "main", "event-1", "processed"}},
		{`INSERT INTO _intake_dlq(id,intake,payload,reason) VALUES (?,?,?,?)`, []any{"dlq-1", "Orders", `{"id":1}`, "retry"}},
		{`INSERT INTO _ai_audit(id,user_login,task,query,response) VALUES (?,?,?,?,?)`, []any{"ai-1", "alice", "chat", "question", "answer"}},
		{`INSERT INTO _rollup(id,cutoff,created_at,registers) VALUES (?,?,?,?)`, []any{"rollup-1", "2026-01-01", "2026-01-02", "Stock"}},
		{`INSERT INTO _config_versions(id,created_at,message,snapshot) VALUES (?,?,?,?)`, []any{"version-1", "2026-01-02T03:04:05Z", "before deploy", []byte{0, 1, 2, 255}}},
		{`INSERT INTO _ext_printforms(id,document,name,content,enabled) VALUES (?,?,?,?,?)`, []any{"pf-1", "Order", "Invoice", []byte("print-yaml"), true}},
		{`INSERT INTO _ext_reports(id,name,content,enabled) VALUES (?,?,?,?)`, []any{"report-1", "Margin", []byte("report-yaml"), true}},
		{`INSERT INTO _ext_processors(id,name,content,enabled,trusted) VALUES (?,?,?,?,?)`, []any{"processor-1", "Reprice", []byte("processor-yaml"), true, true}},
		{`INSERT INTO _webhook_log(id,webhook_name,error) VALUES (?,?,?)`, []any{"webhook-1", "notify", "target-local diagnostic"}},
		// Токен публикации — единственный носитель права доступа к файлу:
		// потеря строки после restore молча убила бы все розданные ссылки.
		{`INSERT INTO _public_files(token,blob_id,filename,cache_seconds) VALUES (?,?,?,?)`,
			[]any{"portable-token-1", "5f0f2a49-4f6e-4a37-9d5e-2b8f6c3d1e0a", "logo.png", 600}},
	}
	for _, stmt := range statements {
		if _, err := src.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed %q: %v", stmt.query, err)
		}
	}

	var archive bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", testConfigDir(t), "", "portable", &archive); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	entries := zipEntryNames(t, archive.Bytes())
	for _, tableName := range []string{
		"_sequences", "_schema_fields", "_intake_log", "_intake_dlq",
		"_ai_audit", "_rollup", "_config_versions", "_ext_printforms",
		"_ext_reports", "_ext_processors", "_public_files",
	} {
		if !entries["system/"+tableName+".jsonl"] {
			t.Errorf("portable table %s is absent from archive", tableName)
		}
	}
	for _, tableName := range []string{
		"_sessions", "_api_tokens", "_auth_bind_tickets", "_accounts",
		"_fts", "_webhook_log",
	} {
		if entries["system/"+tableName+".jsonl"] || entries["data/"+tableName+".jsonl"] {
			t.Errorf("target-local, credential, or derived table %s leaked into archive", tableName)
		}
	}

	dst := newSQLite(t, "portable-system-dst")
	if err := dst.EnsureWebhookLogSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO _webhook_log(id,webhook_name,error) VALUES (?,?,?)`, "old", "old", "old"); err != nil {
		t.Fatal(err)
	}
	report, err := ImportUniversal(ctx, dst, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}
	for _, tableName := range []string{
		"_sequences", "_schema_fields", "_intake_log", "_intake_dlq",
		"_ai_audit", "_rollup", "_config_versions", "_ext_printforms",
		"_ext_reports", "_ext_processors", "_public_files",
	} {
		if got := report.Tables[tableName]; got != 1 {
			t.Errorf("report.Tables[%s] = %d, want 1", tableName, got)
		}
		var count int
		if err := dst.QueryRow(ctx, "SELECT COUNT(*) FROM "+quotedIdent(dst, tableName)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tableName, err)
		}
		if count != 1 {
			t.Errorf("restored %s row count = %d, want 1", tableName, count)
		}
	}

	var lastNum int64
	if err := dst.QueryRow(ctx, `SELECT last_num FROM _sequences WHERE entity_name=?`, "Orders").Scan(&lastNum); err != nil || lastNum != 41 {
		t.Fatalf("sequence state = %d, err=%v; want 41", lastNum, err)
	}
	var snapshot []byte
	if err := dst.QueryRow(ctx, `SELECT snapshot FROM _config_versions WHERE id=?`, "version-1").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot, []byte{0, 1, 2, 255}) {
		t.Fatalf("configuration snapshot = %v", snapshot)
	}
	var webhookCount int
	if err := dst.QueryRow(ctx, `SELECT COUNT(*) FROM _webhook_log`).Scan(&webhookCount); err != nil {
		t.Fatal(err)
	}
	if webhookCount != 0 {
		t.Fatalf("target-local webhook history survived restore: %d rows", webhookCount)
	}
}

func TestRestorePreMigrationSchemaMapControlsPhysicalRename(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "pre-migration-schema-map")
	if err := db.EnsureSchemaMapSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, old_name TEXT)`); err != nil {
		t.Fatal(err)
	}
	// This target-local map deliberately points at a non-existent column. If it
	// survives until migration, old_name is orphaned and a blank new_name column
	// is added instead of preserving the physical column by rename.
	if _, err := db.Exec(ctx, `INSERT INTO _schema_fields(table_name,field_id,column_name,field_type) VALUES (?,?,?,?)`,
		"items", "field_1", "unrelated", "string"); err != nil {
		t.Fatal(err)
	}

	systemDir := t.TempDir()
	mapJSONL := "{\"_schema\":1}\n" +
		"{\"table_name\":\"items\",\"field_id\":\"field_1\",\"column_name\":\"old_name\",\"field_type\":\"string\"}\n"
	if err := os.WriteFile(filepath.Join(systemDir, "_schema_fields.jsonl"), []byte(mapJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	report := &ImportReport{Tables: map[string]int{}}
	if err := restorePreMigrationSystemTables(ctx, db, systemDir, report); err != nil {
		t.Fatal(err)
	}
	if report.Tables["_schema_fields"] != 1 {
		t.Fatalf("early _schema_fields report count = %d", report.Tables["_schema_fields"])
	}

	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "name: Items\nfields:\n  - id: field_1\n    name: new_name\n    type: string\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "catalogs", "items.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchema(ctx, db, "file", cfgDir); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}

	cols := sqliteTableColumns(t, ctx, db, "items")
	if !cols["new_name"] {
		t.Fatalf("source schema map did not rename old_name: columns=%v", cols)
	}
	if cols["old_name"] {
		t.Fatalf("old_name was orphaned instead of renamed: columns=%v", cols)
	}
}

func TestRestorePreMigrationSchemaMapClearsStaleStateForLegacyArchive(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "legacy-pre-migration-map")
	if err := db.EnsureSchemaMapSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _schema_fields(table_name,field_id,column_name,field_type) VALUES (?,?,?,?)`,
		"old", "field", "column", "string"); err != nil {
		t.Fatal(err)
	}
	if err := restorePreMigrationSystemTables(ctx, db, t.TempDir(), &ImportReport{Tables: map[string]int{}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _schema_fields`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy archive retained %d stale target schema-map rows", count)
	}
}

func zipEntryNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

func sqliteTableColumns(t *testing.T, ctx context.Context, db *storage.DB, tableName string) map[string]bool {
	t.Helper()
	rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteQuote(tableName)+")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
