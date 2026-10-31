package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/userdata"
)

func TestPortableStoreRelocatesAndPreservesUnknownFields(t *testing.T) {
	original := filepath.Join(t.TempDir(), "original bundle")
	moved := filepath.Join(t.TempDir(), "moved bundle")
	t.Setenv(userdata.PortableRootEnv, original)
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	base := &Base{Name: "Portable", DBType: "sqlite", ConfigSource: "file",
		Path: filepath.Join(original, "project"), DBPath: filepath.Join(original, "data", "demo.db")}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), original) || !strings.Contains(string(data), "data/demo.db") {
		t.Fatalf("registry is not relative:\n%s", data)
	}
	data = append(data, []byte("future_setting: keep-this # retained\n")...)
	if err := os.MkdirAll(filepath.Join(moved, ".onebase"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moved, ".onebase", "ibases.yaml"), data, 0o600); err != nil { //nolint:gosec // G703: destination is this test's t.TempDir, filename fixed
		t.Fatal(err)
	}
	t.Setenv(userdata.PortableRootEnv, moved)
	store, err = NewStore()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(base.ID)
	if err != nil || loaded.Path != filepath.Join(moved, "project") || loaded.DBPath != filepath.Join(moved, "data", "demo.db") {
		t.Fatalf("relocated base=%+v err=%v", loaded, err)
	}
	loaded.Name = "Renamed"
	if err := store.Update(loaded); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchLastOpened(base.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), moved) || !strings.Contains(string(data), "keep-this # retained") {
		t.Fatalf("update expanded paths or lost unknown data:\n%s", data)
	}
	if loaded.DBPath != filepath.Join(moved, "data", "demo.db") {
		t.Fatal("Update mutated caller's absolute path")
	}
}
