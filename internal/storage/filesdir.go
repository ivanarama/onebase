package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RequireFilesDir returns the pinned absolute root after checking its nearest
// existing ancestor. It never creates a missing tree or falls back to cwd/temp.
// The small private probe checks OS permissions (including ACLs); useful I/O
// must still handle errors because permissions can change after this check.
// Connect retains its eager home check until all recovery callers are migrated.
func (db *DB) RequireFilesDir() (string, error) {
	root := db.FilesDir()
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("storage: durable files directory unavailable; set %s to an absolute persistent path: %w", filesDirEnv, err)
	}
	if root == "" || !filepath.IsAbs(root) {
		return fail(fmt.Errorf("files root must be a nonempty absolute path"))
	}
	root = filepath.Clean(root)
	ancestor := root
	for {
		info, err := os.Stat(ancestor)
		if err == nil {
			if !info.IsDir() {
				return fail(fmt.Errorf("%q is not a directory", ancestor))
			}
			break
		}
		if !os.IsNotExist(err) {
			return fail(err)
		}
		// A dangling symlink is an unavailable configured root, not a missing
		// directory that can safely be created beneath a usable ancestor.
		if _, linkErr := os.Lstat(ancestor); linkErr == nil {
			return fail(err)
		} else if !os.IsNotExist(linkErr) {
			return fail(linkErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fail(err)
		}
		ancestor = parent
	}
	// Opening and reading the directory detects unreadable roots before a
	// metadata DELETE, while creating a unique probe checks write/search access.
	dir, err := os.Open(ancestor)
	if err != nil {
		return fail(err)
	}
	_, readErr := dir.Readdirnames(1)
	closeErr := dir.Close()
	if readErr != nil && readErr != io.EOF {
		return fail(readErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	probe, err := os.CreateTemp(ancestor, ".onebase-files-check-*")
	if err != nil {
		return fail(err)
	}
	closeErr = probe.Close()
	removeErr := os.Remove(probe.Name())
	if closeErr != nil {
		return fail(closeErr)
	}
	if removeErr != nil {
		return fail(removeErr)
	}
	return root, nil
}
