package ui

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestResultCapabilityHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		parent := &metadata.Entity{Name: "CapabilityParent", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		source := &metadata.Entity{Name: "CapabilitySource", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Пароль", Type: metadata.FieldTypeString}}}
		ir := &metadata.InfoRegister{Name: "CapabilityIndex", Dimensions: []metadata.Field{{Name: "Parent", Type: metadata.FieldType("reference:" + parent.Name), RefEntity: parent.Name}, {Name: "Owner", Type: metadata.FieldTypeString}},
			Resources: []metadata.Field{{Name: "Enabled", Type: metadata.FieldTypeBool}, {Name: "SourceID", Type: metadata.FieldTypeString}, {Name: "SourceVersion", Type: metadata.FieldTypeNumber}, {Name: "Observed", Type: metadata.FieldTypeDate}}}
		if err := db.Migrate(ctx, []*metadata.Entity{parent, source}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		binding := processor.ResultCapability{Register: ir.Name, ReferenceField: "Parent", OwnerField: "Owner", EnabledField: "Enabled", SourceEntity: source.Name, SourceIDField: "SourceID", SourceVersionField: "SourceVersion", ObservedAtField: "Observed", MaxAgeSeconds: 300}
		for _, mode := range []string{"valid", "valid-admin", "open", "answer", "transaction", "false-flag", "wrong-owner", "wrong-parent", "stale-proof", "future-proof", "bad-source-id", "wrong-source-version", "unknown-name", "ordinary", "foreign", "expired-context", "forged-parent", "index-read-denied", "index-rls-denied", "parent-rls-denied", "masked-index", "writable-index", "save-headless", "mark-headless", "unmark-headless", "delete-headless", "save-rollback", "mark-rollback"} {
			t.Run(mode, func(t *testing.T) {
				form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Probe", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Probe", metadata.FormEventOnAnswer: "Probe"}})
				form.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Probe"}
				name := "Note"
				if mode == "unknown-name" {
					name = "Missing"
				}
				form.ProgramAST = mustParse(t, `Процедура Probe()
Если ВозможностьФормыРезультата("`+name+`") И ResultFormCapability("`+name+`") Тогда
Сообщить("enabled");
Иначе
Сообщить("disabled");
КонецЕсли;
КонецПроцедуры`)
				if mode == "transaction" {
					form.ProgramAST = mustParse(t, `Процедура Probe()
НачатьТранзакцию();
Если ResultFormCapability("Note") Тогда Сообщить("enabled"); Иначе Сообщить("disabled"); КонецЕсли;
ЗафиксироватьТранзакцию();
КонецПроцедуры`)
				}
				proc := &processor.Processor{Name: "CapabilityResult", Params: []processor.Param{{Name: "Parent", Type: "reference:" + parent.Name}}, Forms: []*metadata.FormModule{form}, ResultCapabilities: map[string]processor.ResultCapability{"Note": binding}}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{parent, source}, InfoRegs: []*metadata.InfoRegister{ir}})
				reg.LoadProcessors([]*processor.Processor{proc})
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				s := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), ops: newOperationLimiter()}
				s.entitySvc = s.newEntityService(nil)
				permissions := auth.Permission{Catalogs: map[string][]string{parent.Name: {"read", "write"}}, InfoRegs: map[string][]string{ir.Name: {"read"}}, Processors: map[string][]string{proc.Name: {"run"}}, RowAccess: auth.RowAccess{InfoRegs: map[string]auth.RowPolicies{ir.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "id"}}}}}}
				actor := &auth.User{ID: uuid.NewString(), Login: "own-synthetic", Roles: []*auth.Role{{Name: "ReadonlyIndex", Permissions: permissions}}}
				if mode == "valid-admin" {
					actor.IsAdmin = true
				}
				parentID, sourceID := uuid.New(), uuid.New()
				if err := db.Upsert(ctx, parent.Name, parentID, map[string]any{"Наименование": "own parent"}, parent); err != nil {
					t.Fatal(err)
				}
				if err := db.Upsert(ctx, source.Name, sourceID, map[string]any{"Наименование": "own source", "Пароль": "DO-NOT-RETURN"}, source); err != nil {
					t.Fatal(err)
				}
				version, err := db.EntityVersion(ctx, source.Name, sourceID)
				if err != nil {
					t.Fatal(err)
				}
				dims := map[string]any{"Parent": parentID.String(), "Owner": actor.ID}
				proof := map[string]any{"Enabled": true, "SourceID": sourceID.String(), "SourceVersion": version, "Observed": time.Now().Add(-time.Second)}
				switch mode {
				case "false-flag":
					proof["Enabled"] = false
				case "wrong-owner":
					dims["Owner"] = uuid.NewString()
				case "wrong-parent":
					dims["Parent"] = uuid.NewString()
				case "stale-proof":
					proof["Observed"] = time.Now().Add(-301 * time.Second)
				case "future-proof":
					proof["Observed"] = time.Now().Add(time.Minute)
				case "bad-source-id":
					proof["SourceID"] = "not-a-uuid"
				case "wrong-source-version":
					proof["SourceVersion"] = version + 1
				case "index-read-denied":
					permissions.InfoRegs = map[string][]string{}
				case "index-rls-denied":
					permissions.RowAccess.InfoRegs[ir.Name]["read"] = auth.RowPolicy{Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "other"}}
				case "parent-rls-denied":
					permissions.RowAccess.Catalogs = map[string]auth.RowPolicies{parent.Name: {"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "different"}}}}
				case "masked-index":
					permissions.FieldAccess = auth.FieldAccess{InfoRegs: map[string]auth.FieldPolicies{ir.Name: {"Enabled": {Read: "hide"}}}}
				case "writable-index":
					permissions.InfoRegs[ir.Name] = []string{"read", "write"}
				}
				actor.Roles[0].Permissions = permissions
				if err := db.InfoRegSet(ctx, ir, dims, proof, nil); err != nil {
					t.Fatal(err)
				}
				mutation := func(tx context.Context) error {
					switch mode {
					case "save-headless", "save-rollback":
						return db.Upsert(tx, source.Name, sourceID, map[string]any{"Наименование": "changed source"}, source)
					case "mark-headless", "mark-rollback":
						return db.MarkForDeletion(tx, source.Name, sourceID, true)
					case "unmark-headless":
						if err := db.MarkForDeletion(tx, source.Name, sourceID, true); err != nil {
							return err
						}
						return db.MarkForDeletion(tx, source.Name, sourceID, false)
					case "delete-headless":
						return db.Delete(tx, source.Name, sourceID)
					}
					return nil
				}
				if strings.HasSuffix(mode, "-rollback") {
					rollback := errors.New("own rollback")
					if err := db.WithTxScope(ctx, func(tx context.Context) error {
						if err := mutation(tx); err != nil {
							return err
						}
						return rollback
					}); !errors.Is(err, rollback) {
						t.Fatal(err)
					}
				} else if err := mutation(ctx); err != nil {
					t.Fatal(err)
				}
				payload := &resultFormPayload{Token: uuid.NewString()}
				ownerRequest := httptest.NewRequest("POST", "/", nil)
				ownerRequest = ownerRequest.WithContext(auth.ContextWithUser(ctx, actor))
				s.resultForms = map[string]resultFormContext{payload.Token: {owner: resultFormOwner(ownerRequest), expires: time.Now().Add(time.Hour), request: resultFormRequest{processor: proc.Name, parameter: "Parent", reference: interpreter.Ref{Kind: parent.Kind, Type: parent.Name, UUID: parentID.String()}}}}
				body := url.Values{"_element": {"Probe"}, "_event": {"Нажатие"}}
				switch mode {
				case "open":
					body.Set("_element", "")
					body.Set("_event", "ПриОткрытии")
				case "answer":
					body.Set("_event", "Ответ")
					body.Set("_question_answer", "Да")
				case "ordinary":
					payload.Token = ""
				case "foreign":
					clone := *actor
					clone.ID = uuid.NewString()
					actor = &clone
				case "expired-context":
					c := s.resultForms[payload.Token]
					c.expires = time.Now().Add(-time.Second)
					s.resultForms[payload.Token] = c
				case "forged-parent":
					body.Set("Parent", uuid.NewString())
				}
				response := resultContextEvent(t, s, proc, payload, body, actor)
				want := mode == "valid" || mode == "valid-admin" || mode == "open" || mode == "answer" || mode == "transaction" || strings.HasSuffix(mode, "-rollback")
				got := response.OK && len(response.Messages) == 1 && response.Messages[0] == "enabled"
				if got != want {
					t.Fatalf("capability=%v want%v, response=%+v", got, want, response)
				}
				if !want && response.OK && (len(response.Messages) != 1 || response.Messages[0] != "disabled") {
					t.Fatalf("unexpected refusal: %+v", response)
				}
				if response.Navigation != nil || response.CloseResultForm || response.ResultForm != nil {
					t.Fatalf("UI side effect: %+v", response)
				}
				for _, message := range response.Messages {
					if strings.Contains(message, sourceID.String()) || strings.Contains(message, "DO-NOT-RETURN") {
						t.Fatal("source disclosure")
					}
				}
				stored, err := db.InfoRegGetExact(ctx, ir, dims, nil)
				if err != nil || stored == nil {
					t.Fatalf("index unexpectedly changed: %v", err)
				}
				if resultCapabilityTrue(stored["Enabled"]) != resultCapabilityTrue(proof["Enabled"]) {
					t.Fatal("readonly reader mutated cache")
				}
			})
		}
	})
}
