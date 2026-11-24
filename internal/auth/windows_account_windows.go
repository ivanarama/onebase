package auth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func resolveWindowsAccount(_ context.Context, name string) (string, string, error) {
	sid, _, kind, err := windows.LookupSID("", name)
	if err != nil {
		return "", "", fmt.Errorf("не удалось найти учётную запись Windows: %w", err)
	}
	if kind != windows.SidTypeUser {
		return "", "", errors.New("для входа требуется учётная запись пользователя Windows")
	}
	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return "", "", fmt.Errorf("не удалось определить имя пользователя Windows: %w", err)
	}
	rawSID := sid.String()
	if rawSID == "" {
		return "", "", errors.New("не удалось определить SID пользователя Windows")
	}
	return rawSID, domain + `\` + account, nil
}
