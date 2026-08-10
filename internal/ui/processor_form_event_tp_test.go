package ui

import (
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func processorTPContextFixture(t *testing.T, readOnly bool) (*Server, *processor.Processor, map[string]string) {
	t.Helper()
	formProgram := mustParse(t, `
Процедура СтрокиПриИзменении()
	Сообщить(ИмяТабличнойЧасти);
	Сообщить(ТекущаяКолонка);
	Сообщить(ИндексСтроки);
	Сообщить(НомерСтроки);
	Сообщить(ТекущаяСтрока.Количество);
	Сообщить(ВыделенныеСтроки.Количество());
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ЭлементСтроки", DataPath: "Объект.Строки", ReadOnly: readOnly,
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "СтрокиПриИзменении"},
	})
	form.ProgramAST = formProgram
	params := make([]processor.Param, 0, len(processorServiceFields))
	for _, name := range processorServiceFields {
		params = append(params, processor.Param{Name: name, Type: "string"})
	}
	proc := &processor.Processor{
		Name:   "КонтекстТЧ",
		Params: params,
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Товар", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			},
		}},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	return srv, proc, processorServiceFieldNames(proc.Params)
}

func validProcessorTPContextBody(names map[string]string) url.Values {
	body := url.Values{}
	body.Set(names["_element"], "ЭлементСтроки")
	body.Set(names["_event"], string(metadata.FormEventOnChange))
	body.Set(names["_tp"], "строки")
	body.Set(names["_tp_selected"], "0,1")
	body.Set(names["_tp_row"], "1")
	body.Set(names["_tp_row_number"], "2")
	body.Set(names["_tp_col"], "количество")
	body.Set(names["_tp_col_index"], "1")
	body.Set("tp_json.Строки", `[{"Товар":"A","Количество":10},{"Товар":"B","Количество":20}]`)
	return body
}

func TestHandleProcessorFormEventCanonicalizesCollisionSafeTPContext(t *testing.T) {
	srv, proc, names := processorTPContextFixture(t, false)
	body := validProcessorTPContextBody(names)
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"Строки", "Количество", "1", "2", "20", "2"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("TP context: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
	if names["_tp"] == "_tp" || names["_tp_row"] == "_tp_row" {
		t.Fatalf("fixture did not allocate collision-safe names: %#v", names)
	}
}

func TestHandleProcessorFormEventRejectsForgedOrInconsistentTPContext(t *testing.T) {
	srv, proc, names := processorTPContextFixture(t, false)
	tests := []struct {
		name   string
		mutate func(url.Values)
	}{
		{
			name: "hardcoded service names are ignored",
			mutate: func(body url.Values) {
				body.Del(names["_tp"])
				body.Set("_tp", "Строки")
			},
		},
		{
			name: "selected row out of range",
			mutate: func(body url.Values) {
				body.Set(names["_tp_selected"], "0,9")
			},
		},
		{
			name: "row number mismatch",
			mutate: func(body url.Values) {
				body.Set(names["_tp_row_number"], "1")
			},
		},
		{
			name: "column index mismatch",
			mutate: func(body url.Values) {
				body.Set(names["_tp_col_index"], "0")
			},
		},
		{
			name: "unrendered table part",
			mutate: func(body url.Values) {
				body.Set(names["_tp"], "СкрытаяТЧ")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validProcessorTPContextBody(names)
			tc.mutate(body)
			rec := postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
				t.Fatalf("forged TP context accepted: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
			}
		})
	}
}

func TestHandleProcessorFormEventRejectsReadOnlyTablePartEvent(t *testing.T) {
	srv, proc, names := processorTPContextFixture(t, true)
	body := validProcessorTPContextBody(names)
	resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode())).Body.Bytes())
	if resp.OK || !strings.Contains(strings.ToLower(resp.Error), "только для чтения") {
		t.Fatalf("readonly TP event was not rejected: ok=%v error=%q", resp.OK, resp.Error)
	}
}

func TestPickerPhaseTwoPreservesTablePartContext(t *testing.T) {
	managed := string(managedJS)
	uiSource := string(uiJS)
	if !strings.Contains(managed, "openItemPicker(data.pickerData, elementName, extraParams || null)") {
		t.Fatal("managed.js drops TP context when opening picker phase two")
	}
	if !strings.Contains(managed, "canonicalItems") || !strings.Contains(managed, "getItem(displayIndex)") {
		t.Fatal("managed.js does not map visually sorted selected rows to canonical POST rows")
	}
	if !strings.Contains(uiSource, "Object.keys(eventContext)") || !strings.Contains(uiSource, "params._pick_result") {
		t.Fatal("ui.js does not merge TP context into picker phase-two request")
	}
}
