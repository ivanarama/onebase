package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestImportUniversalReplacesCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "atomic-success-src")
	if _, err := src.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO items(id,name) VALUES ('new-id','new value')`); err != nil {
		t.Fatal(err)
	}
	srcConfig := testConfigDir(t)
	srcAttachments := t.TempDir()
	srcAttachmentPath, _ := createDiskAttachmentForBackup(t, src, srcAttachments, []byte("new attachment"))
	srcAttachmentRel, err := filepath.Rel(srcAttachments, srcAttachmentPath)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", srcConfig, srcAttachments, "atomic", &archive); err != nil {
		t.Fatal(err)
	}

	dst := newSQLite(t, "atomic-success-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO items(id,name) VALUES ('old-id','old value')`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `CREATE TABLE obsolete (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO obsolete(id) VALUES ('must-disappear')`); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(dst)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := repo.Create(ctx, "old-admin", "S3cret-pass", "Old Admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise}); err != nil {
		t.Fatal(err)
	}
	if err := dst.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO _settings(key,value) VALUES ('ai.daily_token_cap','999')`); err != nil {
		t.Fatal(err)
	}

	parent := t.TempDir()
	dstConfig := filepath.Join(parent, "project")
	dstAttachments := filepath.Join(parent, "files")
	for _, dir := range []string{dstConfig, filepath.Join(dstConfig, ".git"), filepath.Join(dstConfig, "backups"), dstAttachments} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(dstConfig, "onebase.yaml"), "name: old\n")
	writeTestFile(t, filepath.Join(dstConfig, "removed.yaml"), "remove me")
	writeTestFile(t, filepath.Join(dstConfig, ".git", "HEAD"), "keep git")
	writeTestFile(t, filepath.Join(dstConfig, "backups", "before.obz"), "keep backup")
	writeTestFile(t, filepath.Join(dstAttachments, "old.bin"), "remove old attachment")

	report, err := ImportUniversal(ctx, dst, "file", dstConfig, dstAttachments,
		bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}
	if report.Files != 1 {
		t.Fatalf("restored attachments = %d, want 1", report.Files)
	}

	var itemID, itemName string
	if err := dst.QueryRow(ctx, `SELECT id,name FROM items`).Scan(&itemID, &itemName); err != nil {
		t.Fatal(err)
	}
	if itemID != "new-id" || itemName != "new value" {
		t.Fatalf("restored item = %q/%q", itemID, itemName)
	}
	assertTableCount(t, ctx, dst, "obsolete", 0)
	assertTableCount(t, ctx, dst, "_sessions", 0)
	var portable int
	if err := dst.QueryRow(ctx, `SELECT COUNT(*) FROM _settings WHERE key='ai.daily_token_cap'`).Scan(&portable); err != nil {
		t.Fatal(err)
	}
	if portable != 0 {
		t.Fatalf("portable setting absent from snapshot survived: %d", portable)
	}

	if got := readTestFile(t, filepath.Join(dstConfig, "onebase.yaml")); got != "name: test\n" {
		t.Fatalf("configuration = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dstConfig, "removed.yaml")); !os.IsNotExist(err) {
		t.Fatalf("configuration file absent from snapshot survived: %v", err)
	}
	if got := readTestFile(t, filepath.Join(dstConfig, ".git", "HEAD")); got != "keep git" {
		t.Fatalf("preserved .git content = %q", got)
	}
	if got := readTestFile(t, filepath.Join(dstConfig, "backups", "before.obz")); got != "keep backup" {
		t.Fatalf("preserved backup content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dstAttachments, "old.bin")); !os.IsNotExist(err) {
		t.Fatalf("old attachment survived snapshot restore: %v", err)
	}
	if got := readTestFile(t, filepath.Join(dstAttachments, srcAttachmentRel)); got != "new attachment" {
		t.Fatalf("restored attachment = %q", got)
	}
}

func TestImportUniversalLateFailureRollsBackDatabaseAndFiles(t *testing.T) {
	ctx := context.Background()
	archive := buildUniversalAtomicFixture(t, map[string]string{
		"META.txt":            "onebase_full_export\nversion=2\nformat=universal\nhas_exchange_state=false\n",
		"manifest.json":       `{"data/ghost.jsonl":1}`,
		"config/onebase.yaml": "name: replacement\n",
		"data/ghost.jsonl":    "{\"_schema\":1}\n{\"id\":\"not-importable\"}\n",
	})

	dst := newSQLite(t, "atomic-failure-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE keep_rows (id TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO keep_rows(id,value) VALUES ('old','must survive')`); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	dstConfig := filepath.Join(parent, "project")
	dstAttachments := filepath.Join(parent, "files")
	if err := os.MkdirAll(dstConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstAttachments, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dstConfig, "onebase.yaml"), "name: original\n")
	writeTestFile(t, filepath.Join(dstAttachments, "old.bin"), "original attachment")

	if _, err := ImportUniversal(ctx, dst, "file", dstConfig, dstAttachments,
		bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("ImportUniversal error = nil, want late table-count failure")
	}
	var value string
	if err := dst.QueryRow(ctx, `SELECT value FROM keep_rows WHERE id='old'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "must survive" {
		t.Fatalf("database rollback value = %q", value)
	}
	if got := readTestFile(t, filepath.Join(dstConfig, "onebase.yaml")); got != "name: original\n" {
		t.Fatalf("configuration changed on failed restore: %q", got)
	}
	if got := readTestFile(t, filepath.Join(dstAttachments, "old.bin")); got != "original attachment" {
		t.Fatalf("attachments changed on failed restore: %q", got)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".restore-stage-") || strings.Contains(entry.Name(), ".restore-old-") {
			t.Fatalf("restore temporary tree leaked after rollback: %s", entry.Name())
		}
	}
}

func TestListAppTablesExcludesSQLiteInternalTables(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "sqlite-internal-list")
	if _, err := db.Exec(ctx, `CREATE TABLE numbered (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	tables, err := listAppTables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0] != "numbered" {
		t.Fatalf("application tables = %v, want [numbered]", tables)
	}
}

func TestImportTableJSONLQuotesArchiveColumnNames(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "quoted-archive-column")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE must_survive (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	evilColumn := `value" TEXT); DROP TABLE must_survive; --`
	row, err := json.Marshal(map[string]any{"id": "row-1", evilColumn: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonl, append([]byte("{\"_schema\":1}\n"), append(row, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importTableJSONL(ctx, db, "target", jsonl); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	exists, err := tableExistsChecked(ctx, db, "must_survive")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("archive column name executed SQL instead of being quoted")
	}
}

func TestImportTableJSONLDiscoversColumnsAfterFirstRow(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "late-archive-column")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	contents := "{\"_schema\":1}\n" +
		"{\"id\":\"first\"}\n" +
		"{\"id\":\"second\",\"late_value\":\"preserved\"}\n"
	if err := os.WriteFile(jsonl, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := importTableJSONL(ctx, db, "target", jsonl); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	} else if n != 2 {
		t.Fatalf("imported rows = %d, want 2", n)
	}
	var got string
	if err := db.QueryRow(ctx, `SELECT late_value FROM target WHERE id='second'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "preserved" {
		t.Fatalf("late column value = %q", got)
	}
}

func TestImportTableJSONLRejectsDuplicateKeys(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "duplicate-archive-key")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	contents := "{\"_schema\":1}\n" +
		"{\"id\":\"duplicate\",\"value\":\"first\"}\n" +
		"{\"id\":\"duplicate\",\"value\":\"second\"}\n"
	if err := os.WriteFile(jsonl, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importTableJSONL(ctx, db, "target", jsonl); err == nil {
		t.Fatal("importTableJSONL accepted duplicate primary key")
	}
}

func TestImportTableJSONLPreservesLargeInteger(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "large-json-integer")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id TEXT PRIMARY KEY, value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	const want int64 = 9007199254740993 // 2^53 + 1: not exactly representable as float64.
	contents := "{\"_schema\":1}\n{\"id\":\"row\",\"value\":9007199254740993}\n"
	if err := os.WriteFile(jsonl, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importTableJSONL(ctx, db, "target", jsonl); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	var got int64
	if err := db.QueryRow(ctx, `SELECT value FROM target WHERE id='row'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("large integer = %d, want %d", got, want)
	}
}

func TestImportTableJSONLPreservesExactDecimalLexeme(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "exact-json-decimal")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	const want = "12345678901234567890.12345678901234567890"
	contents := "{\"_schema\":1}\n{\"id\":\"row\",\"value\":" + want + "}\n"
	if err := os.WriteFile(jsonl, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importTableJSONL(ctx, db, "target", jsonl); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	var got string
	if err := db.QueryRow(ctx, `SELECT value FROM target WHERE id='row'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decimal = %q, want %q", got, want)
	}
}

func TestImportTableJSONLRejectsEmptyRow(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "empty-json-row")
	if _, err := db.Exec(ctx, `CREATE TABLE target (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	jsonl := filepath.Join(t.TempDir(), "target.jsonl")
	if err := os.WriteFile(jsonl, []byte("{\"_schema\":1}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := importTableJSONL(ctx, db, "target", jsonl); err == nil {
		t.Fatal("importTableJSONL accepted a row that inserted no data")
	}
}

func TestValidateUniversalArchiveRejectsNonPortableAliases(t *testing.T) {
	cases := []string{
		`config\\onebase.yaml`,
		"config/../manifest.json",
		"config/file:stream",
		"config/NUL.txt",
		"config/trailing.",
		"config/trailing ",
		"config//onebase.yaml",
	}
	for _, archiveName := range cases {
		t.Run(strings.ReplaceAll(archiveName, "/", "_"), func(t *testing.T) {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			w, err := zw.Create(archiveName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateUniversalArchive(zr); err == nil {
				t.Fatalf("validateUniversalArchive accepted %q", archiveName)
			}
		})
	}
}

func buildUniversalAtomicFixture(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, key := range keys {
		w, err := zw.Create(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[key])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-owned temporary path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertTableCount(t *testing.T, ctx context.Context, db *storage.DB, tableName string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+quotedIdent(db, tableName)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", tableName, got, want)
	}
}
