package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

func navigationRestoreTarget(t *testing.T, source *storage.DB) *storage.DB {
	t.Helper()
	if source.IsSQLite() {
		return newSQLite(t, "navigation-target")
	}
	ctx := context.Background()
	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, os.Getenv("TEST_DATABASE_URL"), schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Error(err)
		}
		db.Close()
	})
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	db.SetFilesDir(t.TempDir())
	return db
}

func TestUniversalNavigationSettingsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, source *storage.DB) {
		ctx := context.Background()
		source.SetFilesDir(t.TempDir())
		keys := []string{"ui.navigation.admin.9:Education", "ui.navigation.user.8:Иван.9:Education", "ui.navigation.admin.7:corrupt"}
		// Preserve literal formatting and Unicode, rather than decode/re-encode
		// portable values. A corrupt JSON layer is portable too and will fall back
		// independently when the navigation resolver reads it after restoration.
		hash := "sha256:" + strings.Repeat("0", 64)
		values := []string{
			" {\n \"version\":1, \"base_hash\":\"" + hash + "\", \"ops\":[{\"op\":\"rename\",\"node\":\"cfg:main\",\"title\":\"Школа\"}] } \n",
			`{"version":1,"base_hash":"` + hash + `","ops":[{"op":"rename","node":"cfg:main","title":"Личная настройка"}]}`,
			"{ broken stored JSON: повреждённый слой }",
		}
		for _, include := range []bool{true, false} {
			t.Run(map[bool]string{true: "with-layers", false: "without-layers"}[include], func(t *testing.T) {
				for i, key := range keys {
					if include {
						if err := source.SaveSetting(ctx, key, values[i]); err != nil {
							t.Fatal(err)
						}
					} else if _, err := source.Exec(ctx, "DELETE FROM _settings WHERE key = "+source.Dialect().Placeholder(1), key); err != nil {
						t.Fatal(err)
					}
				}
				for _, key := range []string{"llm.config", "ui.navigation.admin.not-a-length-key"} {
					if err := source.SaveSetting(ctx, key, "source-private-secret"); err != nil {
						t.Fatal(err)
					}
				}
				var archive bytes.Buffer
				if err := ExportUniversal(ctx, source, "file", testConfigDir(t), "", "test", &archive); err != nil {
					t.Fatal("export", err)
				}
				zipReader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range zipReader.File {
					if file.Name != "settings/safe.jsonl" {
						continue
					}
					r, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(r)
					closeErr := r.Close()
					if err != nil {
						t.Fatal(err)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
					if strings.Contains(string(raw), "source-private-secret") || strings.Contains(string(raw), "llm.config") || strings.Contains(string(raw), "not-a-length-key") {
						t.Fatal("non-portable setting leaked into archive")
					}
				}
				for _, mode := range []ExchangeRestoreMode{ExchangeRestoreClone, ExchangeRestoreDisasterRecovery} {
					t.Run(string(mode), func(t *testing.T) {
						target := navigationRestoreTarget(t, source)
						oldKeys := []string{"ui.navigation.admin.5:other", "ui.navigation.user.3:bob.5:other"}
						for _, key := range append(append([]string{}, keys...), oldKeys...) {
							if err := target.SaveSetting(ctx, key, "previous-target-layout"); err != nil {
								t.Fatal(err)
							}
						}
						if err := target.SaveSetting(ctx, "llm.config", "target-private-secret"); err != nil {
							t.Fatal(err)
						}
						if err := target.SaveSetting(ctx, "UI.NAVIGATION.ADMIN.5:other", "unrelated-case-sensitive-key"); err != nil {
							t.Fatal(err)
						}
						if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()), ImportOptions{ExchangeMode: mode}); err != nil {
							t.Fatal("restore", err)
						}
						for i, key := range keys {
							got, exists, err := target.GetSetting(ctx, key)
							if err != nil || exists != include || include && got != values[i] {
								t.Fatalf("layer %q not preserved byte-for-byte: exists=%v got=%q err=%v", key, exists, got, err)
							}
						}
						for _, key := range oldKeys {
							if _, exists, err := target.GetSetting(ctx, key); err != nil || exists {
								t.Fatalf("old target override retained: %q %v", key, err)
							}
						}
						if got, exists, err := target.GetSetting(ctx, "llm.config"); err != nil || !exists || got != "target-private-secret" {
							t.Fatal("non-portable setting overwritten", err)
						}
						if got, exists, err := target.GetSetting(ctx, "UI.NAVIGATION.ADMIN.5:other"); err != nil || !exists || got != "unrelated-case-sensitive-key" {
							t.Fatal("non-portable case variant was removed", err)
						}
					})
				}
			})
		}
	})
}
