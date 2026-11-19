//go:build linux

package interpreter

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileMD5RejectsNamedPipe(t *testing.T) {
	name := filepath.Join(t.TempDir(), "photo.jpg")
	if err := unix.Mkfifo(name, 0o600); err != nil {
		t.Fatal(err)
	}

	rejected := make(chan any, 1)
	go func() {
		defer func() { rejected <- recover() }()
		_, _ = fileMD5Fn([]any{name}, "", 0)
	}()
	select {
	case reason := <-rejected:
		if reason == nil {
			t.Fatal("a named pipe is not a regular file")
		}
	case <-time.After(time.Second):
		t.Fatal("opening a named pipe blocked")
	}
}
