//go:build windows

package selfupdate

import "golang.org/x/sys/windows"

// platformDurableRename waits for the same-volume metadata transition to be
// flushed before returning. Plain os.Rename ultimately uses MoveFileEx without
// MOVEFILE_WRITE_THROUGH and is therefore not a durable commit primitive.
func platformDurableRename(source, destination string, replace bool) error {
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
