//go:build windows

package installtest

import "golang.org/x/sys/windows"

// privateRoot — НАСТОЯЩИЙ профиль пользователя.
//
// Именно его спрашивает проверка приватности установки, и спрашивает
// намеренно не через USERPROFILE: переменную можно направить в Program Files и
// тем самым обойти границу. Фикстура обязана смотреть туда же, иначе тест,
// подменивший USERPROFILE (а так делает изоляция состояния обновлений), создаст
// «приватный» каталог там, где продукт приватности не видит.
func privateRoot() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
}
