//go:build !windows

package launcher

import "os"

func replaceStoreFile(source, destination string) error {
	return os.Rename(source, destination)
}
