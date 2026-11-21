package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// StageStandaloneSQLite copies a finished backup into the caller's temporary
// directory. Opening a WAL-mode image with SQLite, even read-only, can create
// sidecars beside it. Only the private copy is subsequently opened by SQLite.
func StageStandaloneSQLite(ctx context.Context, source, staged string) error {
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	before, err := standaloneSQLiteInfo(source)
	if err != nil {
		return err
	}
	if err := copyFileSynced(ctx, source, staged, 0o600); err != nil {
		return err
	}
	after, err := standaloneSQLiteInfo(source)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("файл резервной копии изменился во время чтения: %s", source)
	}
	return nil
}

func standaloneSQLiteInfo(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("ожидается непустой файл резервной копии SQLite: %s", path)
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		_, err := os.Lstat(path + suffix)
		if err == nil {
			return nil, fmt.Errorf("рядом с файлом есть %s: укажите автономную копию от onebase backup", path+suffix)
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return info, nil
}
