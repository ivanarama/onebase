package auth

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultMinPasswordLength = 8
	maxBcryptPasswordBytes   = 72

	allowEmptyPasswordsEnv = "ONEBASE_ALLOW_EMPTY_PASSWORDS" //nolint:gosec // G101: это не секрет — имя переменной окружения либо строка-плейсхолдер, которую пользователь заменяет своим ключом
	minPasswordLengthEnv   = "ONEBASE_MIN_PASSWORD_LENGTH"
)

var (
	ErrPasswordRequired = errors.New("пароль не может быть пустым")
	ErrPasswordTooShort = errors.New("пароль слишком короткий")
	ErrPasswordTooLong  = errors.New("пароль слишком длинный")
)

// PasswordPolicy is process-wide authentication policy captured when a Repo
// is created. Empty passwords are disabled by default and require an explicit
// kiosk-mode opt-in through ONEBASE_ALLOW_EMPTY_PASSWORDS=true.
type PasswordPolicy struct {
	MinLength  int
	AllowEmpty bool
}

func passwordPolicyFromEnv() PasswordPolicy {
	policy := PasswordPolicy{
		MinLength:  DefaultMinPasswordLength,
		AllowEmpty: envBool(os.Getenv(allowEmptyPasswordsEnv)),
	}
	if raw := strings.TrimSpace(os.Getenv(minPasswordLengthEnv)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= maxBcryptPasswordBytes {
			policy.MinLength = n
		}
	}
	return policy
}

func envBool(raw string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && v
}

func (p PasswordPolicy) validate(password string) error {
	byteLen := len(password)
	if byteLen == 0 {
		if p.AllowEmpty {
			return nil
		}
		return ErrPasswordRequired
	}
	if utf8.RuneCountInString(password) < p.MinLength {
		return fmt.Errorf("%w: минимум %d символов", ErrPasswordTooShort, p.MinLength)
	}
	if byteLen > maxBcryptPasswordBytes {
		return fmt.Errorf("%w: максимум %d байта", ErrPasswordTooLong, maxBcryptPasswordBytes)
	}
	return nil
}
