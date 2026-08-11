package launcher

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestRoleConfigSnapshotFailsClosed(t *testing.T) {
	h := &handler{}

	t.Run("file read directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "roles"), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		contents, names, err := h.roleConfigSnapshot(context.Background(), &Base{ConfigSource: "file", Path: root})
		if err == nil {
			t.Fatal("unreadable roles directory was treated as an empty snapshot")
		}
		if contents != nil || names != nil {
			t.Fatalf("partial snapshot returned on error: contents=%v names=%v", contents, names)
		}
	})

	t.Run("file malformed YAML", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "roles"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "roles", "broken.yaml"), []byte("name: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		contents, names, err := h.roleConfigSnapshot(context.Background(), &Base{ConfigSource: "file", Path: root})
		if err == nil {
			t.Fatal("malformed role YAML was skipped")
		}
		if contents != nil || names != nil {
			t.Fatalf("partial snapshot returned on error: contents=%v names=%v", contents, names)
		}
	})

	for name, source := range map[string]string{
		"file duplicate key":      "name: First\nname: Second\npermissions: {}\n",
		"file multiple documents": "name: First\npermissions: {}\n---\nname: Second\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "roles"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "roles", "unsafe.yaml"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			contents, names, err := h.roleConfigSnapshot(context.Background(), &Base{ConfigSource: "file", Path: root})
			if err == nil {
				t.Fatal("structurally unsafe role YAML was accepted")
			}
			if contents != nil || names != nil {
				t.Fatalf("partial snapshot returned on error: contents=%v names=%v", contents, names)
			}
		})
	}

	t.Run("database query", func(t *testing.T) {
		ctx := context.Background()
		dbPath := filepath.Join(t.TempDir(), "broken-config.db")
		db, err := storage.ConnectSQLite(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `CREATE TABLE _onebase_config (path TEXT PRIMARY KEY)`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()

		contents, names, err := h.roleConfigSnapshot(ctx, &Base{
			ConfigSource: "database",
			DBType:       "sqlite",
			DBPath:       dbPath,
		})
		if err == nil {
			t.Fatal("database read failure was treated as an empty snapshot")
		}
		if contents != nil || names != nil {
			t.Fatalf("partial snapshot returned on error: contents=%v names=%v", contents, names)
		}
	})
}

func TestSaveRoleConfigFileFileModeTargetFailurePreservesSource(t *testing.T) {
	root := t.TempDir()
	rolesDir := filepath.Join(root, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(rolesDir, "old.yaml")
	oldContent := []byte("name: Old\npermissions: {}\n")
	if err := os.WriteFile(oldPath, oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the target path makes publication fail portably on both
	// Windows and Unix. The source must not have been deleted first.
	if err := os.Mkdir(filepath.Join(rolesDir, "new.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := &handler{}
	err = h.saveRoleConfigFile(context.Background(), &Base{ConfigSource: "file", Path: root},
		"roles/new.yaml", []byte("name: New\npermissions: {}\n"), []string{"roles/old.yaml"}, "test", "New")
	if err == nil {
		t.Fatal("expected target publication failure")
	}
	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("old role was lost: %v", err)
	}
	if !bytes.Equal(got, oldContent) {
		t.Fatalf("old role changed: got %q, want %q", got, oldContent)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(oldPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != oldInfo.Mode().Perm() {
			t.Fatalf("old role mode changed: got %v, want %v", info.Mode().Perm(), oldInfo.Mode().Perm())
		}
	}
}

func TestSaveRoleConfigFileFileModeReplacesExistingAndKeepsMode(t *testing.T) {
	root := t.TempDir()
	rolesDir := filepath.Join(root, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(rolesDir, "operator.yaml")
	if err := os.WriteFile(target, []byte("name: Old\npermissions: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetFile, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := targetFile.Stat()
	closeErr := targetFile.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	want := []byte("name: Operator\npermissions: {}\n")

	if err := saveRoleConfigFileOnDisk(root, "roles/operator.yaml", want, nil); err != nil {
		t.Fatalf("replace existing role: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("target contains partial/wrong data: got %q, want %q", got, want)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("existing role inode was replaced, losing owner/ACL/security metadata")
	}
	if runtime.GOOS != "windows" {
		if after.Mode().Perm() != before.Mode().Perm() {
			t.Fatalf("mode changed: got %v, want %v", after.Mode().Perm(), before.Mode().Perm())
		}
	}
}

func TestSaveRoleConfigFileFileModeRenameKeepsSourceMetadata(t *testing.T) {
	root := t.TempDir()
	rolesDir := filepath.Join(root, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(rolesDir, "old.yaml")
	if err := os.WriteFile(source, []byte("name: Old\npermissions: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := sourceFile.Stat()
	closeErr := sourceFile.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	want := []byte("name: New\npermissions: {}\n")
	if err := saveRoleConfigFileOnDisk(root, "roles/new.yaml", want, []string{"roles/old.yaml"}); err != nil {
		t.Fatalf("rename role: %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("old source still exists: %v", err)
	}
	target := filepath.Join(rolesDir, "new.yaml")
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("renamed role did not retain the source inode/security metadata")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("renamed role content = %q, want %q", got, want)
	}
}

func TestSaveRoleConfigFileValidatesEverySourceBeforePublish(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := saveRoleConfigFileOnDisk(root, "roles/new.yaml",
		[]byte("name: New\npermissions: {}\n"), []string{"../outside.yaml"})
	if err == nil {
		t.Fatal("unsafe stale path was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "roles", "new.yaml")); !os.IsNotExist(err) {
		t.Fatalf("target was published before all source paths were validated: %v", err)
	}
}

func TestRoleSaveReloadsConfigurationBeforeMutation(t *testing.T) {
	h, projectDir := roleEditorBase(t, editorRoleYAML)
	rolePath := filepath.Join(projectDir, "roles", nameToFilename("Оператор")+".yaml")
	before, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.store.Get("roles-editor")
	if err != nil {
		t.Fatal(err)
	}
	db, err := getAuthDB(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	beforeLive, err := auth.ParseRole(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SyncRoles(context.Background(), []*auth.Role{beforeLive}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "catalogs", "broken.yaml"), []byte("name: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := saveRoleFromMatrix(t, h, "Оператор", "Оператор", "catalog|Клиент|read")
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409; body=%q", rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("role file changed while configuration was unavailable")
	}
	roles, err := repo.ListRoles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0].Name != beforeLive.Name ||
		!auth.PermissionHas(roles[0].Permissions, "catalog", "Клиент", "write") {
		t.Fatalf("live roles changed while configuration was unavailable: %+v", roles)
	}
}

func TestRoleSaveRejectsCaseInsensitiveCollisions(t *testing.T) {
	t.Run("case-only rename", func(t *testing.T) {
		h, projectDir := roleEditorBase(t, "name: Admin\npermissions: {}\n")
		source := filepath.Join(projectDir, "roles", nameToFilename("Оператор")+".yaml")
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		rec := saveRoleFromMatrix(t, h, "admin", "Admin")
		if rec.Code != http.StatusConflict {
			t.Fatalf("code = %d, want 409; body=%q", rec.Code, rec.Body.String())
		}
		after, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatal("source changed on rejected case-only rename")
		}
	})

	t.Run("configuration name", func(t *testing.T) {
		h, projectDir := roleEditorBase(t, "name: Admin\npermissions: {}\n")
		source := filepath.Join(projectDir, "roles", nameToFilename("Оператор")+".yaml")
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}

		rec := saveRoleFromMatrix(t, h, "admin", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("code = %d, want 409; body=%q", rec.Code, rec.Body.String())
		}
		after, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatal("existing role changed on case-insensitive collision")
		}
		if _, err := os.Stat(filepath.Join(projectDir, "roles", "admin.yaml")); !os.IsNotExist(err) {
			t.Fatalf("colliding target was created: %v", err)
		}
	})

	t.Run("target path", func(t *testing.T) {
		h, projectDir := roleEditorBase(t, "")
		target := filepath.Join(projectDir, "roles", "admin.yaml")
		before := []byte("name: Other\npermissions: {}\n")
		if err := os.WriteFile(target, before, 0o600); err != nil {
			t.Fatal(err)
		}

		rec := saveRoleFromMatrix(t, h, "Admin", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("code = %d, want 409; body=%q", rec.Code, rec.Body.String())
		}
		after, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatal("occupied target path was overwritten")
		}
	})

	t.Run("live table", func(t *testing.T) {
		h, projectDir := roleEditorBase(t, "")
		b, err := h.store.Get("roles-editor")
		if err != nil {
			t.Fatal(err)
		}
		db, err := getAuthDB(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		repo := auth.NewRepo(db)
		if err := repo.SyncRoles(context.Background(), []*auth.Role{{Name: "Admin"}}); err != nil {
			t.Fatal(err)
		}

		rec := saveRoleFromMatrix(t, h, "admin", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("code = %d, want 409; body=%q", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(projectDir, "roles", "admin.yaml")); !os.IsNotExist(err) {
			t.Fatalf("file created despite live role collision: %v", err)
		}
		roles, err := repo.ListRoles(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(roles) != 1 || roles[0].Name != "Admin" {
			t.Fatalf("live roles changed: %+v", roles)
		}
	})
}

func TestRoleSaveRejectsUnsafeFileName(t *testing.T) {
	for _, name := range []string{"bad:name", "CON"} {
		t.Run(name, func(t *testing.T) {
			h, projectDir := roleEditorBase(t, "")
			rec := saveRoleFromMatrix(t, h, name, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400; body=%q", rec.Code, rec.Body.String())
			}
			entries, err := os.ReadDir(filepath.Join(projectDir, "roles"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("unsafe name created config entries: %v", entries)
			}
		})
	}
}

func TestStaleRolePermsOmitsRowsWithoutManagedOperations(t *testing.T) {
	role := &auth.Role{
		Name: "Operator",
		Permissions: auth.Permission{Catalogs: map[string][]string{
			"ManagedGone": {"read", "disclose"},
			"CustomGone":  {"disclose"},
		}},
	}
	stale := staleRolePerms([]*auth.Role{role}, &configuratorData{})
	if got := stale["catalog"]; len(got) != 1 || got[0] != "ManagedGone" {
		t.Fatalf("stale catalog rows = %v, want only ManagedGone", got)
	}
	html := roleMatrixHTML(&configuratorData{}, stale)
	if bytes.Contains([]byte(html), []byte("CustomGone")) {
		t.Fatal("custom-only stale permission was shown as removable by the matrix")
	}
}
