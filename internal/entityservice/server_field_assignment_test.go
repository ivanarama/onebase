package entityservice

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

func TestServerAssignedFieldTwoFieldsReadonly(t *testing.T) {
	vars := map[string]any{}
	finish := InstallServerAssignedFieldQuery(vars, map[string]bool{"исполнитель": true, "типзадачи": true})
	defer finish()
	for _, alias := range []string{"ПолеНазначеноСервером", "ServerAssignedField"} {
		fn := vars[alias].(interpreter.BuiltinFunc)
		for _, name := range []string{"Исполнитель", "ТипЗадачи", "ТипЗадачи", "Исполнитель", "Исполнитель", "ТипЗадачи"} {
			if value, err := fn([]any{name}, "", 0); value != true || err != nil {
				t.Fatalf("%s(%s): %v %v", alias, name, value, err)
			}
		}
	}
}

func TestServerAssignedFieldQueryScope(t *testing.T) {
	vars := map[string]any{}
	finish := InstallServerAssignedFieldQuery(vars, map[string]bool{"executor": true})
	fn := vars["ServerAssignedField"].(interpreter.BuiltinFunc)
	for _, tc := range []struct {
		name string
		args []any
		want bool
	}{{"field", []any{"EXECUTOR"}, true}, {"other", []any{"note"}, false}, {"empty", nil, false}, {"typed", []any{1}, false}, {"extra", []any{"executor", true}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := fn(tc.args, "", 0)
			if err != nil || v != tc.want {
				t.Fatalf("%v %v", v, err)
			}
		})
	}
	finish()
	if v, _ := fn([]any{"executor"}, "", 0); v != false {
		t.Fatal("escaped hook lifetime")
	}
}
