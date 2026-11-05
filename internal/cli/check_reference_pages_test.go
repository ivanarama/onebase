package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

func TestCheckReferencePagesCLI(t *testing.T) {
	for _, owner := range []string{"processor", "document"} {
		t.Run(owner, func(t *testing.T) {
			for _, tc := range []struct {
				name, fieldType, attrType, path, page, want string
			}{
				{"parameter", "reference:Клиент", "", "Объект.Клиент", "Переписка", ""},
				{"missing-page", "reference:Клиент", "", "Объект.Клиент", "НетСтраницы", "не найдена"},
				{"non-reference", "string", "", "Объект.Клиент", "Переписка", "только у ссылочного"},
				{"form-attribute", "reference:Клиент", "CatalogRef.Клиент", "Форма.Выбор", "переписка", ""},
				{"attribute-missing-page", "reference:Клиент", "CatalogRef.Клиент", "Форма.Выбор", "НетСтраницы", "не найдена"},
				{"attribute-non-reference", "reference:Клиент", "string", "Форма.Выбор", "Переписка", "только у ссылочного"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					writeProcrunFixture(t, dir, "config/app.yaml", "name: reference-pages-check\nversion: '1.0'\n")
					writeProcrunFixture(t, dir, "catalogs/клиент.yaml", "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n")
					writeProcrunFixture(t, dir, "pages/переписка.yaml", "name: Переписка\ntitle: Переписка\n")
					writeProcrunFixture(t, dir, "src/переписка.page.os", "Процедура ПриФормировании(Страница)\nКонецПроцедуры\n")
					folder, fields := "processors", "params"
					if owner == "document" {
						folder, fields = "documents", "fields"
					}
					writeProcrunFixture(t, dir, folder+"/подбор.yaml", fmt.Sprintf("name: Подбор\n%s:\n  - {name: Клиент, type: %q}\n", fields, tc.fieldType))
					attrs := ""
					if tc.attrType != "" {
						attrs = fmt.Sprintf("attributes:\n  - {name: Выбор, type: %q, save: false}\n", tc.attrType)
					}
					const formPath = "forms/подбор/переход.form.yaml"
					writeProcrunFixture(t, dir, formPath, fmt.Sprintf(`schema: onebase.form/v1
form:
  name: Переход
  kind: object
  entity: Подбор
%selements:
  - kind: ГруппаФормы
    name: Навигация
    children:
      - kind: ПолеВвода
        name: ПолеКлиент
        data_path: %s
        readonly: true
        reference_page: %s
`, attrs, tc.path, tc.page))
					out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
					var result configcheck.Result
					if e := json.Unmarshal([]byte(out), &result); e != nil {
						t.Fatalf("check --json: %v\n%s", e, out)
					}
					if tc.want == "" {
						if err != nil || !result.OK || result.Total != 0 {
							t.Fatalf("valid reference_page rejected: %v\n%s", err, out)
						}
						return
					}
					if !errors.Is(err, errSilentExit) || result.OK || result.Total != 1 || len(result.Issues) != 1 {
						t.Fatalf("invalid reference_page accepted or unrelated error: %v\n%s", err, out)
					}
					issue := result.Issues[0]
					if issue.Code != "form.reference-page" || issue.File != formPath || issue.Object != "Подбор" || !strings.Contains(issue.Message, tc.want) {
						t.Fatalf("wrong reference_page diagnostic: %+v", issue)
					}
				})
			}
		})
	}
}
