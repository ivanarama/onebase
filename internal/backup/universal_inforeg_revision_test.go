package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUniversalInfoRegLocalRevisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, source *storage.DB) {
		ctx := context.Background()
		source.SetFilesDir(t.TempDir())
		cfg := testConfigDir(t)
		if err := os.Mkdir(filepath.Join(cfg, "inforegs"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "inforegs", "revisionbackup.yaml"), []byte(`name: RevisionBackup
dimensions:
  - {name: Key, type: string}
resources:
  - {name: Value, type: string}
`), 0600); err != nil {
			t.Fatal(err)
		}
		proj, err := project.Load(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		if len(proj.InfoRegisters) != 1 {
			t.Fatal("fixture metadata missing")
		}
		ir := proj.InfoRegisters[0]
		if err := source.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		originals := map[string]string{}
		for _, k := range []string{"", "other"} {
			key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": k}}
			if err := source.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "данные"}, nil); err != nil {
				t.Fatal(err)
			}
			row, err := source.InfoRegReadForEdit(ctx, ir, key, nil)
			if err != nil || row == nil {
				t.Fatalf("read %v %v", row, err)
			}
			originals[k] = row.Revision
		}
		var archive bytes.Buffer
		if err := ExportUniversal(ctx, source, "file", cfg, "", "revision-test", &archive); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
		if err != nil {
			t.Fatal(err)
		}
		var injected bytes.Buffer
		zw := zip.NewWriter(&injected)
		found := false
		for _, f := range zr.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(io.LimitReader(r, 1<<20))
			closeErr := r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if len(data) == 1<<20 {
				t.Fatal("fixture entry too large")
			}
			if strings.Contains(f.Name, "инфо_revisionbackup") && strings.HasSuffix(f.Name, ".jsonl") {
				found = true
				if bytes.Contains(data, []byte(storage.InfoRegEditRevisionColumn)) {
					t.Fatal("local revision exported")
				}
				lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
				for i := 1; i < len(lines); i++ {
					var row map[string]any
					if err := json.Unmarshal(lines[i], &row); err != nil {
						t.Fatal(err)
					}
					row[storage.InfoRegEditRevisionColumn] = "untrusted-archive-version"
					lines[i], err = json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
				}
				data = append(bytes.Join(lines, []byte("\n")), '\n')
			}
			w, err := zw.Create(f.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("register absent in backup")
		}
		dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
			target.SetFilesDir(t.TempDir())
			for _, a := range []*bytes.Buffer{&archive, &injected} {
				if _, err := ImportUniversal(ctx, target, "file", filepath.Join(t.TempDir(), "config"), "", bytes.NewReader(a.Bytes()), int64(a.Len())); err != nil {
					t.Fatal(err)
				}
				seen := map[string]bool{}
				for k, original := range originals {
					row, err := target.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": k}}, nil)
					if err != nil || row == nil {
						t.Fatalf("restored read %v %v", row, err)
					}
					if row.Revision == original || seen[row.Revision] || row.Row["Value"] != "данные" {
						t.Fatalf("restored row %v", row)
					}
					seen[row.Revision] = true
					originals[k] = row.Revision
				}
			}
		})
	})
}
