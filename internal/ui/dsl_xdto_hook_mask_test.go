package ui

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// The public processor path must carry the user and the writer's field origin
// through Conduct -> OnPost -> This -> XDTO, including XML round-trips.
func TestDSL_XDTOHookFieldPolicyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, policy := range []struct {
			name, want string
			fields     auth.FieldPolicies
		}{
			{"full", "<Телефон>+79161234455</Телефон>", nil},
			{"mask_tail", "<Телефон>••••••••4455</Телефон>", auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}},
			{"hide", "<Телефон/>", auth.FieldPolicies{"Телефон": {Read: "hide"}}},
		} {
			for _, source := range []string{"new", "saved_by_module", "loaded", "assigned_before_post", "assigned_in_hook", "indexed_assignment_in_hook"} {
				t.Run(policy.name+"/"+source, func(t *testing.T) {
					number := policy.name + "_" + source
					hookAssignment := ""
					switch source {
					case "assigned_in_hook":
						hookAssignment = `ЭтотОбъект.Телефон = "+79990001122";`
					case "indexed_assignment_in_hook":
						hookAssignment = `ЭтотОбъект["Телефон"] = "+79990001122";`
					}
					create := fmt.Sprintf(`Док = Документы.Док.Создать(); Док.Номер = "%s"; Док.Дата = Дата(2026, 1, 2, 3, 4, 5);
      Док.Телефон = "+79161234455"; Док.Примечание = "шапка";
      СтрокаТЧ = Док.Товары.Добавить(); СтрокаТЧ.Артикул = "SKU-1"; СтрокаТЧ.Количество = 2;`, number)
					setup := create
					if source == "saved_by_module" {
						setup += "Док.Записать();"
					}
					if source != "new" && source != "saved_by_module" {
						setup = fmt.Sprintf(`Док = Документы.Док.НайтиПоНомеру("%s").ПолучитьОбъект();`, number)
					}
					if source == "assigned_before_post" {
						setup += `Док.Телефон = "+79990001122";`
					}
					dir := t.TempDir()
					files := map[string]string{
						"documents/док.yaml": `name: Док
posting: true
fields:
  - {name: Номер, type: string}
  - {name: Дата, type: date}
  - {name: Телефон, type: string}
  - {name: Примечание, type: string}
tableparts:
  - name: Товары
    fields:
      - {name: Артикул, type: string}
      - {name: Количество, type: number}
`,
						"processors/подготовка.yaml": "name: Подготовка\n",
						"processors/проба.yaml":      "name: Проба\n",
						"src/подготовка.proc.os":     "Процедура Выполнить()\n" + create + "Док.Записать();\nКонецПроцедуры",
						"src/док.posting.os": `Процедура ОбработкаПроведения()
       ` + hookAssignment + `
       Сообщить(СериализаторXDTO.ЗаписатьXML(ЭтотОбъект));
      КонецПроцедуры`,
						"src/проба.proc.os": "Процедура Выполнить()\n" + setup + `
       Док.Провести();
       XML = СериализаторXDTO.ЗаписатьXML(Док);
       Сообщить(XML);
       Сообщить(СериализаторXDTO.ЗаписатьXML(СериализаторXDTO.ПрочитатьXML(XML)));
      КонецПроцедуры`,
					}
					for name, body := range files {
						path := filepath.Join(dir, name)
						if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					proj, err := project.Load(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer proj.Close()
					ctx := context.Background()
					if err := db.Migrate(ctx, proj.Entities); err != nil {
						t.Fatal(err)
					}
					if source != "new" && source != "saved_by_module" {
						msgs, runErr, err := RunProcessorOffline(ctx, proj, db, "Подготовка", nil, nil)
						if err != nil || runErr != nil {
							t.Fatalf("seed: %v / %v / %q", err, runErr, msgs)
						}
					}
					user := uiMaskUser([]string{"read", "write", "post"}, nil)
					user.Roles[0].Permissions.Documents = map[string][]string{"Док": {"read", "write", "post"}}
					user.Roles[0].Permissions.FieldAccess.Documents = map[string]auth.FieldPolicies{"Док": policy.fields}
					messages, runErr, err := RunProcessorOffline(auth.ContextWithUser(ctx, user), proj, db, "Проба", nil, nil)
					if err != nil || runErr != nil {
						t.Fatalf("processor: %v / %v / %q", err, runErr, messages)
					}
					if len(messages) != 3 {
						t.Fatalf("want hook, post and round-trip snapshots: %q", messages)
					}
					if messages[1] != messages[2] {
						t.Fatalf("round-trip changed XML: %q", messages[1:])
					}
					want := policy.want
					if source == "new" || source == "saved_by_module" {
						want = "<Телефон>+79161234455</Телефон>"
					}
					if hookAssignment != "" || (source == "assigned_before_post" && policy.fields == nil) {
						want = "<Телефон>+79990001122</Телефон>"
					}
					var id uuid.UUID
					for i, message := range messages {
						var state struct {
							Ref          string
							Posted       *bool
							DeletionMark *bool
						}
						if err := xml.Unmarshal([]byte(message), &state); err != nil {
							t.Fatal(err)
						}
						ref, err := uuid.Parse(state.Ref)
						if err != nil || ref == uuid.Nil || (i > 0 && ref != id) {
							t.Fatalf("changed Ref: %s", message)
						}
						id = ref
						if state.Posted == nil || *state.Posted != (i > 0) || state.DeletionMark == nil || *state.DeletionMark {
							t.Fatalf("snapshot state: %s", message)
						}
						for _, expected := range []string{want, "<Number>" + number + "</Number>", "<Примечание>шапка</Примечание>", "<Date>2026-01-02T03:04:05</Date>", "<Товары>", "<Артикул>SKU-1</Артикул>", "<Количество>2</Количество>"} {
							if !strings.Contains(message, expected) {
								t.Fatalf("snapshot lost %s: %s", expected, message)
							}
						}
						if (source == "loaded" || source == "assigned_before_post") && policy.fields != nil && (strings.Contains(message, "+79161234455") || strings.Contains(message, "+79990001122")) {
							t.Fatalf("stored protected value leaked: %s", message)
						}
					}
					row, err := db.GetByID(ctx, "Док", id, proj.Entities[0])
					if err != nil {
						t.Fatal(err)
					}
					storedPhone := "+79161234455"
					if hookAssignment != "" || (source == "assigned_before_post" && policy.fields == nil) {
						storedPhone = "+79990001122"
					}
					if row["Телефон"] != storedPhone || !asBool(row["posted"]) {
						t.Fatalf("stored state changed: %v", row)
					}
				})
			}
		}
	})
}
