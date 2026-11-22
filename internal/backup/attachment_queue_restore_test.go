package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUniversalRestoreClearsAttachmentDeletionJobs(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cfg, files := testConfigDir(t), t.TempDir()
		payload := []byte("bytes from the backup")
		path, _ := createDiskAttachmentForBackup(t, db, files, payload)
		id := uuid.MustParse(filepath.Base(path))

		var archive bytes.Buffer
		if err := ExportUniversal(ctx, db, "file", cfg, files, "before deletion", &archive); err != nil {
			t.Fatal(err)
		}
		if err := db.QueueAttachmentDeletion(ctx, id, "scans"); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportUniversal(ctx, db, "file", cfg, files, bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
			t.Fatal(err)
		}
		result, err := db.FlushAttachmentDeletionQueue(ctx, "Orders", "scans")
		if err != nil || result.Deleted != 0 || result.Failed != 0 {
			t.Fatalf("restore left a deletion job: %+v, %v", result, err)
		}
		reader, _, err := db.OpenAttachment(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := io.ReadAll(reader)
		if closeErr := reader.Close(); closeErr != nil {
			t.Errorf("close restored attachment: %v", closeErr)
		}
		if err != nil || !bytes.Equal(restored, payload) {
			t.Fatalf("restored bytes = %q, %v", restored, err)
		}
	})
}

func TestUniversalExportOmitsPendingAttachmentFiles(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cfg, files := testConfigDir(t), t.TempDir()
		pendingPath, _ := createDiskAttachmentForBackup(t, db, files, []byte("already deleted logically"))
		livePath, _ := createDiskAttachmentForBackup(t, db, files, []byte("still attached"))
		pending := uuid.MustParse(filepath.Base(pendingPath))
		if err := db.QueueAttachmentDeletion(ctx, pending, "scans"); err != nil {
			t.Fatal(err)
		}

		for _, phase := range []string{"before cleanup", "after failed cleanup"} {
			if phase == "after failed cleanup" {
				db.SetFilesDir("")
				result, err := db.FlushAttachmentDeletionQueue(ctx, "Orders", "scans")
				db.SetFilesDir(files)
				if err != nil || result.Failed != 1 || result.Deleted != 0 {
					t.Fatalf("expected one failed cleanup, got %+v, %v", result, err)
				}
			}

			var archive bytes.Buffer
			if err := ExportUniversal(ctx, db, "file", cfg, files, phase, &archive); err != nil {
				t.Fatalf("%s: %v", phase, err)
			}
			zipped, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
			if err != nil {
				t.Fatal(err)
			}
			liveFound := false
			for _, entry := range zipped.File {
				switch entry.Name {
				case "attachments/Orders/" + filepath.Base(livePath):
					liveFound = true
				case "attachments/Orders/" + filepath.Base(pendingPath), "system/_attachment_deletion_queue.jsonl":
					t.Fatalf("%s: archive contains %s", phase, entry.Name)
				}
			}
			if !liveFound {
				t.Fatalf("%s: live attachment is missing from archive", phase)
			}
		}

		const orphan = "untracked.bin"
		if err := os.WriteFile(filepath.Join(files, "Orders", orphan), []byte("unknown"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := ExportUniversal(ctx, db, "file", cfg, files, "orphan", io.Discard)
		if err == nil || !strings.Contains(err.Error(), "unreferenced external-object file") ||
			!strings.Contains(err.Error(), orphan) {
			t.Fatalf("untracked file error = %v", err)
		}
	})
}
