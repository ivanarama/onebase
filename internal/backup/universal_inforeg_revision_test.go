package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestUniversalInfoRegEditPhysicalPeriodMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, source *storage.DB) {
		ctx := context.Background()
		source.SetFilesDir(t.TempDir())
		cfg := testConfigDir(t)
		if err := os.Mkdir(filepath.Join(cfg, "inforegs"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "inforegs", "periodbackup.yaml"), []byte(`name: PeriodBackup
periodic: true
dimensions:
  - {name: Key, type: number, length: 15, scale: 2}
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
		ir := proj.InfoRegisters[0]
		if err := source.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		p := time.Date(2026, 10, 19, 13, 22, 33, 123456000, time.UTC)
		periods := []time.Time{p.Truncate(time.Second)}
		if !source.IsSQLite() {
			periods = append(periods, p, p.Add(time.Microsecond))
		}
		for i, period := range periods {
			if err := source.InfoRegSet(ctx, ir, map[string]any{"Key": "1.00"}, map[string]any{"Value": fmt.Sprint(i)}, &period); err != nil {
				t.Fatal(err)
			}
		}
		var archive bytes.Buffer
		if err := ExportUniversal(ctx, source, "file", cfg, "", "period-test", &archive); err != nil {
			t.Fatal(err)
		}
		dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
			target.SetFilesDir(t.TempDir())
			if _, err := ImportUniversal(ctx, target, "file", filepath.Join(t.TempDir(), "config"), "", bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
				t.Fatal(err)
			}
			listed, err := target.InfoRegListWithKeyValues(ctx, ir, storage.RegFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != len(periods) {
				t.Fatalf("restored rows: %v", listed)
			}
			revisions := map[string]string{}
			for _, row := range listed {
				period, ok := storage.ParseRegPeriod(row["period_key"].(string))
				if !ok {
					t.Fatal(row)
				}
				dims := row[storage.InfoRegKeyValuesField].(map[string]string)
				key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": dims["Key"]}, Period: &period}
				read, err := target.InfoRegReadForEdit(ctx, ir, key, nil)
				if err != nil || read == nil || read.Row["Value"] != row["Value"] || !read.Key.Period.Equal(period) {
					t.Fatalf("exact restored read %v %v, want %v", read, err, row)
				}
				again, err := target.InfoRegReadForEdit(ctx, ir, read.Key, nil)
				if err != nil || again == nil || again.Revision != read.Revision {
					t.Fatalf("physical round trip %v %v", again, err)
				}
				revisions[row["period_key"].(string)] = read.Revision
				hidden, err := target.InfoRegReadForEdit(ctx, ir, key, &storage.Predicate{Field: "Value", Op: "eq", Value: "denied"})
				if err != nil || hidden != nil {
					t.Fatalf("RLS leak: %v %v", hidden, err)
				}
			}
			if target.IsSQLite() {
				// Native writes use another physical format and must not steal restored keys.
				native := p.Truncate(time.Second).Add(time.Second)
				if err := target.InfoRegSet(ctx, ir, map[string]any{"Key": "1.00"}, map[string]any{"Value": "native"}, &native); err != nil {
					t.Fatal(err)
				}
				for _, row := range listed {
					period, _ := storage.ParseRegPeriod(row["period_key"].(string))
					read, err := target.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "1.00"}, Period: &period}, nil)
					if err != nil || read == nil || read.Row["Value"] != row["Value"] || read.Revision != revisions[row["period_key"].(string)] {
						t.Fatalf("sibling selected: %v %v", read, err)
					}
				}
				missing := p.Add(time.Second)
				read, err := target.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "1.00"}, Period: &missing}, nil)
				if err != nil || read != nil {
					t.Fatalf("fractional key selected second sibling: %v %v", read, err)
				}
			}
		})
	})
}
