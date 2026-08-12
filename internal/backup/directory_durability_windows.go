//go:build windows

package backup

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

var flushDirectoryHandle = windows.FlushFileBuffers

// syncDirectoryMetadata flushes the directory's own entries. os.Open(path)
// is insufficient on Windows because it opens the directory read-only while
// FlushFileBuffers requires GENERIC_WRITE. FILE_FLAG_BACKUP_SEMANTICS permits
// opening a directory handle; OPEN_REPARSE_POINT makes a concurrent reparse
// point substitution fail the attribute check instead of flushing its target.
func syncDirectoryMetadata(path string) (resultErr error) {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pathp,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fmt.Errorf("open directory for metadata flush %s: %w", path, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, windows.CloseHandle(handle))
	}()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect directory before metadata flush %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refuse to flush non-directory or reparse point: %s", path)
	}
	if err := flushDirectoryHandle(handle); err != nil {
		return fmt.Errorf("flush directory metadata %s: %w", path, err)
	}
	return nil
}

func durableRenamePath(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// Destination names are reserved and verified absent. Do not request
	// replacement: an unexpected occupant is a state conflict, not something
	// recovery may overwrite.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
