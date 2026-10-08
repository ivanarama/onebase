package access

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

// Список встроенных атрибутов в тексте ошибки обязан совпадать с тем, что
// resolveUserAttr действительно понимает: иначе подсказка отправит автора
// политики к несуществующему атрибуту.
func TestBuiltinUserAttrsResolve(t *testing.T) {
	u := &auth.User{}
	for _, name := range strings.Split(builtinUserAttrs, ", ") {
		if _, ok := resolveUserAttr(u, name); !ok {
			t.Errorf("атрибут %q из builtinUserAttrs не распознаётся resolveUserAttr", name)
		}
	}
}
