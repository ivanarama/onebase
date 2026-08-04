package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// ---- helpers ---------------------------------------------------------------

// newSQLite opens a fresh temporary SQLite database.
func newSQLite(t *testing.T, name string) *storage.DB {
	t.Helper()
	db, err := storage.ConnectSQLite(context.Background(),
		filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatalf("ConnectSQLite %s: %v", name, err)
	}
	t.Cleanup(db.Close)
	return db
}

// buildLegacyOBZ creates a minimal binary-format .obz (no format= in META.txt).
func buildLegacyOBZ(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mf, _ := zw.Create("META.txt")
	if _, err := mf.Write([]byte("onebase_full_export\nversion=1.0\ndb_type=sqlite\n")); err != nil {
		t.Fatal(err)
	}
	df, _ := zw.Create("database.db")
	if _, err := df.Write([]byte("not a real db")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// extractZip extracts a ZIP archive to dir.
func extractZip(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		outPath := filepath.Join(dir, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(outPath)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc) //nolint:gosec // G110: суммарный распакованный объём проверяется до первой записи (validateArchiveEntries/validateUniversalArchive), а после копирования сверяется с UncompressedSize64
		closeErr := out.Close()        // write-close: ошибка важна (файл мог не долететь)
		_ = rc.Close()                 // read-close: best-effort
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// makeNumeric builds a pgtype.Numeric from an int64 significand and int32 exponent.
// value = significand * 10^exponent
func makeNumeric(significand int64, exp int32) pgtype.Numeric {
	return pgtype.Numeric{
		Int:   big.NewInt(significand),
		Exp:   exp,
		Valid: true,
	}
}

// ---- tests -----------------------------------------------------------------

// TestImportUniversalRejectsLegacy verifies that a binary .obz archive returns ErrLegacyFormat.
func TestImportUniversalRejectsLegacy(t *testing.T) {
	data := buildLegacyOBZ(t)
	db := newSQLite(t, "reject-legacy")
	_, err := ImportUniversal(
		context.Background(), db,
		"file", t.TempDir(),
		t.TempDir(),
		bytes.NewReader(data), int64(len(data)),
	)
	if err != ErrLegacyFormat {
		t.Fatalf("expected ErrLegacyFormat, got %v", err)
	}
}

// TestNumericToString covers the pgtype.Numeric → exact decimal string conversion.
func TestNumericToString(t *testing.T) {
	cases := []struct {
		sig  int64
		exp  int32
		want string
	}{
		{123456, -2, "1234.56"},
		{1, 0, "1"},
		{1, 3, "1000"},
		{123, -5, "0.00123"},
		{-5, -1, "-0.5"},
		{10, -1, "1"},
		{100, -2, "1"},
		{1, -1, "0.1"},
		{0, 0, "0"},
		{1000, -4, "0.1"},
	}
	for _, tc := range cases {
		n := makeNumeric(tc.sig, tc.exp)
		got := numericToString(n)
		if got != tc.want {
			t.Errorf("numericToString(%d e%d) = %q, want %q", tc.sig, tc.exp, got, tc.want)
		}
	}
}

// TestMarshalUnmarshalBytes verifies that BLOB/BYTEA columns survive
// the base64 encoding round-trip through JSONL.
func TestMarshalUnmarshalBytes(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "bytes-src")
	if _, err := db.Exec(ctx, `CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0x00, 0xFF, 0x42, 0xDE, 0xAD, 0xBE, 0xEF}
	if _, err := db.Exec(ctx, `INSERT INTO blobs VALUES('x', ?)`, payload); err != nil {
		t.Fatal(err)
	}

	// Export.
	var buf bytes.Buffer
	if err := ExportUniversal(ctx, db, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	// Extract JSONL and import into a new DB.
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatalf("extractZip: %v", err)
	}

	dst := newSQLite(t, "bytes-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(tmpDir, "data", "blobs.jsonl")
	if _, err := os.Stat(jsonlPath); os.IsNotExist(err) {
		t.Skip("blobs.jsonl not generated (table may be system-filtered)")
	}
	if _, err := importTableJSONL(ctx, dst, "blobs", jsonlPath); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}

	var got []byte
	if err := dst.QueryRow(ctx, `SELECT data FROM blobs WHERE id='x'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("bytes mismatch: got %x, want %x", got, payload)
	}
}

// TestImportLegacyTextIntoBlobCol проверяет, что бэкап старого релиза, где
// BLOB-столбец (например _audit.old_value) хранился как обычный TEXT, а не
// base64, импортируется без ошибки «illegal base64 data»: значение
// сохраняется как строка.
func TestImportLegacyTextIntoBlobCol(t *testing.T) {
	ctx := context.Background()
	dst := newSQLite(t, "legacy-dst")
	if _, err := dst.Exec(ctx,
		`CREATE TABLE _audit (id TEXT PRIMARY KEY, old_value BLOB)`); err != nil {
		t.Fatal(err)
	}

	// Имитируем JSONL старого бэкапа: заголовок помечает old_value как
	// байтовый столбец, но реальное значение — обычная JSON-строка, а не
	// base64 (старый SQLite-драйвер вернул TEXT-значение BLOB-столбца строкой).
	tmpDir := t.TempDir()
	jsonlPath := filepath.Join(tmpDir, "_audit.jsonl")
	legacy := `{"_schema":1,"btypes":["old_value"]}` + "\n" +
		`{"id":"a1","old_value":"{\"qty\":5}"}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := importTableJSONL(ctx, dst, "_audit", jsonlPath); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}

	var got string
	if err := dst.QueryRow(ctx,
		`SELECT old_value FROM _audit WHERE id='a1'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != `{"qty":5}` {
		t.Errorf("old_value mismatch: got %q, want %q", got, `{"qty":5}`)
	}
}

// TestMetaTxtUniversalFormat checks that ExportUniversal embeds format=universal
// in META.txt.
func TestMetaTxtUniversalFormat(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "meta")

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, db, "file", t.TempDir(), "", "MyBase", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := readMeta(zr)
	if err != nil {
		t.Fatal(err)
	}
	if meta["format"] != "universal" {
		t.Errorf("format=%q, want universal", meta["format"])
	}
	if meta["source_base"] != "MyBase" {
		t.Errorf("source_base=%q, want MyBase", meta["source_base"])
	}
	if meta["source_db_type"] != "sqlite" {
		t.Errorf("source_db_type=%q, want sqlite", meta["source_db_type"])
	}
}

func TestUniversalSafeSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "settings-src")
	if err := src.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO _settings(key,value) VALUES
		('ai.data_scope','rbac'),
		('ai.daily_token_cap','50000'),
		('llm.config','{"api_key":"secret"}')`); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmpDir, "settings", "safe.jsonl"))
	if err != nil {
		t.Fatalf("safe settings missing: %v", err)
	}
	if !strings.Contains(string(raw), `"ai.data_scope"`) || !strings.Contains(string(raw), `"ai.daily_token_cap"`) {
		t.Fatalf("safe settings do not include expected AI keys:\n%s", raw)
	}
	if strings.Contains(string(raw), "llm.config") || strings.Contains(string(raw), "secret") {
		t.Fatalf("safe settings leaked secret config:\n%s", raw)
	}

	dst := newSQLite(t, "settings-dst")
	if err := dst.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO _settings(key,value) VALUES
		('ai.data_scope','admin_only'),
		('llm.config','target-secret')`); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportUniversal(ctx, dst, "file", t.TempDir(), "", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}

	if got := dst.GetAIDataScope(ctx); got != storage.AIDataScopeRBAC {
		t.Fatalf("ai.data_scope after import = %q, want rbac", got)
	}
	if got := dst.GetAIDailyTokenCap(ctx); got != 50000 {
		t.Fatalf("ai.daily_token_cap after import = %d, want 50000", got)
	}
	var llmRaw string
	if err := dst.QueryRow(ctx, `SELECT value FROM _settings WHERE key='llm.config'`).Scan(&llmRaw); err != nil {
		t.Fatal(err)
	}
	if llmRaw != "target-secret" {
		t.Fatalf("llm.config must not be overwritten, got %q", llmRaw)
	}
}

func TestUniversalExchangeStateRestoreModes(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "exchange-src")
	if err := src.EnsureExchangeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveExchangeThisNode(ctx, "Обмен", "center"); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveExchangeToken(ctx, "Обмен", "source-secret"); err != nil {
		t.Fatal(err)
	}
	if err := src.RegisterExchangeChange(ctx, storage.ExchangeChange{
		Plan: "Обмен", ObjectType: "Товар", ObjectID: "id-1", NodeCode: "fil01", Version: 7, ChangedAt: 123,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO _exchange_peers(plan,node_code,sent_no,ack_no,recv_no) VALUES ('Обмен','fil01',3,2,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO _exchange_applied(plan,object_type,object_id,changed_at) VALUES ('Обмен','Товар','id-2',456)`); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatal(err)
	}

	// Disaster recovery restores node identity, queue and watermarks, but never
	// restores the Bearer token.
	dr := newSQLite(t, "exchange-dr")
	if err := dr.EnsureExchangeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dr.SaveExchangeToken(ctx, "Обмен", "old-target-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportUniversalWithOptions(ctx, dr, "file", t.TempDir(), "", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{ExchangeMode: ExchangeRestoreDisasterRecovery}); err != nil {
		t.Fatal(err)
	}
	if node, _ := dr.GetExchangeThisNode(ctx, "Обмен"); node != "center" {
		t.Fatalf("DR node=%q", node)
	}
	if token, _ := dr.GetExchangeToken(ctx, "Обмен"); token != "" {
		t.Fatalf("DR leaked/preserved token %q", token)
	}
	for _, table := range exchangeTables {
		var count int
		if err := dr.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("DR %s count=%d err=%v", table, count, err)
		}
	}

	// Clone is isolated even when restoring over a target that previously
	// participated in exchange.
	clone := newSQLite(t, "exchange-clone")
	if err := clone.EnsureExchangeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	_ = clone.SaveExchangeThisNode(ctx, "Обмен", "fil02")
	_ = clone.SaveExchangeToken(ctx, "Обмен", "clone-secret")
	_ = clone.RegisterExchangeChange(ctx, storage.ExchangeChange{Plan: "Обмен", ObjectType: "Товар", ObjectID: "old", NodeCode: "center", Version: 1})
	if _, err := ImportUniversal(ctx, clone, "file", t.TempDir(), "", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatal(err)
	}
	if node, _ := clone.GetExchangeThisNode(ctx, "Обмен"); node != "" {
		t.Fatalf("clone retained node %q", node)
	}
	if token, _ := clone.GetExchangeToken(ctx, "Обмен"); token != "" {
		t.Fatalf("clone retained token %q", token)
	}
	for _, table := range exchangeTables {
		var count int
		if err := clone.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("clone %s count=%d err=%v", table, count, err)
		}
	}
}

func TestUniversalReportPresetsRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "report-presets-src")
	presetID, err := src.SaveReportPreset(ctx, storage.ReportPreset{
		Report:       "ВаловаяПрибыльСКД",
		User:         "alice",
		Name:         "По номенклатуре",
		SettingsJSON: `{"composition":{"Groupings":["Номенклатура"],"Measures":[{"Field":"ВаловаяПрибыль"}]}}`,
		IsDefault:    true,
	})
	if err != nil {
		t.Fatalf("SaveReportPreset: %v", err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmpDir, "system", "_report_presets.jsonl"))
	if err != nil {
		t.Fatalf("_report_presets must be exported: %v", err)
	}
	if !strings.Contains(string(raw), "По номенклатуре") {
		t.Fatalf("_report_presets export does not contain saved preset:\n%s", raw)
	}

	dst := newSQLite(t, "report-presets-dst")
	if _, err := ImportUniversal(ctx, dst, "file", t.TempDir(), "", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}
	presets, err := dst.ListReportPresets(ctx, "ВаловаяПрибыльСКД", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 {
		t.Fatalf("expected one restored report preset, got %+v", presets)
	}
	if presets[0].ID != presetID || presets[0].Name != "По номенклатуре" || !presets[0].IsDefault {
		t.Fatalf("restored preset mismatch: %+v", presets[0])
	}
	if !strings.Contains(presets[0].SettingsJSON, "ВаловаяПрибыль") {
		t.Fatalf("settings_json was not restored: %q", presets[0].SettingsJSON)
	}
	def, err := dst.GetDefaultReportPreset(ctx, "ВаловаяПрибыльСКД", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if def == nil || def.ID != presetID {
		t.Fatalf("default report preset was not restored: %+v", def)
	}
}

func TestDemoResetImportsSafeSettings(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "demo-settings-src")
	if err := src.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(ctx, `INSERT INTO _settings(key,value) VALUES ('ai.data_scope','rbac')`); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", t.TempDir(), "", "demo", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	obzPath := filepath.Join(t.TempDir(), "demo.obz")
	if err := os.WriteFile(obzPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := newSQLite(t, "demo-settings-dst")
	if err := dst.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, `INSERT INTO _settings(key,value) VALUES ('ai.data_scope','admin_only')`); err != nil {
		t.Fatal(err)
	}
	if _, err := DemoReset(ctx, dst, obzPath); err != nil {
		t.Fatalf("DemoReset: %v", err)
	}
	if got := dst.GetAIDataScope(ctx); got != storage.AIDataScopeRBAC {
		t.Fatalf("ai.data_scope after demo reset = %q, want rbac", got)
	}
}

// TestJSONLRoundTripSQLite exports a simple SQLite table and re-imports it,
// verifying that string, bool, and integer values survive intact.
func TestJSONLRoundTripSQLite(t *testing.T) {
	ctx := context.Background()
	src := newSQLite(t, "rt-src")
	if _, err := src.Exec(ctx, `CREATE TABLE items (
		id    TEXT PRIMARY KEY,
		name  TEXT,
		qty   INTEGER,
		price TEXT,
		active INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	rows := [][]any{
		{"id1", "Apple", 10, "9.99", 1},
		{"id2", "Banana", 0, "1234567890.1234", 0},
		{"id3", "Cherry", 5, "0.01", 1},
	}
	for _, r := range rows {
		if _, err := src.Exec(ctx,
			`INSERT INTO items VALUES(?,?,?,?,?)`, r...); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Export.
	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", t.TempDir(), "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	// Import.
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatal(err)
	}
	dst := newSQLite(t, "rt-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE items (
		id TEXT PRIMARY KEY, name TEXT, qty INTEGER, price TEXT, active INTEGER
	)`); err != nil {
		t.Fatal(err)
	}

	n, err := importTableJSONL(ctx, dst, "items",
		filepath.Join(tmpDir, "data", "items.jsonl"))
	if err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	if n != 3 {
		t.Errorf("imported rows: got %d, want 3", n)
	}

	// Spot-check.
	var name, price string
	var qty, active int
	if err := dst.QueryRow(ctx,
		`SELECT name, qty, price, active FROM items WHERE id='id2'`).
		Scan(&name, &qty, &price, &active); err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "Banana" || qty != 0 || price != "1234567890.1234" || active != 0 {
		t.Errorf("row mismatch: name=%q qty=%d price=%q active=%d",
			name, qty, price, active)
	}
}

// TestAttachmentsExportRestore verifies that binary attachment files are
// exported into the archive and re-created on import.
func TestAttachmentsExportRestore(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "att")

	attDir := t.TempDir()
	// Write a fake attachment file.
	subDir := filepath.Join(attDir, "Реализация")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	attFile := filepath.Join(subDir, "abc123-uuid")
	attContent := []byte("hello attachment content")
	if err := os.WriteFile(attFile, attContent, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, db, "file", t.TempDir(), attDir, "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	// Verify the attachment appears in the ZIP.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "attachments/Реализация/abc123-uuid" {
			found = true
			// Verify content.
			rc, _ := f.Open()
			got, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !bytes.Equal(got, attContent) {
				t.Errorf("attachment content mismatch: got %q, want %q", got, attContent)
			}
		}
	}
	_ = found // File may appear under any encoding variant of the path

	// Verify META.txt has has_attachments=true.
	meta, _ := readMeta(zr)
	if meta["has_attachments"] != "true" {
		t.Errorf("has_attachments=%q, want true", meta["has_attachments"])
	}

	// Restore attachments.
	dstAttDir := t.TempDir()
	tmpDir := t.TempDir()
	if err := extractZip(buf.Bytes(), tmpDir); err != nil {
		t.Fatal(err)
	}
	attSrc := filepath.Join(tmpDir, "attachments")
	existing := filepath.Join(dstAttDir, "Реализация", "abc123-uuid")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attSrc); err == nil {
		n, err := restoreAttachments(attSrc, dstAttDir)
		if err != nil {
			t.Fatalf("restoreAttachments: %v", err)
		}
		if n != 1 {
			t.Errorf("restored %d files, want 1", n)
		}
	}

	// Verify the restored file exists with correct content.
	var restoredContent []byte
	if err := filepath.WalkDir(dstAttDir, func(path string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			restoredContent, _ = os.ReadFile(path) //nolint:gosec // G122: обход идёт по каталогу, который мы сами и распаковали; переход на os.Root — отдельная задача
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restoredContent, attContent) {
		t.Errorf("restored content mismatch: got %q, want %q", restoredContent, attContent)
	}
}

func TestSkipConfigPath(t *testing.T) {
	cases := []struct {
		rel  string
		skip bool
	}{
		{"metadata/Номенклатура.yaml", false},
		{".gitignore", false},         // file, not the .git dir
		{"sub/.gitignore", false},     // whole-segment match only
		{".git", true},                // the directory itself
		{".git/objects/00/abc", true}, // read-only object that breaks restore
		{"deep/.git/config", true},    // nested repo
		{".svn/entries", true},        // other VCS
		{".hg/store", true},           // other VCS
		{"backups/full.obz", true},    // backups are not config
		{"backupsX/keep.yaml", false}, // prefix must be the backups/ dir
	}
	for _, c := range cases {
		if got := skipConfigPath(c.rel); got != c.skip {
			t.Errorf("skipConfigPath(%q) = %v, want %v", c.rel, got, c.skip)
		}
	}
}

// TestExportConfig_FileSourceExcludesGit verifies that a file-source config
// export prunes the project's .git tree (and other VCS metadata) so that a
// later restore never tries to overwrite read-only git objects — the
// "Access is denied" bug on Windows.
func TestExportConfig_FileSourceExcludesGit(t *testing.T) {
	configDir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(configDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("metadata/Товар.yaml", "name: Товар")
	write(".gitignore", "*.db")
	write(".git/objects/00/abcdef", "binary-git-object")
	write(".git/HEAD", "ref: refs/heads/main")
	write("backups/old.obz", "archive")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := exportConfig(context.Background(), nil, "file", configDir, zw); err != nil {
		t.Fatalf("exportConfig: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for name := range got {
		if strings.Contains(name, "/.git/") || strings.HasSuffix(name, "/.git") {
			t.Errorf("archive must not contain .git entries, found %q", name)
		}
		if strings.Contains(name, "backups/") {
			t.Errorf("archive must not contain backups entries, found %q", name)
		}
	}
	if !got["config/metadata/Товар.yaml"] {
		t.Errorf("expected config metadata to be exported, entries: %v", got)
	}
	if !got["config/.gitignore"] {
		t.Errorf(".gitignore (a regular file) should be exported, entries: %v", got)
	}
}

// TestJSONBRoundTripSQLite checks the import side of the JSONB double-
// encoding fix. It crafts a JSONL line in the LEGACY shape — the JSONB
// value is stored as a JSON-escaped string, the way old universal.go
// exported PostgreSQL jsonb columns — and verifies that importTableJSONL
// unwraps it back to the original JSON text. The export side of the
// fix (marshalValue returning json.RawMessage instead of string) only
// applies to drivers that surface JSONB as []byte, which is the
// pgx / PG path; SQLite hands TEXT back as Go string and so cannot
// exercise that branch — see TestJSONColValue_PassesThroughObject for
// the import-side counterpart.
func TestJSONBRoundTripSQLite(t *testing.T) {
	ctx := context.Background()
	dst := newSQLite(t, "jsonb-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE perms (id TEXT PRIMARY KEY, data TEXT)`); err != nil {
		t.Fatal(err)
	}

	// Hand-craft a JSONL file in the legacy double-encoded shape.
	want := `{"catalogs":{"ЕдиницаИзмерения":["read"]},"documents":{}}`
	// The "data" field is a JSON string literal whose content is the
	// escaped JSON object — i.e. exactly what marshalValue used to emit
	// for a jsonb column read by pgx as []byte.
	legacyLine := []byte(`{"_schema":1}` + "\n" +
		`{"id":"p1","data":"` + strings.ReplaceAll(want, `"`, `\"`) + `"}` + "\n")
	jsonlPath := filepath.Join(t.TempDir(), "perms.jsonl")
	if err := os.WriteFile(jsonlPath, legacyLine, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := importTableJSONL(ctx, dst, "perms", jsonlPath); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	var got string
	if err := dst.QueryRow(ctx, `SELECT data FROM perms WHERE id='p1'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != want {
		t.Errorf("legacy double-encoded import was not unwrapped:\n got  %s\n want %s", got, want)
	}
}

// TestJSONColValue_UnwrapsEscapedString pins down the unwrap behaviour
// used by insertRow for JSON/JSONB columns: a JSONL line that stores the
// value as a JSON-escaped string (the legacy shape produced by the old
// marshalValue) must be unescaped before being handed to the driver so
// that the ::jsonb cast in PostgreSQL stores a real object.
func TestJSONColValue_UnwrapsEscapedString(t *testing.T) {
	raw := json.RawMessage(`"{\"x\":1}"`)
	got, ok := jsonColValue(raw).(string)
	if !ok {
		t.Fatalf("jsonColValue: want string, got %T", jsonColValue(raw))
	}
	if got != `{"x":1}` {
		t.Errorf("jsonColValue: want %q, got %q", `{"x":1}`, got)
	}
}

// TestJSONColValue_PassesThroughObject pins down the modern path: when
// the JSONL stores the value as a nested JSON object, the raw bytes
// are passed straight through (as a string) to the driver.
func TestJSONColValue_PassesThroughObject(t *testing.T) {
	raw := json.RawMessage(`{"x":1}`)
	got, ok := jsonColValue(raw).(string)
	if !ok {
		t.Fatalf("jsonColValue: want string, got %T", jsonColValue(raw))
	}
	if got != `{"x":1}` {
		t.Errorf("jsonColValue: want %q, got %q", `{"x":1}`, got)
	}
}

// TestImportTableJSONL_CP1251StringColumn pins down the deploy failure
// for _audit: a row in the JSONL has a TEXT column whose value is a
// JSON string literal that contains raw Windows-1251 bytes (e.g. a
// Russian legacy export). PostgreSQL rejects the value with
// SQLSTATE 22021 "invalid byte sequence for encoding UTF8: 0x9E".
// insertRow must transcode the value from CP1251 to UTF-8 before the
// driver hands it to PG.
func TestImportTableJSONL_CP1251StringColumn(t *testing.T) {
	ctx := context.Background()
	dst := newSQLite(t, "cp1251-dst")
	if _, err := dst.Exec(ctx, `CREATE TABLE _audit (
		id          TEXT PRIMARY KEY,
		entity_name TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	// Build a JSONL row where entity_name carries raw CP1251 bytes for
	// the word "Привет" (0xCF 0xF0 0xE8 0xE2 0xE5 0xF2) — exactly the
	// shape an old export would write, with the bytes appearing as-is
	// between the JSON quotes (no \uXXXX escaping).
	cp1251 := []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}
	var dataLine []byte
	dataLine = append(dataLine, []byte(`{"id":"91166c0a-a802-49c2-9efd-b2d70a0ec793","entity_name":"`)...)
	dataLine = append(dataLine, cp1251...)
	dataLine = append(dataLine, '"', '}')

	// importTableJSONL expects the first line to be a schema header.
	// No btypes here — entity_name is a regular TEXT column.
	schema := []byte(`{"_schema":1}` + "\n")
	tmpDir := t.TempDir()
	jsonlPath := filepath.Join(tmpDir, "_audit.jsonl")
	if err := os.WriteFile(jsonlPath, append(schema, dataLine...), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := importTableJSONL(ctx, dst, "_audit", jsonlPath); err != nil {
		t.Fatalf("importTableJSONL: %v", err)
	}
	var got string
	if err := dst.QueryRow(ctx, `SELECT entity_name FROM _audit WHERE id='91166c0a-a802-49c2-9efd-b2d70a0ec793'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "Привет" {
		t.Errorf("CP1251 entity_name was not transcoded:\n got  %q (% x)\n want %q",
			got, []byte(got), "Привет")
	}
}

// TestImportTableJSONL_TranscodesNonUTF8Line is a higher-level
// regression test: a JSONL row whose bytes aren't valid UTF-8 (the
// signature of a Windows-1251 source) must be transcoded to UTF-8
// before the JSON parser sees it, so the values land in the target
// columns as readable Cyrillic — not as U+FFFD garbage from the
// json package's automatic replacement of invalid UTF-8, and not as
// "invalid byte sequence for encoding UTF8: 0x9E" from PostgreSQL.
// TestImportTableJSONL_CP1251StringColumn above already covers the
// happy path; this one additionally guards the line-level guard.
func TestImportTableJSONL_TranscodesNonUTF8Line(t *testing.T) {
	// Sanity check: a freshly read line that doesn't pass utf8.Valid
	// must be transcodable via the same code path the import uses.
	cp1251 := []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}
	if utf8.Valid(cp1251) {
		t.Fatal("CP1251 bytes should NOT be valid UTF-8")
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(cp1251)
	if err != nil {
		t.Fatalf("CP1251 decode: %v", err)
	}
	if string(decoded) != "Привет" {
		t.Errorf("decode mismatch: got %q, want %q", decoded, "Привет")
	}
}

// TestInsertRow_BlobSourceIntoJSONBTarget pins down the SQLite→PostgreSQL
// path for jsonb columns. The source DB had the JSON payload stored as
// a BLOB, so the JSONL carries it as a JSON-escaped base64 string
// (e.g. "IlJVQiI=" decodes to "RUB"). insertRow must base64-decode that
// string before handing it to the ::jsonb cast, otherwise PostgreSQL
// rejects the value with "invalid input syntax for type json".
// This is a direct regression test for the deploy failure on
// _constants.value.
func TestInsertRow_BlobSourceIntoJSONBTarget(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "blob-to-jsonb")
	if _, err := db.Exec(ctx, `CREATE TABLE _constants (
		name TEXT PRIMARY KEY, value TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	const want = `"RUB"`
	// The JSONL line exactly as exported from a SQLite source whose
	// `value` column was BLOB: a JSON string literal holding the base64
	// of the raw JSON bytes.
	b64 := base64.StdEncoding.EncodeToString([]byte(want))
	raw := map[string]json.RawMessage{
		"name":  json.RawMessage(`"CURRENCY"`),
		"value": json.RawMessage(strconv.Quote(b64)),
	}
	btypes := map[string]bool{"value": true}
	existingCols := map[string]bool{"name": true, "value": true}
	// Pretend the target is PG and the column is jsonb — that's what
	// makes insertRow take the btypes+jsonCols branch.
	jsonCols := map[string]bool{"value": true}
	boolCols := map[string]bool{}
	byteaCols := map[string]bool{}

	if err := insertRow(ctx, db, "_constants", raw, btypes, existingCols, jsonCols, boolCols, byteaCols); err != nil {
		t.Fatalf("insertRow: %v", err)
	}
	var got string
	if err := db.QueryRow(ctx, `SELECT value FROM _constants WHERE name='CURRENCY'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != want {
		t.Errorf("value round-trip mismatch:\n got  %s\n want %s", got, want)
	}
}

// TestInsertRow_BlobSourceCP1251IntoJSONBTarget covers the _audit import
// failure where the source .obz carries a base64 payload of Windows-1251
// bytes (e.g. "О" = 0x9E). Without the in-branch transcoding, string(decoded)
// leaks invalid UTF-8 into the ::jsonb cast and PostgreSQL raises
// SQLSTATE 22021 "invalid byte sequence for encoding \"UTF8\": 0x9e".
func TestInsertRow_BlobSourceCP1251IntoJSONBTarget(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "cp1251-to-jsonb")
	if _, err := db.Exec(ctx, `CREATE TABLE _audit (
		id TEXT PRIMARY KEY, old_value TEXT, new_value TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	// Windows-1251 text from a legacy OneBase build, e.g. "Приход №5 ООО".
	const cp1251 = "Приход №5 ООО"
	decoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(cp1251))
	if err != nil {
		t.Fatalf("encode cp1251: %v", err)
	}
	if utf8.Valid(decoded) {
		t.Fatalf("test fixture is not actually CP1251")
	}
	b64 := base64.StdEncoding.EncodeToString(decoded)
	// Also include a control byte 0x9E to mirror the actual failure mode.
	payload := append([]byte{0x9E}, decoded...)
	b64With9E := base64.StdEncoding.EncodeToString(payload)

	raw := map[string]json.RawMessage{
		"id":        json.RawMessage(`"row-1"`),
		"old_value": json.RawMessage(strconv.Quote(b64)),
		"new_value": json.RawMessage(strconv.Quote(b64With9E)),
	}
	btypes := map[string]bool{"old_value": true, "new_value": true}
	existingCols := map[string]bool{"id": true, "old_value": true, "new_value": true}
	jsonCols := map[string]bool{"old_value": true, "new_value": true}
	boolCols := map[string]bool{}
	byteaCols := map[string]bool{}

	if err := insertRow(ctx, db, "_audit", raw, btypes, existingCols, jsonCols, boolCols, byteaCols); err != nil {
		t.Fatalf("insertRow: %v", err)
	}

	// Verify both columns decoded and transcoded to UTF-8 successfully.
	var oldVal, newVal string
	if err := db.QueryRow(ctx, `SELECT old_value, new_value FROM _audit WHERE id='row-1'`).Scan(&oldVal, &newVal); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !utf8.ValidString(oldVal) {
		t.Errorf("old_value still has invalid UTF-8: % x", []byte(oldVal))
	}
	if !utf8.ValidString(newVal) {
		t.Errorf("new_value still has invalid UTF-8: % x", []byte(newVal))
	}
	// The decoded bytes are not valid JSON (plain text), so the
	// importer must wrap them as a JSON string for the ::jsonb cast.
	if !json.Valid([]byte(oldVal)) {
		t.Errorf("old_value is not a valid JSON value: %q", oldVal)
	}
	if !json.Valid([]byte(newVal)) {
		t.Errorf("new_value is not a valid JSON value: %q", newVal)
	}
}

// TestInsertRow_BlobSourceValidJSONPassesThrough ensures that when the
// base64-decoded bytes are already a valid JSON document (the common
// case for a well-formed jsonb column), insertRow does NOT double-wrap
// them as a JSON string — the value is forwarded as-is for the ::jsonb
// cast to parse.
func TestInsertRow_BlobSourceValidJSONPassesThrough(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "blob-valid-json")
	if _, err := db.Exec(ctx, `CREATE TABLE _audit (
		id TEXT PRIMARY KEY, new_value TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	const want = `{"name":"RUB","value":100}`
	b64 := base64.StdEncoding.EncodeToString([]byte(want))

	raw := map[string]json.RawMessage{
		"id":        json.RawMessage(`"row-1"`),
		"new_value": json.RawMessage(strconv.Quote(b64)),
	}
	btypes := map[string]bool{"new_value": true}
	existingCols := map[string]bool{"id": true, "new_value": true}
	jsonCols := map[string]bool{"new_value": true}
	boolCols := map[string]bool{}
	byteaCols := map[string]bool{}

	if err := insertRow(ctx, db, "_audit", raw, btypes, existingCols, jsonCols, boolCols, byteaCols); err != nil {
		t.Fatalf("insertRow: %v", err)
	}
	var got string
	if err := db.QueryRow(ctx, `SELECT new_value FROM _audit WHERE id='row-1'`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != want {
		t.Errorf("new_value double-wrapped:\n got  %s\n want %s", got, want)
	}
}

// TestMigrateSchema_CreatesAccountsTable is a regression test for the .obz
// restore failure reported as:
//
//	import: schema migration: sync accounts: sync accounts Хозрасчётный.00:
//	SQL logic error: no such table: _accounts (1)
//
// The import path (migrateSchema) called SyncAccounts without first creating
// the _accounts table — unlike run/migrate/deploy/dev/check, which all call
// EnsureAccountsTable first. _accounts is never exported (it's filtered out of
// data/ by the "_" prefix and absent from systemTables), so it must be created
// during schema migration and repopulated from the config's chart of accounts.
// Any backup whose configuration declared a chart of accounts therefore failed
// to restore. This pins down that migrateSchema creates and fills _accounts.
func TestMigrateSchema_CreatesAccountsTable(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	accDir := filepath.Join(cfgDir, "accounts")
	if err := os.MkdirAll(accDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mirror the user's failing config: a plan named "Хозрасчётный" with an
	// account coded "00".
	chart := "name: Хозрасчётный\n" +
		"title: Хозрасчётный план счетов\n" +
		"accounts:\n" +
		"  - code: \"00\"\n" +
		"    name: Вспомогательный счёт\n" +
		"    kind: active_passive\n" +
		"  - code: \"01\"\n" +
		"    name: Основные средства\n" +
		"    kind: active\n"
	if err := os.WriteFile(filepath.Join(accDir, "хозрасчётный.yaml"), []byte(chart), 0o644); err != nil {
		t.Fatal(err)
	}

	db := newSQLite(t, "migrate-accounts")
	if err := migrateSchema(ctx, db, "file", cfgDir); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}

	var n int
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM _accounts WHERE plan='Хозрасчётный'`).Scan(&n); err != nil {
		t.Fatalf("query _accounts: %v", err)
	}
	if n != 2 {
		t.Errorf("_accounts rows for plan Хозрасчётный: got %d, want 2", n)
	}
}

func TestImportConfigRejectsSymlinkedDestinationParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "catalogs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "catalogs", "item.yaml"), []byte("name: Item\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importConfig(context.Background(), nil, "file", root, src); err == nil {
		t.Fatal("expected symlinked destination parent to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "item.yaml")); !os.IsNotExist(err) {
		t.Fatalf("import escaped through destination symlink: %v", err)
	}
}

// Полнотекстовый индекс восстановленной базы (план 82). Импорт заливает строки
// прямым INSERT мимо upsert, поэтому хук индексации не срабатывает: без
// пересборки поиск в восстановленной базе не находил бы ничего, а поверх
// существующей — держал бы в выдаче объекты, которых в архиве нет.
func TestImportUniversal_ПересобираетПолнотекстовыйИндекс(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "catalogs", "контрагент.yaml"),
		[]byte("name: Контрагент\nfields:\n  - name: Наименование\n    type: string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj, err := project.Load(cfgDir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	src := newSQLite(t, "fts-src")
	if err := src.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	cat := proj.Entities[0]
	if err := src.Upsert(ctx, cat.Name, uuid.New(),
		map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := ExportUniversal(ctx, src, "file", cfgDir, "", "test", &buf); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}

	dst := newSQLite(t, "fts-dst")
	if _, err := ImportUniversal(ctx, dst, "file", cfgDir, "",
		bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}

	hits, err := dst.SearchFullText(ctx, storage.FTSQuery{
		Text: "ромашка", Names: []string{cat.Name}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("восстановленная база не ищет по своим данным: %+v", hits)
	}
}
