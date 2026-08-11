package ui

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func readonlyTablePartFixture(t *testing.T, program string) (*Server, *metadata.Entity, metadata.TablePart) {
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
			metadata.FormEventOnClick: "Run",
		},
	}
	form := managedObjectForm(
		fieldEl("NameField", "Object.Name"),
		&metadata.FormElement{
			Kind:     metadata.FormElementTablePart,
			Name:     "LinesTable",
			DataPath: "Object.lines", // deliberately differs from metadata case
			ReadOnly: true,
			NoGrid:   true,
		},
		button,
	)
	form.ProgramAST = mustParse(t, program)
	ent := &metadata.Entity{
		Name:       "ReadOnlyTPRecord",
		Kind:       metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tp},
		Forms:      []*metadata.FormModule{form},
	}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	return srv, ent, tp
}

func renderReadonlyTablePartJSON(t *testing.T, ent *metadata.Entity, rows []map[string]any) string {
	t.Helper()
	data := map[string]any{
		"Entity":        ent,
		"Form":          ent.Forms[0],
		"IsNew":         true,
		"CanWrite":      true,
		"Values":        map[string]string{"Name": "new"},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Lines": rows},
		"Lang":          "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("render managed form: %v", err)
	}
	body := buf.String()
	match := regexp.MustCompile(`name="tp_json\.Lines" value='([^']*)'`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("readonly no_grid did not render canonical hidden tp_json.Lines:\n%s", body)
	}
	if regexp.MustCompile(`name="tp_json\.lines"`).MatchString(body) {
		t.Fatal("table-part form key kept data_path case instead of metadata case")
	}
	return html.UnescapeString(match[1])
}

func assertStoredTablePartName(t *testing.T, srv *Server, ent *metadata.Entity, tp metadata.TablePart, id uuid.UUID, want string) {
	t.Helper()
	rows, err := srv.store.GetTablePartRows(t.Context(), ent.Name, tp.Name, id, tp)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["Name"] != want {
		t.Fatalf("stored table-part rows = %#v, want Name=%q", rows, want)
	}
}

func onlyStoredID(t *testing.T, srv *Server, ent *metadata.Entity) uuid.UUID {
	t.Helper()
	rows, err := srv.store.List(t.Context(), ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored records = %d, want 1", len(rows))
	}
	id, err := uuid.Parse(refValueString(rows[0]["id"]))
	if err != nil {
		t.Fatalf("stored id: %v (row=%#v)", err, rows[0])
	}
	return id
}

func TestReadonlyNoGrid_BasedOnRowsRenderAndSurviveSave(t *testing.T) {
	srv, ent, tp := readonlyTablePartFixture(t, `
Процедура Run()
КонецПроцедуры
`)
	payload := renderReadonlyTablePartJSON(t, ent, []map[string]any{{"Name": "based-on-row"}})
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil || len(decoded) != 1 || decoded[0]["Name"] != "based-on-row" {
		t.Fatalf("hidden JSON = %q, decoded=%#v, err=%v", payload, decoded, err)
	}

	body := url.Values{"Name": {"new"}, "tp_json.Lines": {payload}}
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/new", body,
		map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.submit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertStoredTablePartName(t, srv, ent, tp, onlyStoredID(t, srv, ent), "based-on-row")
}

func TestReadonlyNoGrid_NewEventRowsSurviveSave(t *testing.T) {
	srv, ent, tp := readonlyTablePartFixture(t, `
Процедура Run()
	Row = Объект.Lines.Добавить();
	Row.Name = "event-row";
КонецПроцедуры
`)
	eventBody := url.Values{
		"_element": {"Check"},
		"_event":   {string(metadata.FormEventOnClick)},
		"_kind":    {"object"},
		"Name":     {"new"},
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, eventBody).Body.Bytes())
	if !resp.OK || len(resp.TableParts[tp.Name]) != 1 || resp.TableParts[tp.Name][0]["Name"] != "event-row" {
		t.Fatalf("event response = %#v", resp)
	}
	payload, err := json.Marshal(resp.TableParts[tp.Name])
	if err != nil {
		t.Fatal(err)
	}

	body := url.Values{"Name": {"new"}, "tp_json.Lines": {string(payload)}}
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/new", body,
		map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.submit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertStoredTablePartName(t, srv, ent, tp, onlyStoredID(t, srv, ent), "event-row")
}

func TestReadonlyNoGrid_ExistingEventRowsSurviveSave(t *testing.T) {
	srv, ent, tp := readonlyTablePartFixture(t, `
Процедура Run()
	Объект.Lines.Очистить();
	Row = Объект.Lines.Добавить();
	Row.Name = "event-updated";
КонецПроцедуры
`)
	id := uuid.New()
	if err := srv.store.Upsert(t.Context(), ent.Name, id, map[string]any{"Name": "existing"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(t.Context(), ent.Name, tp.Name, id,
		[]map[string]any{{"Name": "stored-before-event"}}, tp); err != nil {
		t.Fatal(err)
	}
	eventBody := url.Values{
		"_element":      {"Check"},
		"_event":        {string(metadata.FormEventOnClick)},
		"_kind":         {"object"},
		"_id":           {id.String()},
		"Name":          {"existing"},
		"tp_json.Lines": {`[{"Name":"stored-before-event"}]`},
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, eventBody).Body.Bytes())
	if !resp.OK || len(resp.TableParts[tp.Name]) != 1 || resp.TableParts[tp.Name][0]["Name"] != "event-updated" {
		t.Fatalf("event response = %#v", resp)
	}
	payload, err := json.Marshal(resp.TableParts[tp.Name])
	if err != nil {
		t.Fatal(err)
	}

	body := url.Values{"Name": {"existing"}, "tp_json.Lines": {string(payload)}}
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body,
		map[string]string{"entity": ent.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	srv.submitEdit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertStoredTablePartName(t, srv, ent, tp, id, "event-updated")
}

func TestParseTablePartRows_WritableDuplicateWinsReadonlyJSONMirror(t *testing.T) {
	ent := &metadata.Entity{TableParts: []metadata.TablePart{{
		Name: "Lines", Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
	}}}
	body := url.Values{
		"tp_json.Lines":   {`[{"Name":"readonly-stale"}]`},
		"tp.Lines.0.Name": {"editable-current"},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	rows := parseTablePartRows(req, ent)["Lines"]
	if len(rows) != 1 || rows[0]["Name"] != "editable-current" {
		t.Fatalf("duplicate TP parse = %#v", rows)
	}
}

func TestManagedFormEvent_ValueTableNameCollisionDoesNotOverwriteEntityTablePart(t *testing.T) {
	srv, ent, tp := readonlyTablePartFixture(t, `
Процедура Run()
КонецПроцедуры
`)
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{
		Name: "lines", TypeRef: "ValueTable",
		Columns: []*metadata.FormAttributeColumn{{Name: "Note", TypeRef: "string"}},
	}}
	body := url.Values{
		"_element": {"Check"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"},
		"tp_json.Lines":   {`[{"Name":"entity-row"}]`},
		"vt.lines.0.Note": {"form-table-row"},
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	rows := resp.TableParts[tp.Name]
	if !resp.OK || len(rows) != 1 || rows[0]["Name"] != "entity-row" {
		t.Fatalf("ValueTable collision overwrote entity TP: %#v", resp)
	}
}
