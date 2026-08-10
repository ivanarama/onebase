package interpreter

import (
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/ast"
)

// BindNamedArgs связывает именованные значения (параметры обработки) с
// одноимёнными аргументами объявленной процедуры.
//
// Зачем. Параметры обработки инжектировались только как переменные, а
// `Процедура Выполнить(ModelName = "")` объявляет СВОЙ параметр с тем же
// именем — он затенял инжектированный и приходил пустым. Ошибки при этом не
// было: обработка отрабатывала «успешно», просто со значением по умолчанию,
// и выглядело это как «--set не биндится» (#706). Описать параметры сигнатурой
// процедуры — естественная привычка (так и в 1С), поэтому ловушку правильнее
// убрать, а не задокументировать.
//
// Возвращается ПРЕФИКС аргументов, а не позиционный список с дырами: параметр,
// которому нет одноимённого значения, не передаётся вовсе — иначе явный nil
// затёр бы его значение по умолчанию. Если первый же параметр не совпал, не
// передаётся ничего, и поведение остаётся прежним.
func BindNamedArgs(decl *ast.ProcedureDecl, values map[string]any) []any {
	if decl == nil || len(decl.Params) == 0 || len(values) == 0 {
		return nil
	}
	lower := make(map[string]any, len(values))
	for k, v := range values {
		lower[strings.ToLower(k)] = v
	}
	args := make([]any, 0, len(decl.Params))
	for _, p := range decl.Params {
		v, ok := lower[strings.ToLower(p.Literal)]
		if !ok {
			break // дальше пойдут значения по умолчанию
		}
		args = append(args, v)
	}
	if len(args) == 0 {
		return nil
	}
	return args
}
