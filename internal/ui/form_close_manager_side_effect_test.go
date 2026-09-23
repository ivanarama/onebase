package ui

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedFormCloseIntentManagerMarkForDeletionPublishesCanonicalState(t *testing.T) {
	document := &metadata.Entity{
		Name: "CloseManagerMark", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	document.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: document.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Number", DataPath: "Объект.Номер"}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Документы.CloseManagerMark.ПометитьНаУдаление(Объект.Ссылка);
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, document.Name, id, map[string]any{"Номер": "D-1"}, document); err != nil {
		t.Fatal(err)
	}

	body := closeIntentBody(uuid.NewString(), "close", "")
	body.Del("Наименование")
	body.Set("Номер", "D-1")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	recorder := executeFormCloseIntent(t, srv, document, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !response.OK || response.Close == nil ||
		response.Close.Allowed || !response.Close.Saved || response.SavedID != id.String() || response.Version != 2 {
		t.Fatalf("manager mark did not publish exact durable outcome: status=%d response=%+v", recorder.Code, response)
	}
	if marked, present := response.Values["deletion_mark"]; !present || !decodedCloseResponseBool(marked) {
		t.Fatalf("response did not publish deletion_mark=true delta: values=%v", response.Values)
	}
	row, err := srv.store.GetByID(ctx, document.Name, id, document)
	if err != nil || !asBool(row["deletion_mark"]) || persistedEntityVersion(row) != response.Version {
		t.Fatalf("manager mark durable state differs from response: row=%v response=%+v err=%v", row, response, err)
	}
	version, err := srv.store.EntityVersion(ctx, document.Name, id)
	if err != nil || version != response.Version {
		t.Fatalf("manager mark published a non-canonical version: durable=%d response=%d err=%v", version, response.Version, err)
	}
}

func TestManagedFormCloseIntentManagerPostPublishesCanonicalState(t *testing.T) {
	document := &metadata.Entity{
		Name: "CloseManagerPost", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	document.Forms = []*metadata.FormModule{{
		Name: "Object", Kind: "object", EntityName: document.Name, LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Number", DataPath: "Объект.Номер"}},
		Handlers: map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"},
		ProgramAST: mustParse(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Документы.CloseManagerPost.Провести(Объект.Ссылка);
	Отказ = Истина;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{document})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, document.Name, id, map[string]any{"Номер": "D-1"}, document); err != nil {
		t.Fatal(err)
	}

	body := closeIntentBody(uuid.NewString(), "close", "")
	body.Del("Наименование")
	body.Set("Номер", "D-1")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	recorder := executeFormCloseIntent(t, srv, document, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusOK || !response.OK || response.Close == nil ||
		response.Close.Allowed || !response.Close.Saved || response.SavedID != id.String() || response.Version != 2 {
		t.Fatalf("manager post did not publish exact durable outcome: status=%d response=%+v", recorder.Code, response)
	}
	if posted, present := response.Values["posted"]; !present || !decodedCloseResponseBool(posted) {
		t.Fatalf("response did not publish posted=true delta: values=%v", response.Values)
	}
	row, err := srv.store.GetByID(ctx, document.Name, id, document)
	if err != nil || !asBool(row["posted"]) || persistedEntityVersion(row) != response.Version {
		t.Fatalf("manager post durable state differs from response: row=%v response=%+v err=%v", row, response, err)
	}
	version, err := srv.store.EntityVersion(ctx, document.Name, id)
	if err != nil || version != response.Version {
		t.Fatalf("manager post published a non-canonical version: durable=%d response=%d err=%v", version, response.Version, err)
	}
}

func decodedCloseResponseBool(value any) bool {
	if number, ok := value.(float64); ok {
		return number != 0
	}
	return asBool(value)
}
