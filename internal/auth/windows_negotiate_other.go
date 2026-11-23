//go:build !windows

package auth

import "errors"

func acceptWindowsTicket(_ string, _ []byte) (string, []byte, error) {
	return "", nil, errors.New("SSPI доступен только на Windows")
}
