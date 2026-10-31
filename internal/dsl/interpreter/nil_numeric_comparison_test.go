package interpreter_test

// Raw injected nil is Неопределено, not a typed number (#1274).
// Empty number fields are covered through HTTP/DSL and storage in
// ui/tp_empty_number_zero_test.go and query_typed_empty_matrix_test.go.
import (
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/require"
	"testing"
)

func evalExpr1136(t *testing.T, expression string, vars map[string]any) any {
	t.Helper()
	return evalWithVars(t, "Функция Т()\nВозврат "+expression+";\nКонецФункции", vars)
}

type assertLog1136 struct{ outcomes []interpreter.AssertOutcome }

func (r *assertLog1136) RecordAssert(o interpreter.AssertOutcome) { r.outcomes = append(r.outcomes, o) }

func TestUndefinedAssertionsAgreeWithOperators(t *testing.T) {
	for _, pair := range []struct {
		left, right string
		equal       bool
	}{
		{"Пусто", "0", false}, {"0", "Пусто", false},
		{"Пусто", "5", false}, {"Пусто", `""`, false},
		{"Пусто", `"<nil>"`, false}, {"Пусто", "Пусто", true},
		{"0", "0", true}, {"Пусто", "Неопределено", true},
	} {
		t.Run(pair.left+"="+pair.right, func(t *testing.T) {
			log := &assertLog1136{}
			vars := map[string]any{"Пусто": nil, "Утверждать": interpreter.NewAssertRoot(log)}
			src := "Функция Т()\nУтверждать.Равно(" + pair.left + ", " + pair.right + ");\n" +
				"Утверждать.НеРавно(" + pair.left + ", " + pair.right + ");\n" +
				"Возврат " + pair.left + " = " + pair.right + ";\nКонецФункции"
			require.Equal(t, pair.equal, evalWithVars(t, src, vars))
			require.Len(t, log.outcomes, 2)
			require.Equal(t, pair.equal, log.outcomes[0].Passed)
			require.Equal(t, !pair.equal, log.outcomes[1].Passed)
		})
	}
}

func TestUndefinedSearchResultDiffersFromFirstIndex(t *testing.T) {
	const src = `Функция Т()
        М = ["первый"];
        Возврат М.Найти("нет") = Неопределено И М.Найти("нет") <> 0
            И М.Найти("первый") = 0 И М.Найти("первый") <> Неопределено
            И ТипЗнч(М.Найти("нет")) = "Неопределено"
            И ТипЗнч(М.Найти("первый")) = "Число";
    КонецФункции`
	require.Equal(t, true, evalWithVars(t, src, nil))
}
