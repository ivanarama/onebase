//go:build windows

package launcher

import "golang.org/x/sys/windows"

func replaceStoreFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// A file Sync alone does not make the directory-entry replacement durable
	// on Windows. MoveFileEx with WRITE_THROUGH closes that power-loss window;
	// REPLACE_EXISTING preserves the atomic-store semantics of os.Rename.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
