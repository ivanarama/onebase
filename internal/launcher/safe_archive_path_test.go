package launcher

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
)

// safeArchivePath защищает распаковку ZIP/OBZ-архивов (config/import-zip,
// backup/full-import) от zip-slip (CWE-22/CWE-23): записи с «../», абсолютные
// пути и обратные слэши не должны выходить за пределы каталога распаковки,
// но валидные подкаталоги (config/module.yaml) обязаны проходить.
func TestSafeArchivePath(t *testing.T) {
	dir := t.TempDir()

	good := []string{
		"somefile.yaml",
		"config/module.yaml",
		"database.db",
		"data/documents/order.json",
	}
	for _, name := range good {
		fp, err := safeArchivePath(dir, name)
		if err != nil {
			t.Errorf("ожидался валидный путь для %q, got err: %v", name, err)
			continue
		}
		want := filepath.Join(dir, filepath.FromSlash(name))
		if fp != want {
			t.Errorf("для %q: got %q, want %q", name, fp, want)
		}
	}

	bad := []string{
		"",
		"..",
		"../secret",
		"../../etc/passwd",
		"config/../../etc/passwd",
		`..\..\windows\system32`, // обратный слэш — traversal при распаковке на Windows
		`C:\Windows\system.ini`,
		"a\x00b",
	}
	for _, name := range bad {
		if _, err := safeArchivePath(dir, name); err == nil {
			t.Errorf("ожидалась ошибка для опасного имени %q, но путь принят", name)
		}
	}
}

func TestUnzipBytesRejectsWholeArchiveBeforeWriting(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	good, err := zw.Create("config/module.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := good.Write([]byte("name: test")); err != nil {
		t.Fatal(err)
	}
	bad, err := zw.Create("../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := unzipBytes(buf.Bytes(), dst); err == nil {
		t.Fatal("ожидался отказ от архива с path traversal")
	}
	if _, err := os.Stat(filepath.Join(dst, "config", "module.yaml")); !os.IsNotExist(err) {
		t.Fatalf("валидная запись не должна извлекаться до проверки всего архива: %v", err)
	}
}

func TestValidateArchiveRejectsSpecialFiles(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0o777)
	f, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateArchiveEntries(t.TempDir(), zr.File, maxFormArchiveExpanded); err == nil {
		t.Fatal("символическая ссылка в архиве должна быть отклонена")
	}
}

func TestFullArchiveLimitsMatchUniversalImporter(t *testing.T) {
	if maxFullArchiveExpanded != backup.MaxUniversalArchiveExpanded {
		t.Fatalf("launcher expanded limit = %d, universal importer limit = %d",
			maxFullArchiveExpanded, backup.MaxUniversalArchiveExpanded)
	}
	if maxFullArchiveUpload != int64(maxFullArchiveExpanded)+maxFullArchiveUploadOverhead {
		t.Fatalf("full upload limit = %d, want expanded limit plus %d bytes overhead",
			maxFullArchiveUpload, maxFullArchiveUploadOverhead)
	}
	if maxFullArchiveUpload <= int64(maxFullArchiveExpanded) {
		t.Fatalf("full upload limit %d leaves no room for ZIP and multipart overhead",
			maxFullArchiveUpload)
	}

	atLimit := &zip.File{FileHeader: zip.FileHeader{
		Name:               "payload.bin",
		UncompressedSize64: maxFullArchiveExpanded,
	}}
	if err := validateArchiveEntries(t.TempDir(), []*zip.File{atLimit}, maxFullArchiveExpanded); err != nil {
		t.Fatalf("archive at universal expanded-size limit was rejected: %v", err)
	}

	overLimit := &zip.File{FileHeader: zip.FileHeader{
		Name:               "payload.bin",
		UncompressedSize64: maxFullArchiveExpanded + 1,
	}}
	if err := validateArchiveEntries(t.TempDir(), []*zip.File{overLimit}, maxFullArchiveExpanded); err == nil {
		t.Fatal("archive above universal expanded-size limit was accepted")
	}
}
