//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedWritableTargetIsExplicitlyRejected(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared-bin")
	if err := os.Mkdir(target, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o775); err != nil { //nolint:gosec // G302: intentionally create a group-writable target to verify fail-closed policy
		t.Fatal(err)
	}
	// Класс причины проверяется через errors.Is, а не по тексту: по нему
	// интерфейс выбирает, что советовать пользователю, и «нет прав» здесь был бы
	// неверным советом — писать в каталог он как раз может (#1065).
	_, err := targetCoordinationPermissions(target)
	if !errors.Is(err, ErrTargetNotPrivate) {
		t.Fatalf("shared writable target error = %v, want ErrTargetNotPrivate", err)
	}
	if errors.Is(err, ErrTargetNotWritable) {
		t.Fatalf("общая установка выдана за отказ по правам: %v", err)
	}
}

func TestPublicReadOnlySystemStyleTargetCannotSelfUpdate(t *testing.T) {
	target, err := os.MkdirTemp(os.TempDir(), "onebase-public-install-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target) })
	if err := os.Chmod(target, 0o755); err != nil { //nolint:gosec // G302: intentionally model a public system-style install target
		t.Fatal(err)
	}
	if _, err := targetCoordinationPermissions(target); !errors.Is(err, ErrTargetNotPrivate) {
		t.Fatalf("public system-style target error = %v, want ErrTargetNotPrivate", err)
	}
}
