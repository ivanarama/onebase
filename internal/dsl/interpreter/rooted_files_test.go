package interpreter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootedFiles_CopyListAndVerifiedRemoval(t *testing.T) {
	parent := t.TempDir()
	queue := filepath.Join(parent, "queue")
	if err := os.MkdirAll(filepath.Join(queue, "chat"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(queue, "chat", "photo.jpg")
	if err := os.WriteFile(source, []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := callB(t, "найтифайлы", "chat", "*.jpg", queue).(*Array)
	if len(found.items) != 1 || found.items[0] != "chat/photo.jpg" {
		t.Fatalf("rooted results = %v", found.items)
	}
	temporary := filepath.Join(parent, "temporary.jpg")
	callB(t, "копироватьфайл", found.items[0], temporary, queue)
	oldHash := callB(t, "md5файла", temporary)
	if err := os.WriteFile(source, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if callB(t, "удалитьфайлы", found.items[0], queue, oldHash) != false {
		t.Fatal("removed a changed upload")
	}
	currentHash := callB(t, "md5файла", source)
	if callB(t, "удалитьфайлы", found.items[0], queue, currentHash) != true {
		t.Fatal("matching upload was not removed")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
}

func TestRootedFiles_RejectEscapingPaths(t *testing.T) {
	parent := t.TempDir()
	queue := filepath.Join(parent, "queue")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{queue, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exterior := filepath.Join(outside, "photo.jpg")
	if err := os.WriteFile(exterior, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(parent, "staged.jpg")
	digest := callB(t, "md5файла", exterior)
	assertRejected := func(directory, file string) {
		t.Helper()
		for name, args := range map[string][]any{
			"найтифайлы":     {directory, "*.jpg", queue},
			"копироватьфайл": {file, staged, queue},
			"удалитьфайлы":   {file, queue, digest},
		} {
			if _, err := builtins[name](args, "", 0); err == nil {
				t.Errorf("%s accepted %s", name, file)
			}
		}
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Errorf("outside content was copied: %v", err)
		}
		if content, err := os.ReadFile(exterior); err != nil || string(content) != "outside" {
			t.Errorf("outside content was changed or removed: %q, %v", content, err)
		}
	}
	assertRejected("../outside", "../outside/photo.jpg")
	if err := os.Symlink(outside, filepath.Join(queue, "jump")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	assertRejected("jump", "jump/photo.jpg")
}
