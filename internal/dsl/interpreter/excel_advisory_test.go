package interpreter_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/testutil/xlsxadvisory"
)

func TestExcelImport_PublicAdvisories(t *testing.T) {
	for _, name := range []string{"ПрочитатьExcel", "ImportExcel"} {
		for _, scenario := range xlsxadvisory.Cases {
			t.Run(name+"/"+scenario, func(t *testing.T) {
				xlsxadvisory.Run(t, func() {
					data := xlsxadvisory.Book(t, scenario)
					path := filepath.Join(t.TempDir(), "input.xlsx")
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					proc := parseProc(t, fmt.Sprintf(`Функция Тест()
   Строки = %s(ПутьКниги);
   Возврат Строки[0][0];
  КонецФункции`, name))
					vars := interpreter.NewFileFunctions(nil)
					vars["ПутьКниги"] = path
					result, err := runSandboxEntry(t, "RunSandboxed", proc, interpreter.SandboxProfile{}, vars)
					xlsxadvisory.NoRecoveredPanic(t, err)
					if scenario == "cyclic-array" {
						if err != nil || result != "7" {
							t.Fatalf("cached value changed: %v, %v", result, err)
						}
					} else if err == nil || result != nil {
						t.Fatalf("bad workbook continued: %v, %v", result, err)
					}
					after, e := os.ReadFile(path)
					if e != nil || !bytes.Equal(data, after) {
						t.Fatalf("source file changed: %v", e)
					}
				})
			})
		}
	}
}
