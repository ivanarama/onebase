package reviewrouting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func consumerFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	value := fixture(t, "consumer-tree-v1.json")
	root := t.TempDir()
	for name, raw := range value["files"].(map[string]any) {
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			t.Fatalf("fixture path escapes its private root: %q", name)
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw.(string)), 0o600); err != nil {
			t.Fatal(err)
		} //nolint:gosec // G703: paths are fixed testdata entries below this test's private t.TempDir.
	}
	return root, value
}

func TestSharedCompleteConsumerTreeHashes(t *testing.T) {
	root, value := consumerFixture(t)
	got, err := ConsumerTrees(root)
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range value["reader_sha256"].(map[string]any) {
		if got[role] != want {
			t.Fatalf("%s tree differs from Python: %s, want %v", role, got[role], want)
		}
	}
	reference := filepath.Join(root, "fix-approved", "references", "тест😀.md")
	if err := os.WriteFile(reference, []byte("changed referenced algorithm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := ConsumerTree(root, "fix")
	if err != nil {
		t.Fatal(err)
	}
	if changed == got["fix"] {
		t.Fatal("referenced consumer edit did not change complete tree binding")
	}
}

func TestConsumerTreeRefusesMissingDuplicateOrLinkedVersion(t *testing.T) {
	for _, fault := range []string{"missing", "duplicate", "linked"} {
		t.Run(fault, func(t *testing.T) {
			root, _ := consumerFixture(t)
			entry := filepath.Join(root, "review-queue", "SKILL.md")
			switch fault {
			case "missing":
				if err := os.WriteFile(entry, []byte("unversioned reader\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				data := strings.Repeat("<!-- pp:review-routing-v1-consumer stage=review -->\n", 2)
				if err := os.WriteFile(entry, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			case "linked":
				outside := filepath.Join(t.TempDir(), "outside.md")
				if err := os.WriteFile(outside, []byte("outside consumer\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "review-queue", "linked.md")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := ConsumerTree(root, "review"); err == nil {
				t.Fatal("unknown/linked reader tree was admitted")
			}
		})
	}
}
