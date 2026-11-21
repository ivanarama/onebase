package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupVerifySQLiteCLI(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	liveFile := filepath.Join(root, "live.db")
	db, err := storage.ConnectSQLite(ctx, liveFile)
	require.NoError(t, err)
	t.Cleanup(db.Close)

	doc := &metadata.Entity{Name: "Продажа", Kind: metadata.KindDocument, Posting: true}
	empty := &metadata.Entity{Name: "Возврат", Kind: metadata.KindDocument, Posting: true}
	require.NoError(t, db.Migrate(ctx, []*metadata.Entity{doc, empty}))
	for _, posted := range []bool{true, true, false} {
		id := uuid.New()
		require.NoError(t, db.Upsert(ctx, doc.Name, id, nil, doc))
		require.NoError(t, db.SetPosted(ctx, doc.Name, id, posted))
	}
	copyPath, err := backup.DumpSQLite(ctx, liveFile, filepath.Join(root, "копии с пробелом"))
	require.NoError(t, err)
	before, err := os.ReadFile(copyPath)
	require.NoError(t, err)

	// Diverge the live database from the backup. Verification must use only the named copy.
	id := uuid.New()
	require.NoError(t, db.Upsert(ctx, doc.Name, id, nil, doc))
	require.NoError(t, db.SetPosted(ctx, doc.Name, id, true))

	broken := filepath.Join(root, "broken.db")
	require.NoError(t, os.WriteFile(broken, []byte("incomplete backup"), 0o600))
	missing := filepath.Join(root, "missing.db")
	withWAL, err := backup.DumpSQLite(ctx, liveFile, filepath.Join(root, "with-wal"))
	require.NoError(t, err)
	walMarker := []byte("sidecar must remain intact")
	require.NoError(t, os.WriteFile(withWAL+"-wal", walMarker, 0o600))

	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	for _, tc := range []struct {
		name, file, document string
		expected, actual     int64 // -1 means the command could not count the rows.
		ok                   bool
	}{
		{"matched", copyPath, doc.Name, 2, 2, true},
		{"mismatch", copyPath, doc.Name, 3, 2, false},
		{"explicit zero", copyPath, empty.Name, 0, 0, true},
		{"unknown document", copyPath, "НеизвестныйДокумент", 0, -1, false},
		{"missing file", missing, doc.Name, 0, -1, false},
		{"damaged file", broken, doc.Name, 0, -1, false},
		{"sidecar present", withWAL, doc.Name, 3, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, runErr := executeRootArgs(t, "backup", "verify", "--file", tc.file, "--document", tc.document,
				"--expect-posted", strconv.FormatInt(tc.expected, 10), "--json")
			if tc.ok {
				require.NoError(t, runErr, out)
			} else {
				require.Error(t, runErr, out)
			}

			var report struct {
				OK             *bool     `json:"ok"`
				File           string    `json:"file"`
				Document       string    `json:"document"`
				ExpectedPosted *int64    `json:"expected_posted"`
				ActualPosted   *int64    `json:"actual_posted"`
				CheckedAt      time.Time `json:"checked_at"`
				DurationMS     *int64    `json:"duration_ms"`
				Error          string    `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &report), out)
			require.Equal(t, &tc.ok, report.OK)
			require.Equal(t, tc.file, report.File)
			require.Equal(t, tc.document, report.Document)
			require.Equal(t, &tc.expected, report.ExpectedPosted)
			require.False(t, report.CheckedAt.IsZero())
			require.NotNil(t, report.DurationMS)
			require.GreaterOrEqual(t, *report.DurationMS, int64(0))
			if tc.actual >= 0 {
				require.Equal(t, &tc.actual, report.ActualPosted)
			} else {
				require.Nil(t, report.ActualPosted)
			}
			require.Equal(t, tc.ok, report.Error == "")
			files, err := os.ReadDir(scratch)
			require.NoError(t, err)
			require.Empty(t, files, "a temporary copy survived verification")
		})
	}

	t.Run("omitted expected count", func(t *testing.T) {
		_, err := executeRootArgs(t, "backup", "verify", "--file", copyPath, "--document", empty.Name)
		require.Error(t, err, "zero must be supplied explicitly")
	})
	after, err := os.ReadFile(copyPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal(before, after), "backup changed during verification")
	afterWAL, err := os.ReadFile(withWAL + "-wal")
	require.NoError(t, err)
	require.Equal(t, walMarker, afterWAL)
	require.NoFileExists(t, missing)
}
