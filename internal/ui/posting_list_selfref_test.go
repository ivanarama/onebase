package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// List posting must expose the same self reference to OnPost as
// entityservice.Save and the DSL document writer.
func TestPostDocument_ListOnPostHasSelfReference(t *testing.T) {
	ctx, db, s, doc, cat := newSelfRefPostingServer(t)
	id := uuid.New()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  true,
		Fields: map[string]any{"Номер": "P-LIST"},
	}); err != nil {
		t.Fatalf("prepare document: %v", err)
	}

	req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+id.String()+"/post", nil,
		map[string]string{"entity": doc.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("postDocument status = %d, body=%s", rec.Code, rec.Body.String())
	}

	events, err := db.List(ctx, cat.Name, cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("child events = %d, want 1", len(events))
	}
	if got := refValueString(events[0]["Прибор"]); got != id.String() {
		t.Fatalf("OnPost self reference = %q, want %q", got, id.String())
	}
}

func TestPostDocument_ListReservedSelfReferenceBeatsLegacyFields(t *testing.T) {
	ctx, db, s, doc, cat := newSelfRefPostingServer(t)
	doc.Fields = append(doc.Fields,
		metadata.Field{Name: "Ссылка", Type: metadata.FieldTypeString},
		metadata.Field{Name: "reference", Type: metadata.FieldTypeString},
	)
	if err := db.Migrate(ctx, []*metadata.Entity{doc, cat}); err != nil {
		t.Fatal(err)
	}
	program := mustParse(t, `Процедура ОбработкаПроведения()
  СобытиеRU = Справочники.СобытиеПрибора.Создать();
  СобытиеRU.Прибор = ЭтотОбъект.Ссылка;
  СобытиеRU.Записать();
  СобытиеEN = Справочники.СобытиеПрибора.Создать();
  СобытиеEN.Прибор = ЭтотОбъект.reference;
  СобытиеEN.Записать();
КонецПроцедуры`)
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc, cat},
		Programs: map[string]*ast.Program{doc.Name: program},
	})

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{
		"Номер":     "P-LEGACY",
		"Ссылка":    "legacy-cyrillic-field",
		"reference": "legacy-english-field",
	}, doc); err != nil {
		t.Fatalf("prepare legacy document: %v", err)
	}
	req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+id.String()+"/post", nil,
		map[string]string{"entity": doc.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("postDocument status = %d, body=%s", rec.Code, rec.Body.String())
	}

	events, err := db.List(ctx, cat.Name, cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("child events = %d, want 2", len(events))
	}
	for i, event := range events {
		if got := refValueString(event["Прибор"]); got != id.String() {
			t.Fatalf("child %d OnPost self reference = %q, want %q", i, got, id.String())
		}
	}
}
