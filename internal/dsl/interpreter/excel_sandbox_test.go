package interpreter_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/excel"
	"github.com/stretchr/testify/require"
)

func TestSandbox_ExcelImportHonorsFilePolicy(t *testing.T) {
	data, err := excel.ExportList([]string{"Code"}, [][]any{{"007"}})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "private.xlsx")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		for _, name := range []string{"ПрочитатьExcel", "ImportExcel"} {
			for _, profile := range []struct {
				name string
				p    interpreter.SandboxProfile
			}{
				{name: "Allowed"},
				{name: "DenyFile", p: interpreter.SandboxProfile{DenyFile: true}},
				{name: "Restricted", p: interpreter.RestrictedProfile()},
			} {
				t.Run(entry+"/"+name+"/"+profile.name, func(t *testing.T) {
					proc := parseProc(t, fmt.Sprintf(`Функция Тест()
						Строки = %s(ПутьКниги);
						Возврат Строки[1][0];
					КонецФункции`, name))
					// As in a normal processor run, the caller supplies file functions.
					// The sandbox must enforce its profile over these permissive vars.
					vars := interpreter.NewFileFunctions(nil)
					vars["ПутьКниги"] = path
					result, err := runSandboxEntry(t, entry, proc, profile.p, vars)
					if profile.p.DenyFile {
						require.ErrorContains(t, err, "файловые операции запрещены")
						return
					}
					require.NoError(t, err)
					require.Equal(t, "007", result)
				})
			}
		}
	}
}
