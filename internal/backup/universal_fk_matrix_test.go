package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// Loading A before Z exercises references to a later archive entry. A cycle
// also makes migration recreate fk_ob_* on an already migrated PostgreSQL target.
func TestImportUniversalForeignKeysMatrix(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(fmt.Sprintf("cycle=%t", cycle), func(t *testing.T) {
			ctx := context.Background()
			config := testConfigDir(t)
			if err := os.MkdirAll(filepath.Join(config, "catalogs"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(config, "catalogs", "A.yaml"), "name: A\nfields:\n  - name: Label\n    type: string\n  - name: Peer\n    type: reference:Z\n")
			zConfig := "name: Z\nfields:\n  - name: Label\n    type: string\n"
			if cycle {
				zConfig += "  - name: Peer\n    type: reference:A\n"
			}
			writeTestFile(t, filepath.Join(config, "catalogs", "Z.yaml"), zConfig)
			proj, err := project.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			defer proj.Close()
			src := newSQLite(t, "fk-source")
			if err := src.EnsureServiceSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := src.Migrate(ctx, proj.Entities); err != nil {
				t.Fatal(err)
			}
			aID, zID := uuid.New(), uuid.New()
			if _, err := src.Exec(ctx, `INSERT INTO z(id,label) VALUES (?,?)`, zID.String(), "source Z"); err != nil {
				t.Fatal(err)
			}
			if _, err := src.Exec(ctx, `INSERT INTO a(id,label,peer_id) VALUES (?,?,?)`, aID.String(), "source A", zID.String()); err != nil {
				t.Fatal(err)
			}
			if cycle {
				if _, err := src.Exec(ctx, `UPDATE z SET peer_id=?`, aID.String()); err != nil {
					t.Fatal(err)
				}
			}
			if err := src.SaveExchangeThisNode(ctx, "FKTest", "source-node"); err != nil {
				t.Fatal(err)
			}
			if _, err := src.Exec(ctx, `INSERT INTO _exchange_peers(plan,node_code,sent_no,ack_no,recv_no) VALUES ('FKTest','peer',3,2,1)`); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := ExportUniversal(ctx, src, "file", config, "", "fk-test", &archive); err != nil {
				t.Fatal(err)
			}
			invalid := archiveWithMissingFKTarget(t, archive.Bytes(), zID.String())

			for _, existing := range []bool{false, true} {
				for _, mode := range []ExchangeRestoreMode{ExchangeRestoreClone, ExchangeRestoreDisasterRecovery} {
					t.Run(fmt.Sprintf("existing=%t/%s", existing, mode), func(t *testing.T) {
						dbtest.ForEachDialect(t, func(t *testing.T, dst *storage.DB) {
							if existing {
								if err := dst.Migrate(ctx, proj.Entities); err != nil {
									t.Fatal(err)
								}
								if _, err := dst.Exec(ctx, `INSERT INTO z(id,label) VALUES (`+dst.Dialect().Placeholder(1)+`, 'obsolete')`, uuid.New().String()); err != nil {
									t.Fatal(err)
								}
							}
							root := t.TempDir()
							configDest, filesDest := filepath.Join(root, "project"), filepath.Join(root, "files")
							for _, dir := range []string{configDest, filesDest} {
								if err := os.MkdirAll(dir, 0o700); err != nil {
									t.Fatal(err)
								}
							}
							writeTestFile(t, filepath.Join(configDest, "onebase.yaml"), "name: original\n")
							writeTestFile(t, filepath.Join(filesDest, "old.bin"), "old attachment")
							restore := func(data []byte) error {
								_, err := ImportUniversalWithOptions(ctx, dst, "file", configDest, filesDest, bytes.NewReader(data), int64(len(data)), ImportOptions{ExchangeMode: mode})
								return err
							}
							if !existing && !dst.IsSQLite() {
								beforeFK := restoredFKSnapshot(t, dst)
								err := restore(invalid)
								if err == nil || !strings.Contains(err.Error(), "restore and validate FK constraints") {
									t.Fatalf("invalid reference in empty target: %v", err)
								}
								var tables int
								if err := dst.QueryRow(ctx, `SELECT COUNT(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename IN ('a','z')`).Scan(&tables); err != nil || tables != 0 {
									t.Fatalf("failed restore left %d entity tables: %v", tables, err)
								}
								if after := restoredFKSnapshot(t, dst); after != beforeFK {
									t.Fatalf("failed empty restore changed original FK set: %s", after)
								}
								if got := readTestFile(t, filepath.Join(configDest, "onebase.yaml")); got != "name: original\n" {
									t.Fatalf("failed empty restore changed config: %q", got)
								}
								if got := readTestFile(t, filepath.Join(filesDest, "old.bin")); got != "old attachment" {
									t.Fatalf("failed empty restore changed attachments: %q", got)
								}
							}
							for round := 1; round <= 2; round++ {
								if err := restore(archive.Bytes()); err != nil {
									t.Fatalf("restore %d: %v", round, err)
								}
								for _, table := range []string{"a", "z"} {
									assertTableCount(t, ctx, dst, table, 1)
								}
								var peer string
								if err := dst.QueryRow(ctx, `SELECT peer_id FROM a`).Scan(&peer); err != nil || peer != zID.String() {
									t.Fatalf("A reference = %q, err %v", peer, err)
								}
								if cycle {
									if err := dst.QueryRow(ctx, `SELECT peer_id FROM z`).Scan(&peer); err != nil || peer != aID.String() {
										t.Fatalf("Z reference = %q, err %v", peer, err)
									}
								}
								peers := 0
								if mode == ExchangeRestoreDisasterRecovery {
									peers = 1
								}
								assertTableCount(t, ctx, dst, "_exchange_peers", peers)
								assertRestoredFKEnforced(t, dst, "a")
								if cycle {
									assertRestoredFKEnforced(t, dst, "z")
								}
							}
							if _, err := os.Stat(filepath.Join(filesDest, "old.bin")); !os.IsNotExist(err) {
								t.Fatalf("obsolete attachment survived: %v", err)
							}
							// PostgreSQL validates restored constraints before commit. SQLite's
							// existing bulk-import contract only re-enables FK enforcement.
							if dst.IsSQLite() {
								return
							}
							beforeFK := restoredFKSnapshot(t, dst)
							if _, err := dst.Exec(ctx, `UPDATE a SET label='target before failure'`); err != nil {
								t.Fatal(err)
							}
							writeTestFile(t, filepath.Join(configDest, "onebase.yaml"), "name: target before failure\n")
							writeTestFile(t, filepath.Join(filesDest, "keep.bin"), "target attachment")
							err := restore(invalid)
							if err == nil || !strings.Contains(err.Error(), "restore and validate FK constraints") {
								t.Fatalf("invalid reference error = %v", err)
							}
							var label string
							if err := dst.QueryRow(ctx, `SELECT label FROM a`).Scan(&label); err != nil || label != "target before failure" {
								t.Fatalf("rollback label = %q, err %v", label, err)
							}
							for _, table := range []string{"a", "z"} {
								assertTableCount(t, ctx, dst, table, 1)
							}
							if got := readTestFile(t, filepath.Join(configDest, "onebase.yaml")); got != "name: target before failure\n" {
								t.Fatalf("configuration not rolled back: %q", got)
							}
							if got := readTestFile(t, filepath.Join(filesDest, "keep.bin")); got != "target attachment" {
								t.Fatalf("attachments not rolled back: %q", got)
							}
							if after := restoredFKSnapshot(t, dst); after != beforeFK {
								t.Fatalf("FK definitions changed after rollback:\nbefore %s\nafter %s", beforeFK, after)
							}
							assertRestoredFKEnforced(t, dst, "a")
							if cycle {
								assertRestoredFKEnforced(t, dst, "z")
							}
							if err := restore(archive.Bytes()); err != nil {
								t.Fatalf("valid restore after rollback: %v", err)
							}
						})
					})
				}
			}
		})
	}
}

func assertRestoredFKEnforced(t *testing.T, db *storage.DB, table string) {
	t.Helper()
	// A raw SQL write ensures the database constraint itself, rather than the
	// application's reference validator, survived restore and rollback.
	query := `UPDATE ` + table + ` SET peer_id=` + db.Dialect().Placeholder(1)
	if _, err := db.Exec(context.Background(), query, uuid.New().String()); err == nil {
		t.Fatalf("%s accepted a missing FK target", table)
	}
}

func restoredFKSnapshot(t *testing.T, db *storage.DB) string {
	t.Helper()
	var snapshot string
	err := db.QueryRow(context.Background(), `SELECT COALESCE(string_agg(t.relname || ':' || c.conname || ':' || pg_get_constraintdef(c.oid) || ':' || c.convalidated::text, E'\\n' ORDER BY t.relname,c.conname), '') FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE c.contype='f' AND c.connamespace=current_schema()::regnamespace`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == "" {
		t.Fatal("no restored foreign keys")
	}
	return snapshot
}

func archiveWithMissingFKTarget(t *testing.T, data []byte, target string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	replaced := false
	for _, entry := range zr.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if entry.Name == "data/a.jsonl" {
			if !bytes.Contains(body, []byte(target)) {
				t.Fatal("archive has no A reference to replace")
			}
			body = bytes.ReplaceAll(body, []byte(target), []byte(uuid.New().String()))
			replaced = true
		}
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if !replaced {
		t.Fatal("archive omitted A")
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
