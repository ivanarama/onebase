package userdata

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPortablePathsAndPreparation(t *testing.T) {
	for _, name := range []string{"directory", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if name == "symlink" {
				link := filepath.Join(t.TempDir(), "bundle")
				if err := os.Symlink(root, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("directory symlinks unavailable: %v", err)
					}
					t.Fatal(err)
				}
				root = link
			}
			t.Setenv(PortableRootEnv, root)
			t.Chdir(t.TempDir())
			profile, _ := os.UserHomeDir()
			if err := Prepare([]string{"run"}); err != nil {
				t.Fatal(err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			cwdInfo, err := os.Stat(cwd)
			if err != nil {
				t.Fatal(err)
			}
			rootInfo, err := os.Stat(root)
			if err != nil {
				t.Fatal(err)
			}
			// Getwd may resolve aliases such as /var -> /private/var on macOS.
			if !os.SameFile(cwdInfo, rootInfo) {
				t.Fatalf("cwd=%s want bundle root %s", cwd, root)
			}
			if got, _ := os.UserHomeDir(); got != profile {
				t.Fatal("Operating system profile was changed")
			}
			if path, err := Path("logs"); err != nil || path != filepath.Join(root, ".onebase", "logs") {
				t.Fatalf("Path=%s err=%v", path, err)
			}
			inside := filepath.Join(root, "data", "main.db")
			if got := StorePath(inside); got != "data/main.db" {
				t.Fatalf("stored inside path=%s", got)
			}
			if got := Resolve("data/main.db"); got != inside {
				t.Fatalf("resolved path=%s", got)
			}
			outside := filepath.Join(t.TempDir(), "other.db")
			if got := StorePath(outside); got != outside {
				t.Fatalf("external path changed=%s", got)
			}
			if got := StorePath(""); got != "" {
				t.Fatal("empty path changed")
			}
		})
	}
}

func TestPortableErrorsDoNotFallBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv(PortableRootEnv, root)
	if err := os.WriteFile(filepath.Join(root, ".onebase"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(nil); err == nil {
		t.Fatal("unwritable data directory accepted")
	}
	t.Setenv(PortableRootEnv, "relative")
	if _, err := Path(); err == nil {
		t.Fatal("relative inherited root accepted")
	}
	if err := Prepare(nil); err == nil {
		t.Fatal("relative root accepted by preparation")
	}
}

func TestPortableOffAndArgumentTerminator(t *testing.T) {
	t.Setenv(PortableRootEnv, t.TempDir())
	if err := Prepare([]string{"--portable=false"}); err != nil || PortableRoot() != "" {
		t.Fatalf("explicit disable: root=%q err=%v", PortableRoot(), err)
	}
	if err := Prepare([]string{"--", "--portable"}); err != nil || PortableRoot() != "" {
		t.Fatal("argument after terminator enabled portable mode")
	}
	if err := Prepare([]string{"--portable=invalid"}); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
