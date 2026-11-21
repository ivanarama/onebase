package sheet

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	workbookUpdateLock = ".onebase-xlsx.lock"
	workbookLockWait   = 10 * time.Second
	workbookLockPoll   = 25 * time.Millisecond
)

// UpdateXLSXFile accepts only an existing file immediately inside a
// server-administered directory that browser users cannot modify directly.
func (b *Workbook) UpdateXLSXFile(ctx context.Context, dir, filename string) ([]byte, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("на сервере не настроен абсолютный ONEBASE_REPORT_XLSX_DIR")
	}
	dir = filepath.Clean(dir)
	if filepath.IsAbs(filename) {
		relative, err := filepath.Rel(dir, filename)
		if err != nil {
			return nil, fmt.Errorf("файл должен находиться в настроенном каталоге отчётов")
		}
		filename = relative
	}
	if !ValidXLSXFileName(filename) {
		return nil, fmt.Errorf("укажите файл .xlsx в настроенном каталоге отчётов")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть каталог отчётов")
	}
	defer root.Close() //nolint:errcheck
	lease, err := lockWorkbookDirectory(ctx, root)
	if err != nil {
		return nil, err
	}
	defer lease.Close() //nolint:errcheck

	info, err := root.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("не удалось открыть существующий обычный файл XLSX")
	}
	src, err := root.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("книга XLSX недоступна для обновления")
	}
	actual, statErr := src.Stat()
	if statErr != nil || !os.SameFile(actual, info) {
		_ = src.Close()
		return nil, fmt.Errorf("книга XLSX изменилась во время открытия")
	}
	data, err := b.ReplaceXLSX(src)
	closeErr := src.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if len(data) > MaxUpdateXLSXBytes {
		return nil, fmt.Errorf("обновлённая книга XLSX превышает 32 МиБ")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := publishUpdatedWorkbook(ctx, root, filename, info, data); err != nil {
		return nil, err
	}
	return data, nil
}

// ValidXLSXFileName is also used for the browser-visible attachment name.
func ValidXLSXFileName(name string) bool {
	return filepath.IsLocal(name) && filepath.Base(name) == name && len(name) > len(".xlsx") &&
		strings.EqualFold(filepath.Ext(name), ".xlsx") && !strings.ContainsAny(name, "/\\:<>|\"?*") &&
		!strings.ContainsFunc(name, unicode.IsControl) && strings.TrimSpace(name) == name
}

func lockWorkbookDirectory(ctx context.Context, root *os.Root) (*os.File, error) {
	info, err := root.Lstat(workbookUpdateLock)
	if (err == nil && !info.Mode().IsRegular()) || (err != nil && !errors.Is(err, fs.ErrNotExist)) {
		return nil, fmt.Errorf("блокировка каталога XLSX недоступна")
	}
	f, err := root.OpenFile(workbookUpdateLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть блокировку каталога XLSX")
	}
	wait, cancel := context.WithTimeout(ctx, workbookLockWait)
	defer cancel()
	ticker := time.NewTicker(workbookLockPoll)
	defer ticker.Stop()
	for wait.Err() == nil {
		locked, lockErr := tryWorkbookLock(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("не удалось заблокировать каталог XLSX: %w", lockErr)
		}
		if locked {
			return f, nil
		}
		select {
		case <-wait.Done():
		case <-ticker.C:
		}
	}
	_ = f.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("другая операция уже обновляет книгу XLSX, повторите позже")
}

func publishUpdatedWorkbook(ctx context.Context, root *os.Root, filename string, original fs.FileInfo, data []byte) error {
	tempName := ".onebase-" + uuid.NewString() + ".tmp"
	tmp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("не удалось создать временную книгу XLSX")
	}
	defer root.Remove(tempName) //nolint:errcheck
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(original.Mode().Perm())
	}
	if err == nil {
		err = tmp.Sync()
	}
	if err = errors.Join(err, tmp.Close()); err != nil {
		return fmt.Errorf("не удалось записать временную книгу XLSX: %w", err)
	}
	current, err := root.Lstat(filename)
	if err != nil || !os.SameFile(original, current) || current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
		return fmt.Errorf("книга XLSX изменилась во время обновления")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(tempName, filename); err != nil {
		return fmt.Errorf("не удалось заменить книгу XLSX, проверьте доступ и закройте её в Excel")
	}
	return nil
}
