//go:build !windows && !linux

package auth

import (
	"context"
	"errors"
)

func resolveWindowsAccount(context.Context, string) (string, string, error) {
	return "", "", errors.New("эта ОС не поддерживает привязку учётной записи Windows")
}
