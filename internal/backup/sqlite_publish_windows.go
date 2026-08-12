//go:build windows

package backup

import "golang.org/x/sys/windows"

func moveSQLiteFile(source, destination string, replace bool) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(from, to, flags)
}

func publishSQLiteFileNoReplace(source, destination string) error {
	return moveSQLiteFile(source, destination, false)
}

func syncSQLiteDirectory(path string) error { return syncDirectoryMetadata(path) }
