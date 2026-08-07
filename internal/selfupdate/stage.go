package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	oblog "github.com/ivantit66/onebase/internal/logging"
)

// PackageBinaries перечисляет исполняемые файлы, которые обновление обязано
// заменить целиком. На Windows их два: консольный onebase.exe и onebase-gui.exe
// с нативным окном (release.yml собирает оба). Лаунчер мог быть запущен из
// любого из них, а базы он поднимает дочерними процессами того же файла
// (launcher/runner.go), поэтому подменить только один — значит получить
// платформу двух разных версий на одной машине.
func PackageBinaries() []string {
	if runtime.GOOS == "windows" {
		return []string{"onebase.exe", "onebase-gui.exe"}
	}
	return []string{"onebase"}
}

// StageAll извлекает из архива обновления все бинари пакета в stageDir и
// возвращает карту «имя файла → путь во временном каталоге».
//
// Обязателен только основной бинарь (BinaryName): архив без onebase-gui.exe
// считается валидным — так выглядели сборки до плана 78, и на Linux GUI нет
// вовсе. Структура каталогов внутри архива игнорируется: файлы кладутся в
// stageDir по базовому имени, поэтому путь из архива не может увести запись
// наружу.
func StageAll(archivePath, stageDir string) (map[string]string, error) {
	wanted := make(map[string]bool, len(PackageBinaries()))
	for _, name := range PackageBinaries() {
		wanted[name] = true
	}

	var (
		out map[string]string
		err error
	)
	switch strings.ToLower(filepath.Ext(archivePath)) {
	case ".zip":
		out, err = stageFromZip(archivePath, stageDir, wanted)
	case ".gz", ".tgz":
		out, err = stageFromTarGz(archivePath, stageDir, wanted)
	default:
		return nil, fmt.Errorf("selfupdate: неизвестный формат архива %s (ожидались .zip или .tar.gz)", filepath.Base(archivePath))
	}
	if err != nil {
		return nil, err
	}
	if out[BinaryName()] == "" {
		return nil, fmt.Errorf("selfupdate: в архиве %s не найден %s", filepath.Base(archivePath), BinaryName())
	}
	return out, nil
}

func stageFromZip(zipPath, stageDir string, wanted map[string]bool) (map[string]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: открыть архив: %w", err)
	}
	defer oblog.CloseQuiet("selfupdate", "архив", zr)

	out := make(map[string]string, len(wanted))
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !wanted[name] {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, fmt.Errorf("selfupdate: %s в архиве не является обычным файлом", name)
		}
		if out[name] != "" {
			return nil, fmt.Errorf("selfupdate: архив содержит несколько файлов %s", name)
		}
		if f.UncompressedSize64 > MaxBinaryBytes {
			return nil, fmt.Errorf("selfupdate: %s в архиве превышает лимит %d байт", name, MaxBinaryBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		dst, err := stageOne(rc, stageDir, name)
		oblog.CloseQuiet("selfupdate", "поток", rc)
		if err != nil {
			return nil, err
		}
		out[name] = dst
	}
	return out, nil
}

func stageFromTarGz(archivePath, stageDir string, wanted map[string]bool) (map[string]string, error) {
	f, err := os.Open(archivePath) //nolint:gosec // G304: путь собран нами — это скачанный в свой каталог архив обновления
	if err != nil {
		return nil, fmt.Errorf("selfupdate: открыть архив: %w", err)
	}
	defer oblog.CloseQuiet("selfupdate", "архив", f)

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: распаковать gzip: %w", err)
	}
	defer oblog.CloseQuiet("selfupdate", "поток gzip", gz)

	out := make(map[string]string, len(wanted))
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("selfupdate: читать tar: %w", err)
		}
		name := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || !wanted[name] {
			continue
		}
		if out[name] != "" {
			return nil, fmt.Errorf("selfupdate: архив содержит несколько файлов %s", name)
		}
		if hdr.Size > MaxBinaryBytes {
			return nil, fmt.Errorf("selfupdate: %s в архиве превышает лимит %d байт", name, MaxBinaryBytes)
		}
		dst, err := stageOne(tr, stageDir, name)
		if err != nil {
			return nil, err
		}
		out[name] = dst
	}
	return out, nil
}

// stageOne пишет один файл из архива в stageDir, ограничивая объём: заявленный
// в заголовке размер — это слово удалённой стороны, а не факт.
func stageOne(r io.Reader, stageDir, name string) (string, error) {
	if err := os.MkdirAll(stageDir, fsmode.SecretDir); err != nil {
		return "", err
	}
	dst := filepath.Join(stageDir, name)
	if err := writeFile(io.LimitReader(r, MaxBinaryBytes+1), dst, 0o755); err != nil {
		return "", err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxBinaryBytes {
		_ = os.Remove(dst)
		return "", fmt.Errorf("selfupdate: %s превышает лимит %d байт", name, MaxBinaryBytes)
	}
	return dst, nil
}
