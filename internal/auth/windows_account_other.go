//go:build !windows

package auth

import "errors"

func resolveWindowsAccount(string) (string, string, error) {
	return "", "", errors.New("для привязки учётной записи требуется Windows")
}
