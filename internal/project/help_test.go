package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/userhelp"
)

func TestHelpSurvivesDatabaseVersionsAndTempCleanup(t *testing.T) {
	dir := t.TempDir()
	if err := project.Scaffold(dir, "test"); err != nil {
		t.Fatal(err)
	}
	if err := userhelp.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := configdb.New(db)
	if err := repo.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := repo.ImportFromDir(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	baseline, err := repo.CreateVersion(t.Context(), configdb.VersionOptions{Message: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveFile(t.Context(), "docs/help/introduction.md", []byte("Updated guide")); err != nil {
		t.Fatal(err)
	}
	p, err := project.LoadFromDB(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Fatal("temporary source directory still exists")
	}
	if c := p.Help.Find("introduction"); c == nil || c.Markdown != "Updated guide" {
		t.Fatalf("guide lost after database source cleanup: %+v", c)
	}
	if _, err := repo.RollbackToVersion(t.Context(), baseline.ID, configdb.VersionOptions{Message: "rollback"}); err != nil {
		t.Fatal(err)
	}
	p, err = project.LoadFromDB(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if c := p.Help.Find("introduction"); c == nil || c.Markdown == "Updated guide" {
		t.Fatal("rollback did not restore the guide")
	}
}
