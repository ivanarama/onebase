package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
}

func TestStore_EmptyList(t *testing.T) {
	s := newTestStore(t)
	bases, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bases) != 0 {
		t.Fatalf("want 0 bases, got %d", len(bases))
	}
}

func TestStore_AddAndList(t *testing.T) {
	s := newTestStore(t)

	b := &Base{Name: "Склад", DB: "postgres://localhost/sklad", Port: 8080}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if b.ID == "" {
		t.Fatal("ID should be auto-set by Add")
	}
	if b.Created.IsZero() {
		t.Fatal("Created should be auto-set by Add")
	}
	if b.ConfigSource == "" {
		t.Fatal("ConfigSource should be defaulted by Add")
	}

	bases, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bases) != 1 {
		t.Fatalf("want 1 base, got %d", len(bases))
	}
	if bases[0].Name != "Склад" {
		t.Fatalf("want Name=Склад, got %q", bases[0].Name)
	}
}

func TestStore_Get(t *testing.T) {
	s := newTestStore(t)
	b := &Base{Name: "ERP", DB: "postgres://localhost/erp"}
	s.Add(b)

	got, err := s.Get(b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "ERP" {
		t.Fatalf("want ERP, got %q", got.Name)
	}

	_, err = s.Get("nonexistent-id")
	if err == nil {
		t.Fatal("Get with unknown ID should return error")
	}
}

func TestStore_Update(t *testing.T) {
	s := newTestStore(t)
	b := &Base{Name: "Old", DB: "postgres://localhost/old"}
	s.Add(b)

	b.Name = "New"
	b.Port = 9090
	if err := s.Update(b); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.Get(b.ID)
	if got.Name != "New" {
		t.Fatalf("want New, got %q", got.Name)
	}
	if got.Port != 9090 {
		t.Fatalf("want 9090, got %d", got.Port)
	}
}

func TestStore_Remove(t *testing.T) {
	s := newTestStore(t)
	b1 := &Base{Name: "A", DB: "postgres://localhost/a"}
	b2 := &Base{Name: "B", DB: "postgres://localhost/b"}
	s.Add(b1)
	s.Add(b2)

	if err := s.Remove(b1.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	bases, _ := s.List()
	if len(bases) != 1 {
		t.Fatalf("want 1 after remove, got %d", len(bases))
	}
	if bases[0].Name != "B" {
		t.Fatalf("wrong base left after remove: %q", bases[0].Name)
	}
}

func TestStore_AtomicWrite(t *testing.T) {
	s := newTestStore(t)
	b := &Base{Name: "Atomic", DB: "postgres://localhost/atomic"}
	if err := s.Add(b); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The .tmp file should not exist — it was renamed to the final path
	tmpPath := s.path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatal(".tmp file should be cleaned up after atomic write")
	}

	// The actual file should exist
	if _, err := os.Stat(s.path); err != nil {
		t.Fatalf("ibases.yaml not found: %v", err)
	}
}

func TestStore_MultipleOps_Persistence(t *testing.T) {
	s := newTestStore(t)

	for i := range 3 {
		s.Add(&Base{
			Name: []string{"Alpha", "Beta", "Gamma"}[i],
			DB:   "postgres://localhost/db",
		})
	}

	bases, _ := s.List()
	if len(bases) != 3 {
		t.Fatalf("want 3 bases, got %d", len(bases))
	}

	// Reload from same file to verify persistence
	s2 := &Store{path: s.path}
	bases2, _ := s2.List()
	if len(bases2) != 3 {
		t.Fatalf("persisted store should have 3 bases, got %d", len(bases2))
	}
}
