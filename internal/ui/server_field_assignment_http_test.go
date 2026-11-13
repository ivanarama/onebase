package ui

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A separate Go caller observes both independent hook results: a caught DSL
// exception may retain its first message, which alone cannot prove the retry's
// provenance state. The same one-shot capsule must fail on the second Save.
func TestServerAssignedFieldFailedSaveMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{Name: "ProofRetry", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Executor", Type: metadata.FieldTypeString}, {Name: "Note", Type: metadata.FieldTypeString}}}
		s := newSubmitTestServerOn(t, db, []*metadata.Entity{entity})
		s.entitySvc = s.newEntityService(nil)
		s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Programs: map[string]*ast.Program{entity.Name: mustParse(t, `Процедура ПриЗаписи()
Если НЕ ServerAssignedField("Executor") Тогда ВызватьИсключение("requires proof"); КонецЕсли;
Если this.Note="reject" Тогда ВызватьИсключение("expected rejection"); КонецЕсли;
КонецПроцедуры`)}})
		ctx := auth.ContextWithOpenAccess(context.Background())
		id := uuid.New()
		if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Executor": "", "Note": "initial"}, entity); err != nil {
			t.Fatal(err)
		}
		obj := &runtime.Object{Type: entity.Name, Kind: entity.Kind, ID: id, Fields: map[string]any{}}
		obj.Set("Executor", "owner")
		obj.MarkServerFieldAssignment("Executor")
		proof := obj.TakeServerFieldAssignments()
		first, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{Entity: entity, ID: id, Fields: map[string]any{"Executor": "owner", "Note": "reject"}, ServerAssignments: proof})
		if err != nil || !strings.Contains(first.DSLError, "expected rejection") {
			t.Fatalf("first: %+v %v", first, err)
		}
		second, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{Entity: entity, ID: id, Fields: map[string]any{"Executor": "owner", "Note": "retry"}, ServerAssignments: proof})
		if err != nil || !strings.Contains(second.DSLError, "requires proof") {
			t.Fatalf("second: %+v %v", second, err)
		}
		row, err := db.GetByID(ctx, entity.Name, id, entity)
		if err != nil || row["_version"] != int64(1) || row["Executor"] != "" || row["Note"] != "initial" {
			t.Fatalf("rollback changed row %v %v", row, err)
		}
		// A failure while opening the scope occurs before the OnWrite edge.
		// It still consumes the attempt; reconnecting cannot borrow its marks.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		version := int64(1)
		writer := &docWriter{s: s, ctxSrc: interpreter.NewStaticCtx(cancelled), entity: entity, obj: &runtime.Object{Type: entity.Name, Kind: entity.Kind, ID: id, Fields: map[string]any{"Executor": "", "Note": "retry"}}, loaded: true, expectedVersion: &version}
		writer.Set("Executor", "owner")
		if err := writer.write(); err == nil {
			t.Fatal("cancelled scope unexpectedly saved")
		}
		writer.ctxSrc = interpreter.NewStaticCtx(ctx)
		if err := writer.write(); err == nil || !strings.Contains(err.Error(), "requires proof") {
			t.Fatalf("failed scope retained witness: %v", err)
		}
		row, err = db.GetByID(ctx, entity.Name, id, entity)
		if err != nil || row["_version"] != int64(1) {
			t.Fatalf("scope retry persisted: %v %v", row, err)
		}
	})
}

func TestServerAssignedFieldFormHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, tc := range []struct {
			name, script string
			binding, ok  bool
			version      int64
		}{
			{"genuine-accept", `Объект.Executor="owner"; Объект.Accepted=ТекущаяДата(); Объект.Записать();`, false, true, 2},
			{"two-field-multi-read", `Объект.Executor="owner"; Объект.ТипЗадачи="kind"; Объект.Note="multi"; Объект.Записать();`, false, true, 2},
			{"binding-self-date-not-proof", `Объект.Записать();`, true, false, 1},
			{"other-field-not-proof", `Объект.Note="changed"; Объект.Записать();`, true, false, 1},
			{"second-save-not-proof", `Объект.Executor="owner"; Объект.Записать(); Объект.Записать();`, false, false, 2},
			{"rejected-save-not-proof", `Объект.Executor="owner"; Объект.Note="reject"; Попытка Объект.Записать(); Исключение КонецПопытки; Объект.Note="retry"; Объект.Записать();`, false, false, 1},
			{"outer-rollback-not-proof", `Объект.Executor="owner"; НачатьТранзакцию(); Объект.Записать(); ОтменитьТранзакцию(); Объект.Записать();`, false, false, 1},
			{"other-object-not-proof", `Объект.Executor="owner"; Другой=Объект.Other.ПолучитьОбъект(); Другой.Записать();`, false, false, 1},
			{"docwriter-genuine", `Другой=Объект.Other.ПолучитьОбъект(); Другой.Executor="owner"; Другой.Записать();`, false, true, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				entity := &metadata.Entity{Name: "TaskProof", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Executor", Type: metadata.FieldTypeString}, {Name: "Note", Type: metadata.FieldTypeString}, {Name: "Accepted", Type: metadata.FieldTypeDate}, {Name: "Other", Type: "reference:TaskProof", RefEntity: "TaskProof"}, {Name: "ТипЗадачи", Type: metadata.FieldTypeString}}}
				form := managedObjectForm(fieldEl("Executor", "Объект.Executor"), fieldEl("Note", "Объект.Note"), fieldEl("Accepted", "Объект.Accepted"), maskEventButton("Accept", "Accept"))
				form.EntityName = entity.Name
				entity.Forms = []*metadata.FormModule{form}
				s := newSubmitTestServerOn(t, db, []*metadata.Entity{entity})
				s.entitySvc = s.newEntityService(nil)
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Programs: map[string]*ast.Program{entity.Name: mustParse(t, `Процедура ПриЗаписи()
Если this.Executor = "owner" И НЕ ПолеНазначеноСервером("Executor") Тогда ВызватьИсключение("executor requires server assignment"); КонецЕсли;
Если ServerAssignedField("DoesNotExist") ИЛИ ServerAssignedField(42) Тогда ВызватьИсключение("invalid field proof"); КонецЕсли;
Если this.Note = "reject" Тогда ВызватьИсключение("expected rejection"); КонецЕсли;
Если this.Note = "multi" Тогда
 Если НЕ ServerAssignedField("Executor") ИЛИ НЕ ПолеНазначеноСервером("ТипЗадачи")
  ИЛИ НЕ ServerAssignedField("ТипЗадачи") ИЛИ НЕ ПолеНазначеноСервером("Executor")
  ИЛИ НЕ ServerAssignedField("Executor") ИЛИ НЕ ServerAssignedField("ТипЗадачи") Тогда
  ВызватьИсключение("getter consumed multi-field witness");
 КонецЕсли;
КонецЕсли;
КонецПроцедуры`)}})
				id, other := uuid.New(), uuid.New()
				for _, row := range []struct {
					id       uuid.UUID
					executor string
				}{{id, ""}, {other, "owner"}} {
					if err := db.Upsert(ctx, entity.Name, row.id, map[string]any{"Executor": row.executor, "Note": "initial"}, entity); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Other": other.String()}, entity); err != nil {
					t.Fatal(err)
				}
				script := tc.script
				outside := `Если ServerAssignedField("Executor") ИЛИ ПолеНазначеноСервером("Executor") Тогда ВызватьИсключение("outside hook proof"); КонецЕсли;`
				form.ProgramAST = mustParse(t, "Процедура Accept()\n"+outside+"\n"+script+"\n"+outside+"\nКонецПроцедуры")
				user := &auth.User{ID: "own", Login: "owner", Roles: []*auth.Role{{Name: "employee", Permissions: auth.Permission{Documents: map[string][]string{entity.Name: {"read", "write"}}}}}}
				router := chi.NewRouter()
				s.Mount(router)
				body := url.Values{"_id": {id.String()}, "_version": {"2"}, "_element": {"Accept"}, "_event": {"Нажатие"}, "Note": {"initial"}, "Executor": {""}}
				if tc.binding {
					body.Set("Executor", "owner")
					body.Set("Accepted", "2026-11-13T10:00:00Z")
					body.Set("ServerAssignedField", "Executor")
					body.Set("_serverAssignedFields", "Executor")
				}
				req := httptest.NewRequest(http.MethodPost, "/ui/document/TaskProof/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if rec.Code != 200 || resp.OK != tc.ok {
					t.Fatalf("HTTP %d %+v want ok=%v", rec.Code, resp, tc.ok)
				}
				wantError := "executor requires server assignment"
				if tc.name == "rejected-save-not-proof" {
					wantError = "expected rejection"
				}
				if !tc.ok && !strings.Contains(resp.Error, wantError) {
					t.Fatalf("wrong refusal: %s", resp.Error)
				}
				row, err := db.GetByID(ctx, entity.Name, id, entity)
				if err != nil {
					t.Fatal(err)
				}
				if row["_version"] != tc.version+1 {
					t.Fatalf("version=%v want %d, row %v", row["_version"], tc.version+1, row)
				}
				if tc.name == "genuine-accept" && (row["Executor"] != "owner" || row["Accepted"] == nil) {
					t.Fatalf("accept not persisted %v", row)
				}
				otherRow, err := db.GetByID(ctx, entity.Name, other, entity)
				if err != nil {
					t.Fatal(err)
				}
				otherVersion := int64(1)
				if tc.name == "docwriter-genuine" {
					otherVersion = 2
				}
				if otherRow["_version"] != otherVersion {
					t.Fatalf("foreign save changed: %v", otherRow)
				}
			})
		}
	})
}
