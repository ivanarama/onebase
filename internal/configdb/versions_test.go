package configdb_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
)

func TestRepoVersions_SaveDiffRollback(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)

	if err := repo.SaveFile(ctx, "config/app.yaml", []byte("name: v1\n")); err != nil {
		t.Fatalf("SaveFile v1: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions len = %d, want 1", len(versions))
	}
	baseline := versions[0]
	if baseline.Message != "save config/app.yaml" {
		t.Fatalf("baseline message = %q", baseline.Message)
	}

	if err := repo.SaveFile(ctx, "config/app.yaml", []byte("name: v2\n")); err != nil {
		t.Fatalf("SaveFile v2: %v", err)
	}
	if err := repo.SaveFile(ctx, "reports/sales.yaml", []byte("name: sales\n")); err != nil {
		t.Fatalf("SaveFile report: %v", err)
	}
	versions, err = repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions after changes: %v", err)
	}
	latest := versions[0]

	diff, err := repo.DiffVersions(ctx, baseline.ID, latest.ID)
	if err != nil {
		t.Fatalf("DiffVersions: %v", err)
	}
	want := map[string]configdb.DiffKind{
		"config/app.yaml":    configdb.DiffModified,
		"reports/sales.yaml": configdb.DiffAdded,
	}
	if len(diff) != len(want) {
		t.Fatalf("diff = %+v", diff)
	}
	for _, d := range diff {
		if want[d.Path] != d.Kind {
			t.Fatalf("diff entry = %+v", d)
		}
	}

	rolled, err := repo.RollbackToVersion(ctx, baseline.ID, configdb.VersionOptions{Message: "rollback test"})
	if err != nil {
		t.Fatalf("RollbackToVersion: %v", err)
	}
	if rolled.ID == "" || rolled.ID == baseline.ID || rolled.Message != "rollback test" {
		t.Fatalf("rollback version = %+v", rolled)
	}
	content, ok, err := repo.ReadFile(ctx, "config/app.yaml")
	if err != nil || !ok {
		t.Fatalf("ReadFile app: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(content, []byte("name: v1\n")) {
		t.Fatalf("rolled back content = %q", content)
	}
	if _, ok, err := repo.ReadFile(ctx, "reports/sales.yaml"); err != nil || ok {
		t.Fatalf("report should be absent after rollback: ok=%v err=%v", ok, err)
	}
}

func TestRepoVersions_DeleteCreatesVersion(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)
	if err := repo.SaveFile(ctx, "src/a.os", []byte("Процедура X()\nКонецПроцедуры\n")); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	versions, _ := repo.ListVersions(ctx, 10)
	beforeDelete := versions[0]

	if err := repo.DeleteFile(ctx, "src/a.os"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions len = %d, want 2", len(versions))
	}
	if versions[0].Message != "delete src/a.os" {
		t.Fatalf("delete message = %q", versions[0].Message)
	}
	diff, err := repo.DiffVersions(ctx, beforeDelete.ID, versions[0].ID)
	if err != nil {
		t.Fatalf("DiffVersions delete: %v", err)
	}
	if len(diff) != 1 || diff[0].Path != "src/a.os" || diff[0].Kind != configdb.DiffDeleted {
		t.Fatalf("delete diff = %+v", diff)
	}
}

func TestRepoVersions_DeleteMissingDoesNotCreateVersion(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)
	if err := repo.DeleteFile(ctx, "missing.yaml"); err != nil {
		t.Fatalf("DeleteFile missing: %v", err)
	}
	versions, err := repo.ListVersions(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("versions after no-op delete = %+v", versions)
	}
}

func TestRepoVersions_SaveFilesCreatesSingleVersion(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)
	files := []configdb.ConfigFile{
		{Path: "forms/order/form.form.yaml", Content: []byte("kind: object\n")},
		{Path: "forms/order/form.form.os", Content: []byte("Процедура X()\nКонецПроцедуры\n")},
	}
	if err := repo.SaveFiles(ctx, files, configdb.VersionOptions{Message: "save managed form"}); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].Message != "save managed form" {
		t.Fatalf("versions = %+v, want one custom message", versions)
	}
	for _, f := range files {
		content, ok, err := repo.ReadFile(ctx, f.Path)
		if err != nil || !ok || !bytes.Equal(content, f.Content) {
			t.Fatalf("ReadFile(%s): ok=%v err=%v content=%q", f.Path, ok, err, content)
		}
	}
}

func TestRepoVersions_DeleteFilesCreatesSingleVersion(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "forms/order/form.form.yaml", Content: []byte("kind: object\n")},
		{Path: "forms/order/form.form.os", Content: []byte("Процедура X()\nКонецПроцедуры\n")},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		t.Fatalf("SaveFiles seed: %v", err)
	}
	if err := repo.DeleteFiles(ctx, []string{
		"forms/order/form.form.yaml",
		"forms/order/form.form.os",
		"forms/order/missing.txt",
	}, configdb.VersionOptions{Message: "delete managed form"}); err != nil {
		t.Fatalf("DeleteFiles: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].Message != "delete managed form" {
		t.Fatalf("versions = %+v, want seed + one delete", versions)
	}
	if _, ok, err := repo.ReadFile(ctx, "forms/order/form.form.yaml"); err != nil || ok {
		t.Fatalf("yaml should be deleted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := repo.ReadFile(ctx, "forms/order/form.form.os"); err != nil || ok {
		t.Fatalf("os should be deleted: ok=%v err=%v", ok, err)
	}
}

func TestRepoVersions_ApplyFilesCreatesSingleVersion(t *testing.T) {
	repo, _, ctx := newSQLiteRepo(t)
	if err := repo.SaveFiles(ctx, []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte("name: old\nlogo: config/old.png\n")},
		{Path: "config/old.png", Content: []byte("old logo")},
	}, configdb.VersionOptions{Message: "seed"}); err != nil {
		t.Fatalf("SaveFiles seed: %v", err)
	}
	if err := repo.ApplyFiles(ctx, []configdb.ConfigFile{
		{Path: "config/app.yaml", Content: []byte("name: new\nlogo: config/new.png\n")},
		{Path: "config/new.png", Content: []byte("new logo")},
	}, []string{"config/old.png", "config/missing.png"}, configdb.VersionOptions{Message: "save app settings"}); err != nil {
		t.Fatalf("ApplyFiles: %v", err)
	}
	versions, err := repo.ListVersions(ctx, 10)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].Message != "save app settings" {
		t.Fatalf("versions = %+v, want seed + one apply", versions)
	}
	content, ok, err := repo.ReadFile(ctx, "config/app.yaml")
	if err != nil || !ok || !bytes.Contains(content, []byte("name: new")) {
		t.Fatalf("ReadFile app: ok=%v err=%v content=%q", ok, err, content)
	}
	content, ok, err = repo.ReadFile(ctx, "config/new.png")
	if err != nil || !ok || !bytes.Equal(content, []byte("new logo")) {
		t.Fatalf("ReadFile new logo: ok=%v err=%v content=%q", ok, err, content)
	}
	if _, ok, err := repo.ReadFile(ctx, "config/old.png"); err != nil || ok {
		t.Fatalf("old logo should be deleted: ok=%v err=%v", ok, err)
	}
}
