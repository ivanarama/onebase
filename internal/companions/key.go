// Package companions — общее правило ключа сопутствующей программы рабочего
// места: его проверяют и сервер (ключи, которые конфигурация называет
// пользователю), и лаунчер (ключи в записи базы).
package companions

import (
	"regexp"
	"strings"
)

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

// NormalizeKey приводит ключ к нижнему регистру и проверяет его: латиница,
// цифры, «_», «.», «-», до 32 символов, первый символ — буква или цифра.
func NormalizeKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	return key, keyRe.MatchString(key)
}
