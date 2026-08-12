package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
)

func createDiskAttachmentForBackup(t *testing.T, db *storage.DB, filesDir string, payload []byte) (string, int64) {
	t.Helper()
	ctx := context.Background()
	db.SetFilesDir(filesDir)
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	attachment, err := db.UploadAttachment(ctx, "document", "Orders", uuid.New(),
		"invoice.bin", "application/octet-stream", "tester", bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filesDir, "Orders", attachment.ID.String()), int64(len(payload))
}

func TestValidateUniversalExportExternalObjectsChecksExactFilesAndSizes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, filesDir, objectPath string)
		want   string
	}{
		{name: "valid"},
		{name: "missing", mutate: func(t *testing.T, _, objectPath string) {
			t.Helper()
			if err := os.Remove(objectPath); err != nil {
				t.Fatal(err)
			}
		}, want: "missing"},
		{name: "wrong size", mutate: func(t *testing.T, _, objectPath string) {
			t.Helper()
			if err := os.WriteFile(objectPath, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "metadata requires"},
		{name: "orphan", mutate: func(t *testing.T, filesDir, _ string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(filesDir, "orphan"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "unreferenced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := newSQLite(t, "external-export-"+strings.ReplaceAll(tc.name, " ", "-"))
			filesDir := t.TempDir()
			objectPath, _ := createDiskAttachmentForBackup(t, db, filesDir, []byte("complete payload"))
			if tc.mutate != nil {
				tc.mutate(t, filesDir, objectPath)
			}
			_, err := validateUniversalExportExternalObjects(ctx, db, filesDir)
			if tc.want == "" && err != nil {
				t.Fatalf("valid external objects rejected: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want)) {
				t.Fatalf("validation error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateUniversalExportExternalObjectsHandlesBlobLocations(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "external-blob-locations")
	filesDir := t.TempDir()
	db.SetFilesDir(filesDir)
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatal(err)
	}
	inline, err := db.PutBlob(ctx, "application/octet-stream", strings.NewReader("inline"), 1<<20, storage.BlobOwner{})
	if err != nil {
		t.Fatal(err)
	}
	emptyInline, err := db.PutBlob(ctx, "application/octet-stream", strings.NewReader(""), 1<<20, storage.BlobOwner{})
	if err != nil {
		t.Fatal(err)
	}
	// Legacy rows with non-NULL data are inline even without loc, including a
	// valid zero-byte BLOB. SQL NULL alone denotes a disk-backed legacy row.
	if _, err := db.Exec(ctx, `UPDATE _blobs SET loc='' WHERE id IN (?,?)`, inline.ID.String(), emptyInline.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err != nil {
		t.Fatal(err)
	}
	disk, err := db.PutBlob(ctx, "application/octet-stream", strings.NewReader("on disk"), 1<<20, storage.BlobOwner{})
	if err != nil {
		t.Fatal(err)
	}

	expected, err := validateUniversalExportExternalObjects(ctx, db, filesDir)
	if err != nil {
		t.Fatalf("mixed valid blob locations rejected: %v", err)
	}
	if len(expected) != 1 || expected[strings.ToLower("_blobs/"+disk.ID.String())].size != int64(len("on disk")) {
		t.Fatalf("disk allowlist = %#v, want exactly blob %s", expected, disk.ID)
	}
}

func writeExternalObjectJSONL(t *testing.T, root, table, body string) {
	t.Helper()
	filePath := filepath.Join(root, "system", table+".jsonl")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeArchivedExternalFile(t *testing.T, root, rel string, payload []byte) {
	t.Helper()
	filePath := filepath.Join(root, "attachments", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func attachmentJSONL(id, owner string, size int, loc string) string {
	return `{"_schema":1}` + "\n" +
		`{"id":"` + id + `","owner_name":"` + owner + `","size_bytes":` +
		strconv.Itoa(size) + `,"loc":"` + loc + `"}` + "\n"
}

func TestValidateUniversalArchiveExternalObjectsChecksSemanticAllowlist(t *testing.T) {
	id := uuid.New().String()
	for _, tc := range []struct {
		name      string
		owner     string
		metadata  int
		file      []byte
		orphan    bool
		filesDest string
		want      string
	}{
		{name: "valid", owner: "Orders", metadata: 3, file: []byte("abc"), filesDest: "destination"},
		{name: "missing", owner: "Orders", metadata: 3, filesDest: "destination", want: "missing"},
		{name: "wrong size", owner: "Orders", metadata: 4, file: []byte("abc"), filesDest: "destination", want: "metadata requires"},
		{name: "orphan", owner: "Orders", metadata: 3, file: []byte("abc"), orphan: true, filesDest: "destination", want: "unreferenced"},
		{name: "no destination", owner: "Orders", metadata: 3, file: []byte("abc"), want: "no files destination"},
		{name: "unsafe owner", owner: "../outside", metadata: 3, file: []byte("abc"), filesDest: "destination", want: "unsafe attachment owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeExternalObjectJSONL(t, root, "_attachments", attachmentJSONL(id, tc.owner, tc.metadata, "disk"))
			if tc.file != nil && tc.owner == "Orders" {
				writeArchivedExternalFile(t, root, "Orders/"+id, tc.file)
			}
			if tc.orphan {
				writeArchivedExternalFile(t, root, "orphan", []byte("x"))
			}
			_, err := validateUniversalArchiveExternalObjects(root, tc.filesDest)
			if tc.want == "" && err != nil {
				t.Fatalf("valid archive rejected: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want)) {
				t.Fatalf("validation error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateUniversalArchiveExternalObjectsHandlesInlineAndLegacyBlobs(t *testing.T) {
	for _, tc := range []struct {
		name string
		loc  string
		data string
		size int
		want string
	}{
		{name: "db", loc: "db", data: "YWJj", size: 3},
		{name: "empty db", loc: "db", data: "", size: 0},
		{name: "legacy inline", loc: "", data: "YWJj", size: 3},
		{name: "legacy empty inline", loc: "", data: "", size: 0},
		{name: "wrong inline size", loc: "db", data: "YWJj", size: 4, want: "metadata requires"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			id := uuid.New().String()
			body := `{"_schema":1,"btypes":["data"]}` + "\n" +
				`{"id":"` + id + `","size":` + strconv.Itoa(tc.size) + `,"loc":"` + tc.loc + `","data":"` + tc.data + `"}` + "\n"
			writeExternalObjectJSONL(t, root, "_blobs", body)
			_, err := validateUniversalArchiveExternalObjects(root, "")
			if tc.want == "" && err != nil {
				t.Fatalf("valid inline blob rejected: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want)) {
				t.Fatalf("validation error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func replaceZipEntryForExternalObjectTest(t *testing.T, archive []byte, name string, replacement []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	found := false
	for _, entry := range zr.File {
		header := entry.FileHeader
		writer, err := zw.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == name {
			found = true
			if _, err := writer.Write(replacement); err != nil {
				t.Fatal(err)
			}
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(writer, reader) //nolint:gosec // test rewrites an archive produced in-process above
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy %s: %v / %v", entry.Name, copyErr, closeErr)
		}
	}
	if !found {
		t.Fatalf("archive entry %q not found", name)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestImportUniversalRejectsExternalObjectMismatchBeforeLiveMutation(t *testing.T) {
	ctx := context.Background()
	source := newSQLite(t, "external-preflight-source")
	filesDir := t.TempDir()
	objectPath, _ := createDiskAttachmentForBackup(t, source, filesDir, []byte("complete"))
	rel, err := filepath.Rel(filesDir, objectPath)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", testConfigDir(t), filesDir, "source", &archive); err != nil {
		t.Fatal(err)
	}
	corrupt := replaceZipEntryForExternalObjectTest(t, archive.Bytes(), "attachments/"+filepath.ToSlash(rel), []byte("x"))

	target := newSQLite(t, "external-preflight-target")
	if _, err := target.Exec(ctx, `CREATE TABLE must_survive (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(ctx, `INSERT INTO must_survive(value) VALUES ('original')`); err != nil {
		t.Fatal(err)
	}
	targetFiles := t.TempDir()
	sentinel := filepath.Join(targetFiles, "sentinel")
	if err := os.WriteFile(sentinel, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ImportUniversal(ctx, target, "database", "", targetFiles,
		bytes.NewReader(corrupt), int64(len(corrupt)))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "metadata requires") {
		t.Fatalf("ImportUniversal() = %v, want semantic external-object rejection", err)
	}
	var value string
	if err := target.QueryRow(ctx, `SELECT value FROM must_survive`).Scan(&value); err != nil || value != "original" {
		t.Fatalf("database mutated before external preflight: value=%q err=%v", value, err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "original" {
		t.Fatalf("files mutated before external preflight: content=%q err=%v", got, err)
	}
}

func TestImportCatalogChecksFailClosed(t *testing.T) {
	db := newSQLite(t, "import-catalog-fail-closed")
	filePath := filepath.Join(t.TempDir(), "table.jsonl")
	if err := os.WriteFile(filePath, []byte(`{"_schema":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := importTableJSONL(ctx, db, "missing", filePath); err == nil {
		t.Fatal("importTableJSONL suppressed a table-catalog query failure")
	}
	if err := resetExchangeSecretsAndCloneState(ctx, db, ExchangeRestoreClone); err == nil {
		t.Fatal("resetExchangeSecretsAndCloneState suppressed a table-catalog query failure")
	}
}

func TestUniversalJSONLRoundTripsBlobLargerThanOldScannerLimit(t *testing.T) {
	ctx := context.Background()
	source := newSQLite(t, "large-inline-blob-source")
	if err := source.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), (5<<20)/16)
	blob, err := source.PutBlob(ctx, "application/octet-stream", bytes.NewReader(payload), int64(len(payload))+1, storage.BlobOwner{})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", testConfigDir(t), "", "large-inline", &archive); err != nil {
		t.Fatal(err)
	}

	target := newSQLite(t, "large-inline-blob-target")
	targetConfig := filepath.Join(t.TempDir(), "project")
	if _, err := ImportUniversal(ctx, target, "file", targetConfig, "", bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
		t.Fatal(err)
	}
	var size, dataSize int64
	var loc string
	if err := target.QueryRow(ctx, `SELECT size,LENGTH(data),loc FROM _blobs WHERE id=?`, blob.ID.String()).Scan(&size, &dataSize, &loc); err != nil {
		t.Fatal(err)
	}
	if size != int64(len(payload)) || dataSize != int64(len(payload)) || loc != storage.FileStorageDB {
		t.Fatalf("restored blob size/data/loc = %d/%d/%q", size, dataSize, loc)
	}
}
