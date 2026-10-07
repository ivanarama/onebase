package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/query"
)

func queryTypeWarnings(result query.Result, file, object, kind string, line, column int) []Issue {
	if len(result.UnconvertedTypedFields) == 0 {
		return nil
	}
	return []Issue{{
		File:   file,
		Object: object,
		Kind:   kind,
		Code:   "query.complex-projection-types",
		Message: fmt.Sprintf(
			"сложная проекция (ОБЪЕДИНИТЬ или подзапрос) читает типизированные поля %s: ссылки, булево и даты в результате не приводятся к прикладному типу",
			strings.Join(result.UnconvertedTypedFields, ", "),
		),
		SuggestedFix: "Разбейте запрос на два простых запроса или явно обработайте строковые значения результата.",
		Line:         line,
		Column:       column,
	}}
}
