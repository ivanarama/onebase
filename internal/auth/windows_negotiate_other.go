//go:build !windows && !linux

package auth

import (
	"context"
	"errors"
)

func acceptWindowsTicket(context.Context, string, []byte) (string, []byte, error) {
	return "", nil, errors.New("вход через Windows не поддерживается на этой платформе")
}
