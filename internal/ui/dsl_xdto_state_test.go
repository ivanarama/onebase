package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func runXDTOStateDSL(t *testing.T, s *Server, ctx context.Context, script string, extra map[string]any) any {
	t.Helper()
	prog, err := parser.New(lexer.New("Функция Проверка()\n"+script+"\nКонецФункции", "xdto-state.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector("test", uuid.Nil))
	for k, v := range extra {
		vars[k] = v
	}
	var result any
	if err := s.interp.RunWithResult(prog.Procedures[0], nil, &result, vars); err != nil {
		t.Fatal(err)
	}
	return result
}

// БД -> штатный менеджер -> ПолучитьОбъект/Прочитать -> XDTO, включая маски
// и XML-round-trip. Одна и та же регрессия работает на обоих диалектах.
func TestDSL_XDTOStoredStateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		cat, doc := dslMaskEntities()
		entities := []*metadata.Entity{cat, doc}
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
		s.entitySvc = s.newEntityService(nil)
		for _, ent := range entities {
			t.Run(string(ent.Kind), func(t *testing.T) {
				id := uuid.New()
				if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Иванов", "Номер": "XDTO-1", "Телефон": "+79161234455"}, ent); err != nil {
					t.Fatal(err)
				}
				setState := func(t *testing.T, flag bool) {
					t.Helper()
					if err := db.MarkForDeletion(ctx, ent.Name, id, false); err != nil {
						t.Fatal(err)
					}
					if ent.Kind == metadata.KindDocument {
						if err := db.SetPosted(ctx, ent.Name, id, flag); err != nil {
							t.Fatal(err)
						}
					}
					if err := db.MarkForDeletion(ctx, ent.Name, id, flag); err != nil {
						t.Fatal(err)
					}
				}
				lookup := fmt.Sprintf(`Справочники.%s.НайтиПоИдентификатору("%s").ПолучитьОбъект()`, ent.Name, id)
				if ent.Kind == metadata.KindDocument {
					lookup = fmt.Sprintf(`Документы.%s.НайтиПоНомеру("XDTO-1").ПолучитьОбъект()`, ent.Name)
				}
				for _, policy := range []struct {
					name, want string
					fields     auth.FieldPolicies
				}{
					{"unmasked", "<Телефон>+79161234455</Телефон>", nil},
					{"mask_tail", "<Телефон>••••••••4455</Телефон>", auth.FieldPolicies{"Телефон": {Read: "mask_tail", Keep: 4}}},
					{"hide", "<Телефон/>", auth.FieldPolicies{"Телефон": {Read: "hide"}}},
				} {
					t.Run(policy.name, func(t *testing.T) {
						user := uiMaskUser([]string{"read", "write"}, policy.fields)
						user.Roles[0].Permissions.Documents = map[string][]string{doc.Name: {"read", "write"}}
						user.Roles[0].Permissions.FieldAccess.Documents = map[string]auth.FieldPolicies{doc.Name: policy.fields}
						userCtx := auth.ContextWithUser(ctx, user)
						for _, first := range []bool{false, true} {
							setState(t, first)
							loaded := runXDTOStateDSL(t, s, userCtx, "Возврат "+lookup+";", nil)
							assertXML := func(script string, flag bool) {
								t.Helper()
								xml := runXDTOStateDSL(t, s, userCtx, script, map[string]any{"Загруженный": loaded}).(string)
								expected := []string{fmt.Sprintf("<DeletionMark>%t</DeletionMark>", flag), "<Ref>" + id.String() + "</Ref>", policy.want}
								if ent.Kind == metadata.KindDocument {
									expected = append(expected, fmt.Sprintf("<Posted>%t</Posted>", flag))
								}
								for _, want := range expected {
									if !strings.Contains(xml, want) {
										t.Fatalf("XML lost %s:\n%s", want, xml)
									}
								}
								if policy.fields != nil && strings.Contains(xml, "+79161234455") {
									t.Fatalf("unmasked value leaked: %s", xml)
								}
								round := runXDTOStateDSL(t, s, userCtx, `Возврат СериализаторXDTO.ЗаписатьXML(СериализаторXDTO.ПрочитатьXML(Вход));`, map[string]any{"Вход": xml}).(string)
								if round != xml {
									t.Fatalf("XML round-trip changed record:\n%s\n%s", xml, round)
								}
							}
							assertXML(`Возврат СериализаторXDTO.ЗаписатьXML(Загруженный);`, first)
							// Присвоенные прикладные ключи не меняют служебное состояние.
							opposite := "Истина"
							if first {
								opposite = "Ложь"
							}
							assertXML(`Загруженный.deletion_mark = `+opposite+`; Загруженный.posted = `+opposite+`; Возврат СериализаторXDTO.ЗаписатьXML(Загруженный);`, first)
							setState(t, !first)
							assertXML(`Загруженный.Телефон = "подменено"; Загруженный.Прочитать(); Возврат СериализаторXDTO.ЗаписатьXML(Загруженный);`, !first)
						}
						newXML := runXDTOStateDSL(t, s, userCtx, `Возврат СериализаторXDTO.ЗаписатьXML(`+strings.Split(lookup, ".Найти")[0]+`.Создать());`, nil).(string)
						if !strings.Contains(newXML, "<DeletionMark>false</DeletionMark>") || (ent.Kind == metadata.KindDocument && !strings.Contains(newXML, "<Posted>false</Posted>")) {
							t.Fatalf("new object flags: %s", newXML)
						}
					})
				}
			})
		}
	})
}
