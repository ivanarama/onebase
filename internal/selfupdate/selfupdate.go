// Package selfupdate реализует ядро офлайн-обновления бинаря onebase из
// локального архива или файла: извлечение, проверку контрольной суммы, атомарную
// подмену бинаря с откатом и опрос readiness-пробы. Оркестрация системного
// сервиса (sc.exe/systemctl) живёт в internal/cli/update.go — здесь только
// кросс-платформенная, юнит-тестируемая механика.
package selfupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const MaxBinaryBytes = 512 << 20 // 512 MiB uncompressed

// BinaryName возвращает ожидаемое имя бинаря onebase для текущей ОС.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "onebase.exe"
	}
	return "onebase"
}

// StageBinary готовит новый бинарь к установке из fromPath, который может быть
// либо ZIP-архивом (внутри ищется onebase[.exe]), либо самим исполняемым файлом.
// Для ZIP бинарь извлекается в stageDir; для файла возвращается его же путь.
func StageBinary(fromPath, stageDir string) (string, error) {
	if strings.EqualFold(filepath.Ext(fromPath), ".zip") {
		return extractFromZip(fromPath, stageDir)
	}
	info, err := os.Stat(fromPath)
	if err != nil {
		return "", fmt.Errorf("selfupdate: файл обновления не найден: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("selfupdate: файл обновления не является обычным файлом")
	}
	if info.Size() > MaxBinaryBytes {
		return "", fmt.Errorf("selfupdate: бинарь превышает лимит %d байт", MaxBinaryBytes)
	}
	return fromPath, nil
}

func extractFromZip(zipPath, stageDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("selfupdate: открыть архив: %w", err)
	}
	defer oblog.CloseQuiet("selfupdate", "архив", zr)

	want := BinaryName()
	var candidate *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != want {
			continue
		}
		if candidate != nil {
			return "", fmt.Errorf("selfupdate: архив содержит несколько файлов %s", want)
		}
		candidate = f
	}
	if candidate != nil {
		f := candidate
		if !f.Mode().IsRegular() {
			return "", fmt.Errorf("selfupdate: %s в архиве не является обычным файлом", want)
		}
		if f.UncompressedSize64 > MaxBinaryBytes {
			return "", fmt.Errorf("selfupdate: бинарь в архиве превышает лимит %d байт", MaxBinaryBytes)
		}
		if err := os.MkdirAll(stageDir, fsmode.Dir); err != nil {
			return "", err
		}
		dst := filepath.Join(stageDir, want)
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer oblog.CloseQuiet("selfupdate", "поток", rc)
		if err := writeFile(io.LimitReader(rc, MaxBinaryBytes+1), dst, 0o755); err != nil {
			return "", err
		}
		info, err := os.Stat(dst)
		if err != nil {
			return "", err
		}
		if info.Size() > MaxBinaryBytes {
			_ = os.Remove(dst)
			return "", fmt.Errorf("selfupdate: бинарь в архиве превышает лимит %d байт", MaxBinaryBytes)
		}
		return dst, nil
	}
	return "", fmt.Errorf("selfupdate: в архиве %s не найден %s", filepath.Base(zipPath), want)
}

// VerifySHA256 проверяет, что sha256 файла совпадает с wantHex (регистр не важен).
func VerifySHA256(path, wantHex string) error {
	want, err := hex.DecodeString(strings.TrimSpace(wantHex))
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("selfupdate: SHA256 должен содержать ровно 64 шестнадцатеричных символа")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("selfupdate", "файл", f)
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := h.Sum(nil)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return fmt.Errorf("selfupdate: контрольная сумма не сошлась: ожидали %s, получили %s", wantHex, hex.EncodeToString(got))
	}
	return nil
}

// SwapBinary заменяет бинарь по targetPath содержимым newPath, сохраняя прежний
// бинарь рядом (targetPath+".old") для отката. Приём с переименованием работает и
// на Windows, где запущенный .exe нельзя перезаписать, но можно переименовать:
// старый бинарь уезжает в .old, новый пишется на освободившееся имя. Возвращает
// путь к сохранённой копии.
func SwapBinary(targetPath, newPath string) (string, error) {
	backupPath := targetPath + ".old"
	// Освобождаем имя ПЕРЕИМЕНОВАНИЕМ, а не копированием: Windows не даёт
	// перезаписать запущенный .exe, но даёт его переименовать. Именно так
	// лаунчер обновляет сам себя — он и есть тот процесс, чей файл меняется.
	// Работающий процесс продолжает жить со своим (уже переименованным) файлом.
	//
	// Раньше здесь старый бинарь копировался в .old, а новый писался поверх
	// занятого имени — на самообновлении это падало с «Access is denied».
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		// Остаток прошлой попытки: Windows не переименует поверх существующего.
		return "", fmt.Errorf("selfupdate: убрать прежнюю резервную копию %s: %w", backupPath, err)
	}
	if err := durableRename(targetPath, backupPath, false); err != nil {
		return "", fmt.Errorf("selfupdate: освободить имя бинаря: %w", err)
	}

	restore := func(cause error) error {
		if rErr := durableRename(backupPath, targetPath, false); rErr != nil {
			return fmt.Errorf("selfupdate: %w; вернуть старый бинарь не удалось: %v", cause, rErr)
		}
		return cause
	}

	in, err := os.Open(newPath)
	if err != nil {
		return "", restore(fmt.Errorf("selfupdate: открыть новый бинарь: %w", err))
	}
	defer oblog.CloseQuiet("selfupdate", "исходный файл", in)

	if err := writeFile(in, targetPath, 0o755); err != nil {
		// Недописанный файл убираем, иначе Rename ниже упрётся в него.
		if rmErr := os.Remove(targetPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return "", fmt.Errorf("selfupdate: записать новый бинарь: %w; убрать недописанный файл: %v", err, rmErr)
		}
		return "", restore(fmt.Errorf("selfupdate: записать новый бинарь: %w", err))
	}
	return backupPath, nil
}

// Rollback возвращает бинарь из backupPath на место targetPath — вызывается, если
// новый бинарь не прошёл /healthz. Сервис к этому моменту должен быть остановлен,
// иначе запущенный targetPath на Windows не удалить.
func Rollback(targetPath, backupPath string) error {
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("selfupdate: резервный бинарь недоступен: %w", err)
	}
	backup, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("selfupdate: открыть резервный бинарь: %w", err)
	}
	writeErr := writeFile(backup, targetPath, 0o755)
	// Закрываем ДО удаления, а не через defer: Windows не даёт удалить открытый
	// файл, и молчаливый `_ = os.Remove` оставлял бы резервную копию на месте.
	// Тогда повторный откат находил бы её снова и отчитывался успехом, хотя
	// откатываться уже некуда.
	oblog.CloseQuiet("selfupdate", "резервную копию бинарника", backup)
	if writeErr != nil {
		return fmt.Errorf("selfupdate: восстановить старый бинарь: %w", writeErr)
	}
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		// Не ошибка операции: бинарь на месте, а лишняя копия — вопрос уборки.
		// Возвращать её наверх нельзя, иначе удавшийся откат выглядел бы
		// провалившимся.
		oblog.Component("selfupdate").Warn("резервная копия не удалена", "file", backupPath, "err", err)
	}
	return nil
}

// writeFile записывает r в path с правами perm, атомарно перезаписывая содержимое.
func writeFile(r io.Reader, path string, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	out, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	committed := false
	defer func() {
		_ = out.Close()
		if !committed {
			_ = os.Remove(tmp)
		}
	}()
	if runtime.GOOS != "windows" {
		if err := out.Chmod(perm); err != nil {
			return err
		}
	}
	if _, err := io.Copy(out, r); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := durableRename(tmp, path, true); err != nil {
		return err
	}
	committed = true
	return nil
}

// PollHealthz опрашивает url раз в interval, пока не получит HTTP 200 или пока не
// истечёт timeout. Возвращает nil при первом 200, иначе — последнюю ошибку.
func PollHealthz(ctx context.Context, url string, timeout, interval time.Duration) error {
	return PollHealthzVersion(ctx, url, "", timeout, interval)
}

// PollHealthzVersion verifies that the responding process is the expected new
// binary rather than an unrelated listener on the same port.
func PollHealthzVersion(ctx context.Context, url, expectedVersion string, timeout, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for {
		if lastErr = probeOnce(ctx, client, url, expectedVersion); lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("selfupdate: %s не поднялся за %s: %w", url, timeout, lastErr)
		case <-time.After(interval):
		}
	}
}

func probeOnce(ctx context.Context, client *http.Client, url, expectedVersion string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // G104: bodyclose распознаёт только прямой вызов; тело прочитано, закрытие вторично
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s вернул %d", url, resp.StatusCode)
	}
	if expectedVersion != "" && resp.Header.Get("X-OneBase-Version") != expectedVersion {
		return fmt.Errorf("%s ответил версией %q, ожидалась %q", url, resp.Header.Get("X-OneBase-Version"), expectedVersion)
	}
	return nil
}
