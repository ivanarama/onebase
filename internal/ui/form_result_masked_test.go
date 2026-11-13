package ui

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Public form-event flow: a saved protected value returns to DSL as its mask,
// while the result handoff must still reject actual unsaved mutations.
func TestResultFormProtectedSavedView(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, policy := range []string{"mask_all", "hide"} {
			for _, scenario := range []string{"clean", "edited-visible", "edited-protected", "not-written", "navigate-clean", "navigate-edited"} {
				t.Run(policy+"/"+scenario, func(t *testing.T) {
					s, entity, proc := resultTestServer(t)
					s.store = db
					entity.Fields = append(entity.Fields, metadata.Field{Name: "Note", Type: metadata.FieldTypeString})
					if err := s.store.Migrate(context.Background(), []*metadata.Entity{entity}); err != nil {
						t.Fatal(err)
					}
					form := entity.Forms[0]
					form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementField, Name: "Note", DataPath: "Объект.Note"})
					body := "Объект.Записать();"
					switch scenario {
					case "edited-visible":
						body += `Объект.Note = "unsaved-visible";`
					case "edited-protected":
						body += `Объект.Наименование = "unsaved-protected";`
					case "not-written":
						body = ""
					}
					command := body + `ОткрытьФормуРезультата("Результат", Объект.Ссылка, "Документ");`
					if strings.HasPrefix(scenario, "navigate-") {
						command = `ОткрытьФорму(Объект.Ссылка);`
						if scenario == "navigate-edited" {
							command = `Объект.Note = "unsaved-visible";` + command
						}
					}
					form.ProgramAST = mustParse(t, "Процедура Завершить()\n"+command+"\nКонецПроцедуры")
					user := &auth.User{ID: "masked-owner", Login: "masked-owner", Roles: []*auth.Role{{Permissions: auth.Permission{
						Catalogs: map[string][]string{entity.Name: {"read", "write"}}, Processors: map[string][]string{proc.Name: {"run"}},
						FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{entity.Name: {"Наименование": {Read: policy}}}},
					}}}}
					id := uuid.New()
					const secret = "SYNTH-PHONE-1234"
					if err := s.store.Upsert(context.Background(), entity.Name, id, map[string]any{"Наименование": secret, "Note": "saved"}, entity); err != nil {
						t.Fatal(err)
					}
					values := url.Values{"_event": {"Нажатие"}, "_element": {"Завершить"}, "_id": {id.String()}, "Note": {"saved"}}
					if policy != "hide" {
						values.Set("Наименование", "••••••")
					}
					for attempt := 0; attempt < 2; attempt++ {
						response := resultSourceEvent(t, s, entity, values, user)
						if strings.HasPrefix(scenario, "navigate-") {
							if !response.OK || response.Navigation == nil || response.Dirty == nil || *response.Dirty != (scenario == "navigate-edited") {
								t.Fatalf("navigation dirty proof: %+v", response)
							}
						} else if scenario == "clean" {
							if !response.OK || response.ResultForm == nil || response.Dirty == nil || *response.Dirty {
								t.Fatalf("saved view refused attempt %d: %+v", attempt, response)
							}
						} else if response.OK || response.ResultForm != nil {
							t.Fatalf("unsaved state passed guard: %+v", response)
						}
						if strings.Contains(strings.Join(response.Messages, " "), secret) {
							t.Fatal("response disclosed protected value")
						}
						stored, err := s.store.GetByID(context.Background(), entity.Name, id, entity)
						if err != nil {
							t.Fatal(err)
						}
						if stored["Наименование"] != secret || stored["Note"] != "saved" {
							t.Fatalf("protected/unsaved values persisted: %v", stored)
						}
					}
				})
			}
		}
	})
}
