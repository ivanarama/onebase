package interpreter

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

func openFileDirectory(dir, relative string) (*os.Root, string, error) {
	if !filepath.IsLocal(relative) {
		return nil, "", i18nerr.Errorf("относительный путь выходит за пределы каталога: %s", relative)
	}
	relative = filepath.Clean(relative)
	path, err := resolveSafePath(dir)
	if err != nil {
		return nil, "", err
	}
	if base := FileSandboxRoot(); base != "" {
		sandbox, err := os.OpenRoot(base)
		if err != nil {
			return nil, "", err
		}
		defer oblog.CloseQuiet("dsl", "рабочий каталог", sandbox)
		subdir, err := filepath.Rel(base, path)
		if err != nil {
			return nil, "", err
		}
		root, err := sandbox.OpenRoot(subdir)
		return root, relative, err
	}
	root, err := os.OpenRoot(path) //nolint:gosec // trusted explicit DSL file root, restricted by the profile guard
	return root, relative, err
}

func findInDirectory(dir, relative, mask string) (*Array, error) {
	root, relative, err := openFileDirectory(dir, relative)
	if err != nil {
		return nil, err
	}
	defer oblog.CloseQuiet("dsl", "корень поиска", root)
	folder, err := root.OpenFile(relative, fileMD5OpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer oblog.CloseQuiet("dsl", "каталог поиска", folder)
	info, err := folder.Stat()
	if err != nil {
		return nil, err
	}
	result := &Array{}
	if !info.IsDir() {
		return result, nil
	}
	entries, err := folder.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() && !entry.Type().IsDir() {
			continue
		}
		matched, err := filepath.Match(mask, entry.Name())
		if err != nil {
			return nil, err
		}
		if matched {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		result.items = append(result.items, filepath.ToSlash(filepath.Join(relative, name)))
	}
	return result, nil
}

func copyFromDirectory(dir, relative, destination string) error {
	root, relative, err := openFileDirectory(dir, relative)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("dsl", "корень копирования", root)
	source, _, err := openRegularFileInDirectory(root, relative)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("dsl", "исходный файл", source)
	destination, err = resolveSafePath(destination)
	if err != nil {
		return err
	}
	out, err := createRootedCopyDestination(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, source)
	return errors.Join(copyErr, out.Close())
}

func createRootedCopyDestination(path string) (*os.File, error) {
	base := FileSandboxRoot()
	if base == "" {
		return os.Create(path) //nolint:gosec // trusted destination as in existing CopyFile
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer oblog.CloseQuiet("dsl", "рабочий каталог", root)
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return nil, err
	}
	return root.Create(relative)
}

func deleteFromDirectory(dir, relative, expectedMD5 string) (bool, error) {
	if len(expectedMD5) != 32 {
		return false, i18nerr.Errorf("для удаления файла нужна контрольная сумма MD5")
	}
	root, relative, err := openFileDirectory(dir, relative)
	if err != nil {
		return false, err
	}
	defer oblog.CloseQuiet("dsl", "корень удаления", root)
	source, info, err := openRegularFileInDirectory(root, relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	digest, readErr := fileMD5(source)
	if err := errors.Join(readErr, source.Close()); err != nil {
		return false, err
	}
	if !strings.EqualFold(digest, expectedMD5) {
		return false, nil
	}
	current, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !os.SameFile(info, current) || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
		return false, nil
	}
	if err := root.Remove(relative); err != nil {
		return false, err
	}
	return true, nil
}

func openRegularFileInDirectory(root *os.Root, relative string) (*os.File, os.FileInfo, error) {
	file, err := root.OpenFile(relative, fileMD5OpenFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = i18nerr.Errorf("путь не является обычным файлом: %s", relative)
	}
	if err != nil {
		oblog.CloseQuiet("dsl", "файл", file)
		return nil, nil, err
	}
	return file, info, nil
}
