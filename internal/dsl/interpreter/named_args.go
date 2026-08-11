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
// Возвращается позиционный список до последнего найденного параметра. Дыры в
// нём помечаются внутренним sentinel: callUserProc отличает «значение не
// передали» от явно переданного nil и вычисляет DSL-default только в первом
// случае. Это позволяет передать второй именованный параметр, не затирая
// default первого.
func BindNamedArgs(decl *ast.ProcedureDecl, values map[string]any) []any {
	if decl == nil || len(decl.Params) == 0 || len(values) == 0 {
		return nil
	}
	lower := make(map[string]any, len(values))
	for k, v := range values {
		lower[strings.ToLower(k)] = v
	}
	last := -1
	args := make([]any, len(decl.Params))
	for idx := range args {
		args[idx] = missingNamedArg{}
	}
	for idx, p := range decl.Params {
		v, ok := lower[strings.ToLower(p.Literal)]
		if !ok {
			continue
		}
		args[idx] = v
		last = idx
	}
	if last < 0 {
		return nil
	}
	return args[:last+1]
}

// missingNamedArg — внутренняя дыра в результате BindNamedArgs. Отдельный тип
// нужен, потому что nil является полноценным явно переданным значением.
type missingNamedArg struct{}
