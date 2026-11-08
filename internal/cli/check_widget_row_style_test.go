package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestCheckWidgetRowStyleWildcard(t *testing.T) {
	for _, tc := range []struct {
		name, projection, background string
		valid                        bool
	}{
		{"star", "*", "Цвет", true},
		{"qualified_star", "З.*", "Цвет", true},
		{"missing_column", "*", "Цвят", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: row-style-star\n")
			writeProcrunFixture(t, dir, "catalogs/Задача.yaml", `name: Задача
fields:
  - name: Цвет
    type: string
  - name: Срочно
    type: bool
`)
			writeProcrunFixture(t, dir, "widgets/Задачи.yaml", fmt.Sprintf(`name: Задачи
type: list
query: 'ВЫБРАТЬ %s ИЗ Справочник.Задача КАК З'
row_style:
  background: %s
  bold: Срочно
`, tc.projection, tc.background))
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
			if tc.valid {
				if err != nil {
					t.Fatalf("check отверг выходные колонки *: %v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(out, "widget.row-style") || !strings.Contains(out, tc.background) {
				t.Fatalf("check не обнаружил отсутствующую колонку: %v\n%s", err, out)
			}
		})
	}
}
