//go:build !windows

package installtest

// privateRoot на не-Windows не нужен: приватность там задаётся правами предка
// (0700), а не расположением внутри профиля.
func privateRoot() (string, error) { return "", nil }
