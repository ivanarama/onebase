package backup

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func makeUnreadableTestEntry(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("create exclusively opened test file: %v", err)
	}
	t.Cleanup(func() {
		_ = windows.CloseHandle(handle)
	})
}
