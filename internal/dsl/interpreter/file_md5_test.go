package interpreter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileMD5_BytesAndFileRestrictions(t *testing.T) {
	dir := t.TempDir()
	SetFileSandbox(dir)
	t.Cleanup(func() { SetFileSandbox("") })
	path := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(path, []byte{0xff, 0xd8, 0x00, 0xff, 0xd9}, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"md5файла", "filemd5"} {
		allowed := NewFileFunctions(nil)[name].(BuiltinFunc)
		got, err := allowed([]any{"photo.jpg"}, "", 0)
		if err != nil || got != "e9c1d0ba71a4ee24d1497ef64d206071" {
			t.Fatalf("%s: MD5 = %v, error = %v", name, got, err)
		}
		for _, badPath := range []string{".", filepath.Join(dir, "..", "unrelated.jpg")} {
			callBuiltinExpectPanic(t, allowed, []any{badPath})
		}

		deny := FileGuard(func() error { return errors.New("файлы запрещены") })
		denied := NewFileFunctions(deny)[name].(BuiltinFunc)
		if msg := callBuiltinExpectPanic(t, denied, []any{path}); !strings.Contains(msg, "файлы запрещены") {
			t.Fatalf("%s: отказ файлового guard: %s", name, msg)
		}
	}
}
