package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func readonlyTablePartFixture(t *testing.T) (*Server, *metadata.Entity, metadata.TablePart, uuid.UUID) {
	t.Helper()
	tp := metadata.TablePart{
		Name: "Lines",
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString},
		},
	}
	button := &metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Check",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick: "Noop",
		},
	}
	form := managedObjectForm(
		fieldEl("NameField", "Object.Name"),
		&metadata.FormElement{
			Kind:     metadata.FormElementTablePart,
			Name:     "LinesTable",
			DataPath: "Object.Lines",
			ReadOnly: true,
			NoGrid:   true,
		},
		button,
	)
	form.ProgramAST = mustParse(t, `
Процедура Noop()
КонецПроцедуры
`)
	ent := &metadata.Entity{
		Name:       "ReadOnlyTPRecord",
		Kind:       metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tp},
		Forms:      []*metadata.FormModule{form},
	}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Name": "original"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(ctx, ent.Name, tp.Name, id,
		[]map[string]any{{"Name": "canonical"}}, tp); err != nil {
		t.Fatal(err)
	}
	return srv, ent, tp, id
}

func assertCanonicalReadOnlyRows(t *testing.T, srv *Server, ent *metadata.Entity, tp metadata.TablePart, id uuid.UUID) {
	t.Helper()
	rows, err := srv.store.GetTablePartRows(t.Context(), ent.Name, tp.Name, id, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["Name"] != "canonical" {
		t.Fatalf("readonly table part changed: %#v", rows)
	}
}

func TestSubmitEdit_ReadOnlyNoGridTablePartPreserved(t *testing.T) {
	srv, ent, tp, id := readonlyTablePartFixture(t)

	// Disabled no-grid controls are absent from FormData. Saving that honest
	// browser payload must not interpret the omission as "clear all rows".
	body := url.Values{"Name": {"updated"}}
	req := reqWithChi("POST", "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	srv.submitEdit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertCanonicalReadOnlyRows(t, srv, ent, tp, id)

	// The server must also ignore a forged hidden/grid payload for the same
	// ReadOnly element; readonly is an authorization boundary, not a UI hint.
	body.Set("tp_json.Lines", `[{"Name":"forged"}]`)
	req = reqWithChi("POST", "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	rec = httptest.NewRecorder()
	srv.submitEdit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("forged save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertCanonicalReadOnlyRows(t, srv, ent, tp, id)
}

func TestManagedFormEvent_ReadOnlyNoGridTablePartPreserved(t *testing.T) {
	srv, ent, tp, id := readonlyTablePartFixture(t)
	body := url.Values{
		"_element":      {"Check"},
		"_event":        {string(metadata.FormEventOnClick)},
		"_kind":         {"object"},
		"_id":           {id.String()},
		"Name":          {"original"},
		"tp_json.Lines": {`[{"Name":"forged"}]`},
	}

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("form event failed: %s", resp.Error)
	}
	rows := resp.TableParts[tp.Name]
	if len(rows) != 1 || rows[0]["Name"] != "canonical" {
		t.Fatalf("form event exposed forged readonly rows: %#v", rows)
	}
	assertCanonicalReadOnlyRows(t, srv, ent, tp, id)
}
