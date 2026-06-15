package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidApplyPath(t *testing.T) {
	ok := []string{"catalogs/клиент.yaml", "documents/заявка.yaml", "registers/продажи.yaml", "enums/статус.yaml"}
	for _, p := range ok {
		if _, err := validApplyPath(p); err != nil {
			t.Errorf("ожидался валидный путь %q: %v", p, err)
		}
	}
	bad := []string{"", "../evil.yaml", "catalogs/../x.yaml", "secret/x.yaml", "catalogs/a/b.yaml", "catalogs/клиент.txt", "catalogs/.yaml", "app.yaml"}
	for _, p := range bad {
		if _, err := validApplyPath(p); err == nil {
			t.Errorf("ожидалась ошибка для пути %q", p)
		}
	}
}

func TestApplyChanges_WritesFile(t *testing.T) {
	dir := t.TempDir()
	n, err := applyChanges(dir, []GenChange{{Path: "catalogs/клиент.yaml", NewContent: "name: Клиент\n"}})
	if err != nil || n != 1 {
		t.Fatalf("applyChanges: n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "catalogs", "клиент.yaml"))
	if err != nil || string(got) != "name: Клиент\n" {
		t.Fatalf("файл не записан правильно: %q err=%v", got, err)
	}
}

func TestApplyChanges_RejectsBadPath(t *testing.T) {
	dir := t.TempDir()
	n, err := applyChanges(dir, []GenChange{{Path: "../evil.yaml", NewContent: "x"}})
	if err == nil {
		t.Error("ожидалась ошибка для пути с ..")
	}
	if n != 0 {
		t.Errorf("ничего не должно примениться, applied=%d", n)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "evil.yaml")); !os.IsNotExist(statErr) {
		t.Error("файл вне базы не должен быть создан")
	}
}
