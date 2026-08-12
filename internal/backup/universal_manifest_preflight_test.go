package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUniversalManifestRejectsInvalidManifest(t *testing.T) {
	tests := []struct {
		name          string
		manifest      string
		writeManifest bool
	}{
		{name: "missing"},
		{name: "empty file", manifest: "", writeManifest: true},
		{name: "malformed", manifest: `{"data/items.jsonl":`, writeManifest: true},
		{name: "null instead of object", manifest: `null`, writeManifest: true},
		{name: "array instead of object", manifest: `[]`, writeManifest: true},
		{name: "trailing JSON value", manifest: "{}\n{}", writeManifest: true},
		{name: "duplicate key", manifest: `{"data/items.jsonl":0,"data/items.jsonl":0}`, writeManifest: true},
		{name: "non-integer count", manifest: `{"data/items.jsonl":1.5}`, writeManifest: true},
		{name: "string count", manifest: `{"data/items.jsonl":"1"}`, writeManifest: true},
		{name: "negative count", manifest: `{"data/items.jsonl":-1}`, writeManifest: true},
		{name: "unknown top-level directory", manifest: `{"other/items.jsonl":0}`, writeManifest: true},
		{name: "config must not be listed", manifest: `{"config/app.yaml":0}`, writeManifest: true},
		{name: "nested data path", manifest: `{"data/nested/items.jsonl":0}`, writeManifest: true},
		{name: "non-JSONL data path", manifest: `{"data/items.json":0}`, writeManifest: true},
		{name: "unknown settings path", manifest: `{"settings/unsafe.jsonl":0}`, writeManifest: true},
		{name: "attachment child instead of aggregate", manifest: `{"attachments/file.bin":1}`, writeManifest: true},
		{name: "backslash path", manifest: `{"data\\items.jsonl":0}`, writeManifest: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if tt.writeManifest {
				writeUniversalManifestFixture(t, tmpDir, "manifest.json", tt.manifest)
			}
			if err := validateUniversalManifest(tmpDir); err == nil {
				t.Fatal("validateUniversalManifest() error = nil, want rejection")
			}
		})
	}
}

func TestValidateUniversalManifestRejectsMissingListedJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"data/items.jsonl":0}`)

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want missing-file rejection")
	}
}

func TestValidateUniversalManifestRejectsUnlistedJSONL(t *testing.T) {
	for _, relativePath := range []string{
		"data/items.jsonl",
		"data/nested/items.jsonl",
		"Data/items.jsonl",
		"settings/unsafe.jsonl",
	} {
		t.Run(relativePath, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{}`)
			writeUniversalManifestFixture(t, tmpDir, relativePath, `{"_schema":1}`+"\n")

			if err := validateUniversalManifest(tmpDir); err == nil {
				t.Fatal("validateUniversalManifest() error = nil, want unlisted/noncanonical-file rejection")
			}
		})
	}
}

func TestValidateUniversalManifestRejectsInvalidJSONL(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty file", body: ""},
		{name: "malformed schema JSON", body: "{\n"},
		{name: "missing schema marker", body: `{"id":1}` + "\n"},
		{name: "unsupported schema version", body: `{"_schema":2}` + "\n"},
		{name: "invalid btypes", body: `{"_schema":1,"btypes":"blob"}` + "\n"},
		{name: "duplicate schema key", body: `{"_schema":1,"_schema":1}` + "\n"},
		{name: "malformed row JSON", body: `{"_schema":1}` + "\n{"},
		{name: "non-object row", body: `{"_schema":1}` + "\n[]\n"},
		{name: "duplicate row key", body: `{"_schema":1}` + "\n" + `{"id":1,"id":2}` + "\n"},
		{name: "empty row object", body: `{"_schema":1}` + "\n{}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"data/items.jsonl":0}`)
			writeUniversalManifestFixture(t, tmpDir, "data/items.jsonl", tt.body)

			if err := validateUniversalManifest(tmpDir); err == nil {
				t.Fatal("validateUniversalManifest() error = nil, want invalid-JSONL rejection")
			}
		})
	}
}

func TestValidateUniversalManifestRejectsRowCountMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"data/items.jsonl":2}`)
	writeUniversalManifestFixture(t, tmpDir, "data/items.jsonl", "{\"_schema\":1}\n{\"id\":1}\n")

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want row-count rejection")
	}
}

func TestValidateUniversalManifestRejectsUnlistedAttachment(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{}`)
	writeUniversalManifestFixture(t, tmpDir, "attachments/nested/file.bin", "payload")

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want unlisted-attachment rejection")
	}
}

func TestValidateUniversalManifestRejectsAttachmentCountMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"attachments/":2}`)
	writeUniversalManifestFixture(t, tmpDir, "attachments/file.bin", "payload")

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want attachment-count rejection")
	}
}

func TestValidateUniversalManifestAcceptsCanonicalEntries(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{
		"data/items.jsonl": 2,
		"system/_users.jsonl": 0,
		"exchange/_exchange_changes.jsonl": 1,
		"settings/safe.jsonl": 1,
		"attachments/": 2
	}`)
	writeUniversalManifestFixture(t, tmpDir, "data/items.jsonl", "{\"_schema\":1}\n{\"id\":1}\n{\"id\":2}\n")
	writeUniversalManifestFixture(t, tmpDir, "system/_users.jsonl", "{\"_schema\":1}\n")
	writeUniversalManifestFixture(t, tmpDir, "exchange/_exchange_changes.jsonl", "{\"_schema\":1}\n{\"id\":1}\n")
	writeUniversalManifestFixture(t, tmpDir, "settings/safe.jsonl", "{\"_schema\":1}\n{\"key\":\"network_enabled\",\"value\":\"false\"}\n")
	writeUniversalManifestFixture(t, tmpDir, "attachments/first.bin", "first")
	writeUniversalManifestFixture(t, tmpDir, "attachments/nested/second.bin", "second")
	writeUniversalManifestFixture(t, tmpDir, "config/app.yaml", "tables: []\n")

	if err := validateUniversalManifest(tmpDir); err != nil {
		t.Fatalf("validateUniversalManifest() error = %v, want nil", err)
	}
}

func TestValidateUniversalManifestRejectsCrossSectionSystemTable(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"data/_users.jsonl":1}`)
	writeUniversalManifestFixture(t, tmpDir, "data/_users.jsonl", "{\"_schema\":1}\n{\"id\":1}\n")

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want reserved-table rejection")
	}
}

func TestValidateUniversalManifestRejectsUnknownSystemTable(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{"system/_sessions.jsonl":0}`)
	writeUniversalManifestFixture(t, tmpDir, "system/_sessions.jsonl", "{\"_schema\":1}\n")

	if err := validateUniversalManifest(tmpDir); err == nil {
		t.Fatal("validateUniversalManifest() error = nil, want non-portable system-table rejection")
	}
}

func TestValidateUniversalManifestAcceptsEmptyDataWithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	writeUniversalManifestFixture(t, tmpDir, "manifest.json", `{}`)
	writeUniversalManifestFixture(t, tmpDir, "config/app.yaml", "tables: []\n")

	if err := validateUniversalManifest(tmpDir); err != nil {
		t.Fatalf("validateUniversalManifest() error = %v, want nil", err)
	}
}

func TestImportUniversalValidatesManifestBeforeMutatingConfig(t *testing.T) {
	configDest := t.TempDir()
	target := filepath.Join(configDest, "app.yaml")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entries := map[string]string{
		"META.txt":        "onebase_full_export\nversion=2\nformat=universal\n",
		"manifest.json":   `{"data/items.jsonl":1}`,
		"config/app.yaml": "replacement\n",
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	db := newSQLite(t, "manifest-preflight-order")
	if _, err := ImportUniversal(context.Background(), db, "file", configDest, "", bytes.NewReader(archive.Bytes()), int64(archive.Len())); err == nil {
		t.Fatal("ImportUniversal() error = nil, want missing-manifest-payload rejection")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Fatalf("configuration was mutated before manifest validation: got %q", got)
	}
}

func writeUniversalManifestFixture(t *testing.T, root, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
