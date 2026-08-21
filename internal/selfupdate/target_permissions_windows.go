//go:build windows

package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var windowsPrivateInstallRoot = func() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
}

func validateTargetCoordinationDirectory(path string, _ os.FileInfo) error {
	// Do not trust USERPROFILE/HOME: both are caller-controlled and could be
	// pointed at Program Files to bypass the private-install boundary.
	home, err := windowsPrivateInstallRoot()
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return err
	}
	if !pathWithinWindowsRoot(home, path) {
		// Причина именно расположение, а не права: запись сюда уже проверена
		// отдельно (CanWriteBinaryDir). Интерфейс обязан различать эти случаи,
		// поэтому отказ опознаваем через errors.Is (#1065).
		return fmt.Errorf("%w: %s is outside the current user profile %s", ErrTargetNotPrivate, path, home)
	}
	return nil
}

func pathWithinWindowsRoot(root, path string) bool {
	// Сравнение чисто текстовое, а один и тот же каталог Windows называет
	// по-разному: `C:\Users\RUNNER~1\…` и `C:\Users\runneradmin\…` — это 8.3-псевдоним
	// и длинное имя одного пути. Без приведения к канонической форме личный
	// каталог, пришедший в короткой записи, объявлялся бы «вне профиля» — то
	// есть ровно той ложной причиной, ради которой заведена #1065.
	//
	// Приводим ОБЕ стороны: разнобой форм у root и path дал бы тот же
	// расходящийся ответ. Если канонизировать не удалось (каталога ещё нет —
	// так приходит `C:\Program Files\onebase` из проверки системной установки),
	// сравниваем исходные пути: прежнее поведение, не хуже.
	if r, p, ok := canonicalWindowsPair(root, path); ok {
		root, path = r, p
	}
	root = strings.ToLower(filepath.Clean(root))
	path = strings.ToLower(filepath.Clean(path))
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canonicalWindowsPair разворачивает 8.3-псевдонимы и переходы (junction,
// symlink) в длинные пути. Разворот идёт до сравнения намеренно: граница
// «внутри профиля» обязана смотреть на настоящее расположение каталога, иначе
// переход внутри профиля, ведущий наружу, читался бы как личная установка.
func canonicalWindowsPair(root, path string) (string, string, bool) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	return r, p, true
}

// On Windows the file inherits the installation directory DACL. Chmod would
// synthesize DOS attributes, not safely express the parent ACL's principals.
func applyTargetCoordinationPermissions(*os.File, os.FileMode) error { return nil }
