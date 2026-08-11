package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"golang.org/x/net/html"
)

func duplicatePayloadEntity(writableNoGrid, writableFirst bool) (*metadata.Entity, *metadata.FormModule, *metadata.FormElement) {
	tablePart := metadata.TablePart{
		Name: "Lines", Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
	}
	writable := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "WritableLines", DataPath: "Object.lInEs", NoGrid: writableNoGrid,
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Run"},
	}
	readonly := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ReadonlyLines", DataPath: "Object.Lines",
		ReadOnly: true, NoGrid: !writableNoGrid,
	}
	placements := []*metadata.FormElement{readonly, writable}
	if writableFirst {
		placements = []*metadata.FormElement{writable, readonly}
	}
	button := &metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Check",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Run"},
	}
	elements := []*metadata.FormElement{fieldEl("NameField", "Object.Name")}
	elements = append(elements, placements...)
	elements = append(elements, button)
	form := managedObjectForm(elements...)
	entity := &metadata.Entity{
		Name: "MixedTPRecord", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tablePart}, Forms: []*metadata.FormModule{form},
	}
	return entity, form, writable
}

func sameSourceDuplicatePayloadEntity(writableNoGrid, writableFirst bool) (*metadata.Entity, *metadata.FormModule) {
	entity, form, _ := duplicatePayloadEntity(writableNoGrid, writableFirst)
	for _, element := range form.Elements {
		if element.Kind == metadata.FormElementTablePart && element.ReadOnly {
			element.NoGrid = writableNoGrid
		}
	}
	return entity, form
}

func parsedPayloadRequest(t *testing.T, body url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return req
}

func duplicatePayloadBody(writableNoGrid bool) (url.Values, string) {
	named, jsonValue, want := "readonly-stale", "slick-current", "slick-current"
	if writableNoGrid {
		named, jsonValue, want = "dom-current", "readonly-stale", "dom-current"
	}
	return url.Values{
		"Name":                 {"record"},
		"tp.Lines.0.Name":      {named},
		"tp_json.Lines":        {`[{"Name":"` + jsonValue + `"}]`},
		"tp.lines.0.Ignored":   {"wrong-case-namespace-does-not-promote"},
		"tp_json.Unrendered":   {`[{"Name":"forged"}]`},
		"tp.Unrendered.0.Name": {"forged"},
	}, want
}

func TestManagedTablePartPayloadSourceFollowsSoleWritablePlacement(t *testing.T) {
	for _, writableNoGrid := range []bool{false, true} {
		for _, writableFirst := range []bool{false, true} {
			name := "slick"
			if writableNoGrid {
				name = "no_grid"
			}
			if writableFirst {
				name += "/writable_first"
			} else {
				name += "/readonly_first"
			}
			t.Run(name, func(t *testing.T) {
				entity, form, _ := duplicatePayloadEntity(writableNoGrid, writableFirst)
				body, want := duplicatePayloadBody(writableNoGrid)
				rows, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true)
				if err != nil {
					t.Fatal(err)
				}
				if got := rows["Lines"]; len(got) != 1 || got[0]["Name"] != want {
					t.Fatalf("selected rows = %#v, want Name=%q", got, want)
				}

				inactiveOnly := url.Values{}
				if writableNoGrid {
					inactiveOnly.Set("tp_json.Lines", `[{"Name":"forged"}]`)
				} else {
					inactiveOnly.Set("tp.Lines.0.Name", "forged")
				}
				rows, err = parseTablePartRowsForManagedForm(parsedPayloadRequest(t, inactiveOnly), entity, form, true)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows["Lines"]) != 0 {
					t.Fatalf("inactive browser channel was trusted: %#v", rows["Lines"])
				}
			})
		}
	}
}

func TestManagedSameSourceReadonlyDuplicateLeavesOneSuccessfulPayload(t *testing.T) {
	for _, noGrid := range []bool{false, true} {
		for _, writableFirst := range []bool{false, true} {
			name := "slick"
			if noGrid {
				name = "no_grid"
			}
			if writableFirst {
				name += "/writable_first"
			} else {
				name += "/readonly_first"
			}
			t.Run(name, func(t *testing.T) {
				entity, form := sameSourceDuplicatePayloadEntity(noGrid, writableFirst)
				body := url.Values{}
				if noGrid {
					body.Set("tp.Lines.0.Name", "body-current")
				} else {
					body.Set("tp_json.Lines", `[{"Name":"body-current"}]`)
				}
				rows, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true)
				if err != nil {
					t.Fatal(err)
				}
				if got := rows["Lines"]; len(got) != 1 || got[0]["Name"] != "body-current" {
					t.Fatalf("single writable payload was not accepted: %#v", got)
				}

				ctx := map[string]any{
					"Entity": entity, "Form": form, "CanWrite": true,
					"TablePartRows": map[string][]map[string]any{"Lines": {{"Name": "display-row"}}},
					"TPRefOptions":  map[string]any{},
					"TPEnumLabels":  map[string]map[string]map[string]string{},
				}
				var rendered bytes.Buffer
				for _, element := range form.Elements {
					if element.Kind != metadata.FormElementTablePart {
						continue
					}
					if err := tmpl.ExecuteTemplate(&rendered, "managed-element", map[string]any{"El": element, "Ctx": ctx}); err != nil {
						t.Fatalf("render duplicate placement: %v", err)
					}
				}
				doc, err := html.Parse(strings.NewReader(rendered.String()))
				if err != nil {
					t.Fatal(err)
				}
				var named, successful, readonlyHosts, writableHosts int
				var walk func(*html.Node)
				walk = func(node *html.Node) {
					if node.Type == html.ElementNode {
						payloadName := "tp_json.Lines"
						if noGrid {
							payloadName = "tp.Lines.0.Name"
						}
						if value, ok := keyboardHTMLAttr(node, "name"); ok && value == payloadName {
							named++
							if _, disabled := keyboardHTMLAttr(node, "disabled"); !disabled {
								successful++
							}
							if value, _ := keyboardHTMLAttr(node, "value"); noGrid && value != "display-row" {
								t.Errorf("readonly display value lost: %q", value)
							}
						}
						if tpName, ok := keyboardHTMLAttr(node, "data-sg-tp"); ok && tpName == "Lines" {
							if rowsJSON, _ := keyboardHTMLAttr(node, "data-sg-rows"); !strings.Contains(rowsJSON, "display-row") {
								t.Errorf("Slick duplicate lost display rows: %q", rowsJSON)
							}
							if _, readonly := keyboardHTMLAttr(node, "data-sg-ro"); readonly {
								readonlyHosts++
							} else {
								writableHosts++
							}
						}
					}
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						walk(child)
					}
				}
				walk(doc)
				if successful != 1 {
					t.Fatalf("successful canonical controls = %d, all named controls = %d, html=%s", successful, named, rendered.String())
				}
				if noGrid {
					if named != 2 {
						t.Fatalf("NoGrid display controls = %d, want readonly+writable", named)
					}
				} else if named != 1 || readonlyHosts != 1 || writableHosts != 1 {
					t.Fatalf("Slick controls/hosts = named:%d readonly:%d writable:%d", named, readonlyHosts, writableHosts)
				}
			})
		}
	}
}

func TestManagedTablePayloadUsesPostBodyOnly(t *testing.T) {
	t.Run("save query only", func(t *testing.T) {
		entity, _, _ := duplicatePayloadEntity(false, false)
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
		query := url.Values{"tp_json.Lines": {`[{"Name":"query-forged"}]`}}
		req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new?"+query.Encode(), url.Values{"Name": {"record"}},
			map[string]string{"entity": entity.Name})
		rec := httptest.NewRecorder()
		srv.submit(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows, err := srv.store.GetTablePartRows(t.Context(), entity.Name, "Lines", onlyStoredID(t, srv, entity), entity.TableParts[0])
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("query-only table payload was stored: %#v", rows)
		}
	})

	t.Run("save body wins", func(t *testing.T) {
		entity, _, _ := duplicatePayloadEntity(false, false)
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
		query := url.Values{"tp_json.Lines": {`[{"Name":"query-forged"}]`}}
		body := url.Values{"Name": {"record"}, "tp_json.Lines": {`[{"Name":"body-current"}]`}}
		req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new?"+query.Encode(), body,
			map[string]string{"entity": entity.Name})
		rec := httptest.NewRecorder()
		srv.submit(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertStoredTablePartName(t, srv, entity, entity.TableParts[0], onlyStoredID(t, srv, entity), "body-current")
	})

	for _, tc := range []struct {
		name string
		body url.Values
		want string
	}{
		{name: "event query only", body: url.Values{}, want: ""},
		{name: "event body wins", body: url.Values{"tp_json.Lines": {`[{"Name":"body-current"}]`}}, want: "body-current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entity, form, _ := duplicatePayloadEntity(false, false)
			form.ProgramAST = mustParse(t, "Процедура Run()\nКонецПроцедуры")
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
			tc.body.Set("_element", "Check")
			tc.body.Set("_event", string(metadata.FormEventOnClick))
			tc.body.Set("_kind", "object")
			query := "?" + url.Values{"tp_json.Lines": {`[{"Name":"query-forged"}]`}}.Encode()
			resp := decodeFormEventResponse(t, executeFormEventWithQuery(t, srv, entity, query, tc.body).Body.Bytes())
			if !resp.OK || resp.Error != "" {
				t.Fatalf("event rejected: %#v", resp)
			}
			got := resp.TableParts["Lines"]
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("query-only event table payload was trusted: %#v", got)
				}
			} else if len(got) != 1 || got[0]["Name"] != tc.want {
				t.Fatalf("event rows = %#v, want body value %q", got, tc.want)
			}
		})
	}
}

func TestManagedTablePayloadRejectsMalformedAndDuplicateCanonicalInputs(t *testing.T) {
	entity, form, _ := duplicatePayloadEntity(false, false)
	valid := `[{"Name":"ok"}]`
	for _, tc := range []struct {
		name string
		body url.Values
	}{
		{name: "empty JSON", body: url.Values{"tp_json.Lines": {""}}},
		{name: "malformed JSON", body: url.Values{"tp_json.Lines": {`[{"Name":`}}},
		{name: "non array JSON", body: url.Values{"tp_json.Lines": {`{"Name":"x"}`}}},
		{name: "non object row", body: url.Values{"tp_json.Lines": {`[1]`}}},
		{name: "trailing JSON", body: url.Values{"tp_json.Lines": {valid + ` true`}}},
		{name: "duplicate exact value", body: url.Values{"tp_json.Lines": {valid, valid}}},
		{name: "duplicate case folded key", body: url.Values{"tp_json.Lines": {valid}, "TP_JSON.lines": {valid}}},
		{name: "duplicate row field", body: url.Values{"tp_json.Lines": {`[{"Name":"a","name":"b"}]`}}},
		{name: "duplicate unicode folded row field", body: url.Values{"tp_json.Lines": {`[{"K":"a","K":"b"}]`}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, tc.body), entity, form, true); err == nil {
				t.Fatal("invalid authoritative JSON payload was accepted")
			}
		})
	}

	entity, form = sameSourceDuplicatePayloadEntity(true, false)
	for _, body := range []url.Values{
		{"tp.Lines.0.Name": {"a", "b"}},
		{"tp.Lines.0.Name": {"a"}, "TP.lines.00.name": {"b"}},
	} {
		if _, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true); err == nil {
			t.Fatalf("duplicate canonical named payload was accepted: %#v", body)
		}
	}

	for _, tc := range []struct {
		name string
		body url.Values
	}{
		{name: "JSON metadata case", body: url.Values{"TP_JSON.lInEs": {`[{"nAmE":"case-json"}]`}}},
		{name: "named metadata case", body: url.Values{"TP.lInEs.00.nAmE": {"case-named"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useNoGrid := strings.Contains(tc.name, "named")
			caseEntity, caseForm := sameSourceDuplicatePayloadEntity(useNoGrid, false)
			rows, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, tc.body), caseEntity, caseForm, true)
			if err != nil {
				t.Fatal(err)
			}
			want := "case-json"
			if useNoGrid {
				want = "case-named"
			}
			if got := rows["Lines"]; len(got) != 1 || got[0]["Name"] != want {
				t.Fatalf("case-insensitive metadata was not canonicalized: %#v", got)
			}
		})
	}
}

func managedFormJSONWithUniqueKeys(count int) string {
	var payload strings.Builder
	payload.Grow(count * 14)
	payload.WriteString("[{")
	for i := 0; i < count; i++ {
		if i > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`"field`)
		payload.WriteString(strconv.Itoa(i))
		payload.WriteString(`":0`)
	}
	payload.WriteString("}]")
	return payload.String()
}

func TestManagedFormFoldKeyMatchesEqualFold(t *testing.T) {
	for _, pair := range [][2]string{
		{"Name", "name"},
		{"K", "K"},
		{"S", "ſ"},
		{"Σ", "ς"},
		{"Straße", "STRASSE"},
	} {
		got := managedFormFoldKey(pair[0]) == managedFormFoldKey(pair[1])
		if want := strings.EqualFold(pair[0], pair[1]); got != want {
			t.Errorf("fold equivalence for %q and %q = %v, want %v", pair[0], pair[1], got, want)
		}
	}
}

func TestDecodeManagedFormJSONRowsManyUniqueKeys(t *testing.T) {
	const keyCount = 25_000
	rows, err := decodeManagedFormJSONRows(managedFormJSONWithUniqueKeys(keyCount), nil)
	if err != nil {
		t.Fatalf("decode %d unique keys: %v", keyCount, err)
	}
	if len(rows) != 1 || len(rows[0]) != 0 {
		t.Fatalf("unknown unique keys produced rows %#v", rows)
	}
}

func BenchmarkDecodeManagedFormJSONRowsManyUniqueKeys(b *testing.B) {
	payload := managedFormJSONWithUniqueKeys(25_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decodeManagedFormJSONRows(payload, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestManagedTablePayloadErrorsReachSaveAndEvent(t *testing.T) {
	entity, form, _ := duplicatePayloadEntity(false, false)
	form.ProgramAST = mustParse(t, "Процедура Run()\nКонецПроцедуры")
	bad := url.Values{"Name": {"record"}, "tp_json.Lines": {`[{"Name":"one"}]`, `[{"Name":"two"}]`}}

	saveServer, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	saveReq := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new", bad,
		map[string]string{"entity": entity.Name})
	saveRec := httptest.NewRecorder()
	saveServer.submit(saveRec, saveReq)
	if saveRec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate JSON save status=%d body=%s", saveRec.Code, saveRec.Body.String())
	}

	eventServer, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	bad.Set("_element", "Check")
	bad.Set("_event", string(metadata.FormEventOnClick))
	bad.Set("_kind", "object")
	resp := decodeFormEventResponse(t, executeFormEvent(t, eventServer, entity, bad).Body.Bytes())
	if resp.OK || resp.Error == "" {
		t.Fatalf("duplicate JSON event payload was accepted: %#v", resp)
	}
}

func TestManagedSaveHooksReceiveWritableValueTableBody(t *testing.T) {
	newEntity := func(t *testing.T) *metadata.Entity {
		t.Helper()
		form := managedObjectForm(
			fieldEl("NameField", "Object.Name"),
			&metadata.FormElement{
				Kind: metadata.FormElementTablePart, Name: "ScratchTable", DataPath: "Form.Scratch", NoGrid: true,
			},
		)
		form.Attributes = []*metadata.FormAttribute{{
			Name: "Scratch", TypeRef: "ValueTable",
			Columns: []*metadata.FormAttributeColumn{{Name: "Note", TypeRef: "string"}},
		}}
		form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeWrite: "CheckScratch"}
		form.ProgramAST = mustParse(t, `
Процедура CheckScratch()
	Если Объект.Scratch.Количество() <> 1 Тогда
		ВызватьИсключение("ValueTable body missing");
	КонецЕсли;
КонецПроцедуры`)
		return &metadata.Entity{
			Name: "ValueTableSaveHook", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
			Forms:  []*metadata.FormModule{form},
		}
	}

	for _, edit := range []bool{false, true} {
		name := "new"
		if edit {
			name = "edit"
		}
		t.Run(name, func(t *testing.T) {
			entity := newEntity(t)
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
			body := url.Values{"Name": {"record"}, "vt.Scratch.0.Note": {"body-current"}}
			target := "/ui/catalog/" + entity.Name + "/new"
			params := map[string]string{"entity": entity.Name}
			rec := httptest.NewRecorder()
			if edit {
				id := uuid.New()
				if err := srv.store.Upsert(t.Context(), entity.Name, id, map[string]any{"Name": "before"}, entity); err != nil {
					t.Fatal(err)
				}
				target = "/ui/catalog/" + entity.Name + "/" + id.String()
				params["id"] = id.String()
				srv.submitEdit(rec, reqWithChi(http.MethodPost, target, body, params))
			} else {
				srv.submit(rec, reqWithChi(http.MethodPost, target, body, params))
			}
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("save hook did not receive ValueTable body: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("query only is ignored", func(t *testing.T) {
		entity := newEntity(t)
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
		query := url.Values{"vt.Scratch.0.Note": {"query-forged"}}
		req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new?"+query.Encode(),
			url.Values{"Name": {"record"}}, map[string]string{"entity": entity.Name})
		rec := httptest.NewRecorder()
		srv.submit(rec, req)
		if rec.Code == http.StatusSeeOther {
			t.Fatal("query-only ValueTable reached ПередЗаписью and allowed save")
		}
	})
}

func TestManagedTablePartPayloadSourceFailsClosed(t *testing.T) {
	entity, form, _ := duplicatePayloadEntity(false, false)
	for _, element := range form.Elements {
		if element.Kind == metadata.FormElementTablePart {
			element.ReadOnly = true
		}
	}
	body := url.Values{
		"tp.Lines.0.Name": {"named-forged"},
		"tp_json.Lines":   {`[{"Name":"json-forged"}]`},
	}
	rows, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["Lines"]) != 0 {
		t.Fatalf("readonly-only browser state was trusted: %#v", rows["Lines"])
	}

	entity, form, _ = duplicatePayloadEntity(false, false)
	rows, err = parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["Lines"]) != 0 {
		t.Fatalf("CanWrite=false browser state was trusted: %#v", rows["Lines"])
	}

	for _, noGrid := range []bool{false, true} {
		entity, form, _ = duplicatePayloadEntity(noGrid, false)
		for _, element := range form.Elements {
			if element.Kind == metadata.FormElementTablePart {
				element.ReadOnly = false
				element.NoGrid = noGrid
			}
		}
		if _, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true); err == nil ||
			!strings.Contains(strings.ToLower(err.Error()), "неоднознач") {
			t.Fatalf("duplicate writable placement (NoGrid=%v) was accepted: %v", noGrid, err)
		}
	}

	entity, form, _ = duplicatePayloadEntity(false, false)
	for _, element := range form.Elements {
		if element.Kind == metadata.FormElementTablePart {
			element.ReadOnly = false
		}
	}
	if _, err := parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, true); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "неоднознач") {
		t.Fatalf("mixed writable NoGrid/Slick metadata was accepted: %v", err)
	}
	rows, err = parseTablePartRowsForManagedForm(parsedPayloadRequest(t, body), entity, form, false)
	if err != nil || len(rows["Lines"]) != 0 {
		t.Fatalf("CanWrite=false must yield no authority even for duplicate metadata: rows=%#v err=%v", rows, err)
	}
	form.ProgramAST = mustParse(t, "Процедура Run()\nСообщить(\"must not run\");\nКонецПроцедуры")
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new", body,
		map[string]string{"entity": entity.Name})
	rec := httptest.NewRecorder()
	srv.submit(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "неоднознач") {
		t.Fatalf("ambiguous save status=%d body=%s", rec.Code, rec.Body.String())
	}
	eventBody := body
	eventBody.Set("_element", "Check")
	eventBody.Set("_event", string(metadata.FormEventOnClick))
	eventBody.Set("_kind", "object")
	eventResp := decodeFormEventResponse(t, executeFormEvent(t, srv, entity, eventBody).Body.Bytes())
	if eventResp.OK || !strings.Contains(strings.ToLower(eventResp.Error), "неоднознач") || len(eventResp.Messages) != 0 {
		t.Fatalf("ambiguous entity handler was not rejected before execution: %#v", eventResp)
	}

	processorForm := processorExecutionForm(form.Elements[1:]...)
	processorForm.ProgramAST = form.ProgramAST
	proc := &processor.Processor{
		Name: "AmbiguousTPProcessor", TableParts: entity.TableParts,
		Forms: []*metadata.FormModule{processorForm},
	}
	processorServer, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	names := processorServiceFieldNames(proc.Params)
	processorBody := body
	processorBody.Set(names["_element"], "Check")
	processorBody.Set(names["_event"], string(metadata.FormEventOnClick))
	processorResp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, processorServer, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(processorBody.Encode())).Body.Bytes())
	if processorResp.OK || !strings.Contains(strings.ToLower(processorResp.Error), "неоднознач") || len(processorResp.Messages) != 0 {
		t.Fatalf("ambiguous processor handler was not rejected before execution: %#v", processorResp)
	}
}

func TestManagedValueTablePayloadIsNamedOnly(t *testing.T) {
	form := managedObjectForm(&metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ScratchTable", DataPath: "Form.sCrAtCh",
		// ValueTable uses the DOM renderer even when NoGrid is left false.
	})
	form.Attributes = []*metadata.FormAttribute{{
		Name: "Scratch", TypeRef: "ValueTable",
		Columns: []*metadata.FormAttributeColumn{{Name: "Note", TypeRef: "string"}},
	}}
	entity := &metadata.Entity{Forms: []*metadata.FormModule{form}}
	body := url.Values{
		"vt.Scratch.0.Note": {"named-current"},
		"tp_json.Scratch":   {`[{"Note":"json-stale"}]`},
	}
	rows, err := parseValueTableRowsForManagedForm(parsedPayloadRequest(t, body), form, entity, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["Scratch"]; len(got) != 1 || got[0]["Note"] != "named-current" {
		t.Fatalf("ValueTable selected wrong channel: %#v", got)
	}
	jsonOnly := url.Values{"tp_json.Scratch": {`[{"Note":"forged"}]`}}
	rows, err = parseValueTableRowsForManagedForm(parsedPayloadRequest(t, jsonOnly), form, entity, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("ValueTable trusted inactive JSON channel: %#v", rows)
	}

	caseBody := url.Values{"VT.sCrAtCh.00.nOtE": {"case-current"}}
	rows, err = parseValueTableRowsForManagedForm(parsedPayloadRequest(t, caseBody), form, entity, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["Scratch"]; len(got) != 1 || got[0]["Note"] != "case-current" {
		t.Fatalf("ValueTable metadata was not canonicalized case-insensitively: %#v", got)
	}

	for _, duplicate := range []url.Values{
		{"vt.Scratch.0.Note": {"a", "b"}},
		{"vt.Scratch.0.Note": {"a"}, "VT.scratch.00.note": {"b"}},
	} {
		if _, err := parseValueTableRowsForManagedForm(parsedPayloadRequest(t, duplicate), form, entity, true); err == nil {
			t.Fatalf("duplicate canonical ValueTable payload was accepted: %#v", duplicate)
		}
	}
}

func TestSubmitNewUsesWritableSlickPayloadOverReadonlyNamedSummary(t *testing.T) {
	entity, _, _ := duplicatePayloadEntity(false, false)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	body, want := duplicatePayloadBody(false)
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/new", body,
		map[string]string{"entity": entity.Name})
	rec := httptest.NewRecorder()
	srv.submit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertStoredTablePartName(t, srv, entity, entity.TableParts[0], onlyStoredID(t, srv, entity), want)
}

func TestSubmitEditUsesWritableSlickPayloadOverReadonlyNamedSummary(t *testing.T) {
	entity, _, _ := duplicatePayloadEntity(false, true)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := srv.store.Upsert(t.Context(), entity.Name, id, map[string]any{"Name": "record"}, entity); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(t.Context(), entity.Name, "Lines", id,
		[]map[string]any{{"Name": "stored-before"}}, entity.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	body, want := duplicatePayloadBody(false)
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+entity.Name+"/"+id.String(), body,
		map[string]string{"entity": entity.Name, "id": id.String()})
	rec := httptest.NewRecorder()
	srv.submitEdit(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("edit status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertStoredTablePartName(t, srv, entity, entity.TableParts[0], id, want)
}

func TestManagedFormEventUsesWritableSlickPayloadOverReadonlyNamedSummary(t *testing.T) {
	entity, form, _ := duplicatePayloadEntity(false, false)
	form.ProgramAST = mustParse(t, "Процедура Run()\nКонецПроцедуры")
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	body, want := duplicatePayloadBody(false)
	body.Set("_element", "Check")
	body.Set("_event", string(metadata.FormEventOnClick))
	body.Set("_kind", "object")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, entity, body).Body.Bytes())
	if !resp.OK || resp.Error != "" || len(resp.TableParts["Lines"]) != 1 || resp.TableParts["Lines"][0]["Name"] != want {
		t.Fatalf("event selected wrong duplicate payload: %#v", resp)
	}
	var readonly *metadata.FormElement
	for _, element := range form.Elements {
		if element.Kind == metadata.FormElementTablePart && element.ReadOnly {
			readonly = element
			readonly.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "Run"}
			break
		}
	}
	if readonly == nil {
		t.Fatal("readonly duplicate not found")
	}
	body.Set("_element", readonly.Name)
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_tp", "Lines")
	resp = decodeFormEventResponse(t, executeFormEvent(t, srv, entity, body).Body.Bytes())
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "только для чтения") {
		t.Fatalf("readonly duplicate borrowed writable placement authority: %#v", resp)
	}
}

func TestManagedFormEventReadOnlyUserCannotForgeTablesOrTableEvents(t *testing.T) {
	entity, form, writable := duplicatePayloadEntity(false, false)
	// Actual CanWrite=false makes every placement inactive, even when the
	// metadata itself contains multiple otherwise-writable representations.
	for _, element := range form.Elements {
		if element.Kind == metadata.FormElementTablePart {
			element.ReadOnly = false
		}
	}
	form.ProgramAST = mustParse(t, "Процедура Run()\nКонецПроцедуры")
	form.Attributes = []*metadata.FormAttribute{{
		Name: "Scratch", TypeRef: "ValueTable",
		Columns: []*metadata.FormAttributeColumn{{Name: "Note", TypeRef: "string"}},
	}}
	scratch := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ScratchTable", DataPath: "Form.Scratch",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Run"},
	}
	form.Elements = append(form.Elements[:len(form.Elements)-1],
		scratch,
		form.Elements[len(form.Elements)-1])
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := srv.store.Upsert(t.Context(), entity.Name, id, map[string]any{"Name": "stored"}, entity); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertTablePartRows(t.Context(), entity.Name, "Lines", id,
		[]map[string]any{{"Name": "stored-row"}}, entity.TableParts[0]); err != nil {
		t.Fatal(err)
	}
	readUser := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read"}},
	}}}}
	request := func(body url.Values) formEventResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+entity.Name+"/form-event", strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		route := chi.NewRouteContext()
		route.URLParams.Add("entity", entity.Name)
		req = req.WithContext(context.WithValue(auth.ContextWithUser(req.Context(), readUser), chi.RouteCtxKey, route))
		rec := httptest.NewRecorder()
		srv.handleManagedFormEvent(rec, req)
		return decodeFormEventResponse(t, rec.Body.Bytes())
	}
	body := url.Values{
		"_id": {id.String()}, "_kind": {"object"}, "_element": {"Check"},
		"_event": {string(metadata.FormEventOnClick)}, "Name": {"forged-header"},
		"tp.Lines.0.Name": {"named-forged"}, "tp_json.Lines": {`[{"Name":"json-forged"}]`},
		"vt.Scratch.0.Note": {"vt-forged"}, "tp_json.Scratch": {`[{"Note":"json-forged"}]`},
	}
	resp := request(body)
	if !resp.OK || resp.Error != "" || len(resp.TableParts["Lines"]) != 1 || resp.TableParts["Lines"][0]["Name"] != "stored-row" {
		t.Fatalf("read-only event did not restore persistent TP: %#v", resp)
	}
	if _, forged := resp.FormTables["Scratch"]; forged {
		t.Fatalf("read-only event accepted forged ValueTable: %#v", resp.FormTables)
	}

	body.Set("_element", writable.Name)
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_tp", "Lines")
	resp = request(body)
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "только для чтения") {
		t.Fatalf("read-only user invoked forged table event: %#v", resp)
	}
	body.Set("_element", scratch.Name)
	resp = request(body)
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "только для чтения") {
		t.Fatalf("read-only user invoked forged ValueTable event: %#v", resp)
	}
	form.ProgramAST = nil
	body.Set("_element", writable.Name)
	body.Set("_event", string(metadata.FormEventOnChange))
	resp = request(body)
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "только для чтения") {
		t.Fatalf("table event bypassed authority when form AST was unavailable: %#v", resp)
	}
}

func TestProcessorFormEventUsesWritableSlickPayloadOverReadonlyNamedSummary(t *testing.T) {
	_, entityForm, _ := duplicatePayloadEntity(false, false)
	button := entityForm.Elements[len(entityForm.Elements)-1]
	form := processorExecutionForm(entityForm.Elements[1], entityForm.Elements[2], button)
	form.ProgramAST = mustParse(t, "Процедура Run()\nКонецПроцедуры")
	proc := &processor.Processor{
		Name: "MixedTPProcessor", TableParts: []metadata.TablePart{{
			Name: "Lines", Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
		}}, Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	names := processorServiceFieldNames(proc.Params)
	body, want := duplicatePayloadBody(false)
	body.Set(names["_element"], "Check")
	body.Set(names["_event"], string(metadata.FormEventOnClick))
	resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode())).Body.Bytes())
	if !resp.OK || resp.Error != "" || len(resp.TableParts["Lines"]) != 1 || resp.TableParts["Lines"][0]["Name"] != want {
		t.Fatalf("processor selected wrong duplicate payload: %#v", resp)
	}
}
