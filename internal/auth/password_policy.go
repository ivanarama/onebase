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
	// MaxPasswordLength — верхняя граница числового минимума в редакторах
	// политики. Минимум считается в Unicode-символах, а сам пароль отдельно
	// ограничен maxBcryptPasswordBytes байтами UTF-8.
	MaxPasswordLength = maxBcryptPasswordBytes

	allowEmptyPasswordsEnv = "ONEBASE_ALLOW_EMPTY_PASSWORDS" //nolint:gosec // G101: это не секрет — имя переменной окружения либо строка-плейсхолдер, которую пользователь заменяет своим ключом
	minPasswordLengthEnv   = "ONEBASE_MIN_PASSWORD_LENGTH"
)

// PasswordMinLengthSource explains which layer supplied the effective minimum.
// The stored value remains available separately through Policy.PasswordMinLength:
// zero there means that the database inherits the process default.
type PasswordMinLengthSource string

const (
	PasswordMinLengthSourceDefault     PasswordMinLengthSource = "default"
	PasswordMinLengthSourceEnvironment PasswordMinLengthSource = "environment"
	PasswordMinLengthSourceDatabase    PasswordMinLengthSource = "database"
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
	MinLength       int
	MinLengthSource PasswordMinLengthSource
	AllowEmpty      bool
}

func passwordPolicyFromEnv() PasswordPolicy {
	policy := PasswordPolicy{
		MinLength:       DefaultMinPasswordLength,
		MinLengthSource: PasswordMinLengthSourceDefault,
		AllowEmpty:      envBool(os.Getenv(allowEmptyPasswordsEnv)),
	}
	if raw := strings.TrimSpace(os.Getenv(minPasswordLengthEnv)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= maxBcryptPasswordBytes {
			policy.MinLength = n
			policy.MinLengthSource = PasswordMinLengthSourceEnvironment
		}
	}
	return policy
}

func envBool(raw string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && v
}

// applyStored накладывает политику паролей базы поверх умолчаний процесса.
// Переменные окружения задают умолчание, сохранённая политика его уточняет:
// длина берётся из базы, когда она там задана, а разрешение пустых паролей
// складывается — снять выданное переменной окружения из интерфейса нельзя
// (см. комментарий у Policy.AllowEmptyPasswords).
//
// Значение длины вне допустимого диапазона игнорируется: политика базы не
// должна уметь запретить вообще любой пароль (даже ASCII-пароль не бывает
// длиннее 72 символов из-за байтового предела bcrypt) или отменить проверку
// целиком.
func (p PasswordPolicy) applyStored(stored Policy) PasswordPolicy {
	if n := stored.PasswordMinLength; n >= 1 && n <= maxBcryptPasswordBytes {
		p.MinLength = n
		p.MinLengthSource = PasswordMinLengthSourceDatabase
	}
	if stored.AllowEmptyPasswords {
		p.AllowEmpty = true
	}
	return p
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
