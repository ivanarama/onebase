//go:build windows

package backup

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSyncDirectoryMetadataUsesFlushFileBuffersAndPropagatesFailure(t *testing.T) {
	want := errors.New("injected directory flush failure")
	original := flushDirectoryHandle
	t.Cleanup(func() { flushDirectoryHandle = original })
	called := false
	flushDirectoryHandle = func(windows.Handle) error {
		called = true
		return want
	}

	err := syncDirectoryMetadata(t.TempDir())
	if !called {
		t.Fatal("syncDirectoryMetadata did not flush its Windows directory handle")
	}
	if !errors.Is(err, want) {
		t.Fatalf("syncDirectoryMetadata error = %v, want injected failure", err)
	}
}
