package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// row_style формы списка через полный checker (тот же, что onebase check):
// YAML формы проходит загрузчик, пути и цвета проверяются.
func TestRunFull_FormListRowStyle(t *testing.T) {
	project := func(t *testing.T, kind, rowStyle string) string {
		t.Helper()
		dir := t.TempDir()
		write := func(rel, body string) {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("config/app.yaml", "name: rowstyle\n")
		write("catalogs/Реклама.yaml", "name: Реклама\nfields:\n  - name: Цвет\n    type: string\n")
		write("catalogs/Задача.yaml", "name: Задача\nfields:\n  - name: Наименование\n    type: string\n  - name: Реклама\n    type: reference:Реклама\n  - name: Пытался\n    type: bool\n")
		write("forms/задача/списка.form.yaml", "schema: onebase.form/v1\nform:\n  name: ФормаСписка\n  kind: "+kind+"\n"+rowStyle)
		return dir
	}
	messages := func(dir string) []string {
		var out []string
		for _, is := range RunFull(dir).Issues {
			if is.Code == "yaml.invalid" {
				t.Fatalf("мини-проект не загрузился: %s", is.Message)
			}
			if is.Code == "form.row-style" {
				out = append(out, is.Message)
			}
		}
		return out
	}
	good := `  row_style:
    rules:
      - field: Наименование
        equals: "Срочно"
        background: "#FAFAD2"
    background_from: Реклама.Цвет
    bold_from: Пытался
`
	if got := messages(project(t, "list", good)); len(got) != 0 {
		t.Fatalf("верный row_style: ложные ошибки %v", got)
	}
	for name, tc := range map[string]struct{ kind, style, want string }{
		"опечатка_в_пути":    {"list", "  row_style:\n    background_from: Реклама.Цвят\n", "Цвят"},
		"путь_не_по_ссылке":  {"list", "  row_style:\n    background_from: Наименование.Цвет\n", "не ссылка"},
		"цвет_не_по_формату": {"list", "  row_style:\n    rules:\n      - field: Наименование\n        equals: x\n        background: red\n", "не цвет"},
		"не_форма_списка":    {"object", good, "только у формы списка"},
	} {
		t.Run(name, func(t *testing.T) {
			got := messages(project(t, tc.kind, tc.style))
			if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Fatalf("нет ошибки «%s»: %v", tc.want, got)
			}
		})
	}
}
