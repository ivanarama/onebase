// Package userdata resolves OneBase's application data without changing the
// operating system's user profile. Portable children inherit the bundle root.
package userdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PortableRootEnv is inherited by launcher-managed run/migrate/window children.
// It is the bundle root, not the .onebase data directory.
const PortableRootEnv = "ONEBASE_PORTABLE_ROOT"

func PortableRoot() string { return os.Getenv(PortableRootEnv) }

// Path resolves a data path without creating it. Read-only callers stay read-only.
func Path(parts ...string) (string, error) {
	root := PortableRoot()
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = home
	} else if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s must be absolute", PortableRootEnv)
	}
	return filepath.Join(append([]string{root, ".onebase"}, parts...)...), nil
}

// Prepare runs before startup logging and Cobra execution. Relative command
// arguments in portable mode are anchored to the bundle, including after reexec.
func Prepare(args []string) error {
	portable := false
	specified := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--portable" {
			specified = true
			portable = true
		} else if value, ok := strings.CutPrefix(arg, "--portable="); ok {
			specified = true
			var err error
			portable, err = strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("--portable: %w", err)
			}
		}
	}
	if specified && !portable {
		return os.Unsetenv(PortableRootEnv)
	}
	root := PortableRoot()
	if portable {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("portable: executable directory: %w", err)
		}
		root = filepath.Dir(exe)
	}
	if root == "" {
		return nil
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%s must be absolute", PortableRootEnv)
	}
	dir := filepath.Join(root, ".onebase")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("portable: create %s: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("portable: directory %s is not writable: %w", dir, err)
	}
	if err := errors.Join(probe.Close(), os.Remove(probe.Name())); err != nil {
		return fmt.Errorf("portable: write test in %s: %w", dir, err)
	}
	if err := os.Setenv(PortableRootEnv, root); err != nil {
		return err
	}
	return os.Chdir(root)
}

// Resolve expands registry paths in a portable bundle. Other modes preserve
// their existing path semantics, including explicitly configured external paths.
func Resolve(path string) string {
	if path == "" || filepath.IsAbs(path) || PortableRoot() == "" {
		return path
	}
	return filepath.Join(PortableRoot(), filepath.FromSlash(path))
}

// StorePath keeps locations inside the bundle relative, and external locations
// absolute. It does not change the caller's Base object or follow file symlinks.
func StorePath(path string) string {
	root := PortableRoot()
	if path == "" || root == "" {
		return path
	}
	absolute := Resolve(path)
	rel, err := filepath.Rel(root, absolute)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return absolute
}
