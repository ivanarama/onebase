package ui

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func newProcessorFormEventExecutionServer(t *testing.T, proc *processor.Processor, program *ast.Program) (*Server, *storage.DB) {
	t.Helper()
	registry := runtime.NewRegistry()
	if program != nil {
		registry.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{proc.Name: program}})
	}
	registry.LoadProcessors([]*processor.Processor{proc})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	return &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
		ops:      newOperationLimiter(),
	}, db
}

func postProcessorFormEventExecution(t *testing.T, srv *Server, procName, contentType string, body io.Reader) *httptest.ResponseRecorder {
	return postProcessorFormEventExecutionWithQuery(t, srv, procName, nil, contentType, body)
}

func postProcessorFormEventExecutionWithQuery(
	t *testing.T,
	srv *Server,
	procName string,
	query url.Values,
	contentType string,
	body io.Reader,
) *httptest.ResponseRecorder {
	t.Helper()
	target := "/ui/processor/" + procName + "/form-event"
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	req := httptest.NewRequest("POST", target, body)
	req.Header.Set("Content-Type", contentType)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", procName)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleProcessorFormEvent(rec, req)
	return rec
}

func processorExecutionForm(elements ...*metadata.FormElement) *metadata.FormModule {
	return &metadata.FormModule{
		Name:       "ФормаОбработки",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   elements,
	}
}

func processorClickBody(elementName string) url.Values {
	body := url.Values{}
	body.Set("_element", elementName)
	body.Set("_event", string(metadata.FormEventOnClick))
	return body
}

func TestProcessorFormBodyLimit_AllowsURLEncodingExpansion(t *testing.T) {
	const maxFileSize = int64(7 << 20)
	params := []processor.Param{{Name: "First", Type: "file"}, {Name: "Second", Type: "file"}}
	proc := &processor.Processor{Params: params}
	controls := processorRequestControlsForForm(proc, nil)
	urlEncoded := httptest.NewRequest("POST", "/", nil)
	urlEncoded.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if got, want := processorFormBodyLimit(urlEncoded, maxFileSize, controls), 6*maxFileSize+uiMultipartOverhead; got != want {
		t.Fatalf("urlencoded body limit=%d, want %d", got, want)
	}

	multipartRequest := httptest.NewRequest("POST", "/", nil)
	multipartRequest.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	if got, want := processorFormBodyLimit(multipartRequest, maxFileSize, controls), 2*maxFileSize+uiMultipartOverhead; got != want {
		t.Fatalf("multipart body limit=%d, want %d", got, want)
	}

	noFiles := processorRequestControlsForForm(&processor.Processor{Params: []processor.Param{{Name: "Text", Type: "string"}}}, nil)
	if got := processorFormBodyLimit(urlEncoded, maxFileSize, noFiles); got != defaultFormMemoryBytes {
		t.Fatalf("zero-file body limit=%d, want small form limit %d", got, defaultFormMemoryBytes)
	}

	managedProc := &processor.Processor{Params: []processor.Param{
		{Name: "Rendered", Type: "file"},
		{Name: "Readonly", Type: "file"},
		{Name: "Unplaced", Type: "file"},
	}}
	managedForm := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "RenderedFile", DataPath: "Объект.Rendered", Type: "file"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ReadonlyFile", DataPath: "Объект.Readonly", Type: "file", ReadOnly: true},
	)
	managedControls := processorRequestControlsForForm(managedProc, managedForm)
	if got, want := processorFormBodyLimit(urlEncoded, maxFileSize, managedControls), 3*maxFileSize+uiMultipartOverhead; got != want {
		t.Fatalf("managed body limit=%d, want only one editable rendered file (%d)", got, want)
	}
}

func TestProcessorSandboxTimeout_UsesRemainingOperationDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	got := processorSandboxTimeout(ctx, 2*time.Hour)
	if got < 59*time.Minute || got > time.Hour {
		t.Fatalf("sandbox timeout=%v, want remaining operation deadline", got)
	}
	if got := processorSandboxTimeout(context.Background(), 0); got != 0 {
		t.Fatalf("disabled timeout=%v, want 0", got)
	}
}

func TestHandleProcessorFormEvent_FallbackReadsBrowserFileContent(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеДанные",
			DataPath: "Объект.Данные", Type: "file",
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name:   "ФайловаяОбр",
		Params: []processor.Param{{Name: "Данные", Type: "file"}},
		Forms:  []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Данные)
	Сообщить(Данные);
КонецПроцедуры
	`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)

	for _, tc := range []struct {
		name  string
		field string
	}{
		{name: "obFire output", field: "Данные"},
		{name: "file-content backing field", field: "_fc_Данные"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := processorClickBody("Выполнить")
			body.Set(tc.field, "содержимое файла")
			rec := postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
			if rec.Code != 200 {
				t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
			}
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "содержимое файла" {
				t.Fatalf("file parameter not passed: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
			}
		})
	}
}

func TestHandleProcessorFormEvent_ReadsMultipartFile(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеДанные",
			DataPath: "Объект.Данные", Type: "file",
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name:   "MultipartФайловаяОбр",
		Params: []processor.Param{{Name: "Данные", Type: "file"}},
		Forms:  []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Данные)
	Сообщить(Данные);
КонецПроцедуры
	`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("_element", "Выполнить"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("_event", string(metadata.FormEventOnClick)); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("Данные", "данные.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("multipart-содержимое")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	rec := postProcessorFormEventExecution(t, srv, proc.Name, writer.FormDataContentType(), &body)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "multipart-содержимое" {
		t.Fatalf("multipart file parameter not passed: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleProcessorFormEvent_UncheckedBoolDiffersFromAbsentParam(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "ПолеФлаг", DataPath: "Объект.Флаг"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name:   "БулеваОбр",
		Params: []processor.Param{{Name: "Флаг", Type: "bool"}},
		Forms:  []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Флаг = Истина)
	Если Флаг Тогда
		Сообщить("true");
	Иначе
		Сообщить("false");
	КонецЕсли;
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)

	for _, tc := range []struct {
		name    string
		marker  bool
		message string
	}{
		{name: "rendered checkbox unchecked", marker: true, message: "false"},
		{name: "custom form parameter absent", marker: false, message: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := processorClickBody("Выполнить")
			if tc.marker {
				body.Set("_ob_present_Флаг", "1")
			}
			rec := postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
			if rec.Code != 200 {
				t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
			}
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != tc.message {
				t.Fatalf("bool presence mismatch: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
			}
		})
	}
}

func TestHandleProcessorFormEvent_HelperPrefixesRemainLegalCustomParams(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеЛегальныйМаркер",
			DataPath: "Объект._ob_present_Flag",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеЛегальныйФайл",
			DataPath: "Объект._fc_Data",
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name: "ЛегальныеПрефиксыОбр",
		Params: []processor.Param{
			{Name: "Flag", Type: "bool"},
			{Name: "Data", Type: "file"},
			{Name: "_ob_present_Flag", Type: "string"},
			{Name: "_fc_Data", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Flag = Истина, Data = "file-default", _ob_present_Flag = "", _fc_Data = "")
	Если Flag Тогда
		Сообщить("true");
	Иначе
		Сообщить("false");
	КонецЕсли;
	Сообщить(Data);
	Сообщить(_ob_present_Flag);
	Сообщить(_fc_Data);
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("Выполнить")
	body.Set("_ob_present_Flag", "legal-marker-value")
	body.Set("_fc_Data", "legal-file-value")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"true", "file-default", "legal-marker-value", "legal-file-value"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("helper-shaped custom params corrupted values: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_HelperNamesAvoidDeclaredParamCollisions(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementCheckbox, Name: "ПолеFlag",
			DataPath: "Объект.Flag",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеData",
			DataPath: "Объект.Data", Type: "file",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеЛегальныйМаркер",
			DataPath: "Объект._ob_present_Flag",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеЛегальныйФайл",
			DataPath: "Объект._fc_Data",
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name: "КоллизииПрефиксовОбр",
		Params: []processor.Param{
			{Name: "Flag", Type: "bool"},
			{Name: "Data", Type: "file"},
			{Name: "_ob_present_Flag", Type: "string"},
			{Name: "_fc_Data", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Flag = Истина, Data = "file-default", _ob_present_Flag = "", _fc_Data = "")
	Если Flag Тогда
		Сообщить("true");
	Иначе
		Сообщить("false");
	КонецЕсли;
	Сообщить(Data);
	Сообщить(_ob_present_Flag);
	Сообщить(_fc_Data);
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("Выполнить")
	boolHelper := processorParamPresenceName(proc.Params, "Flag")
	fileHelper := processorFileContentName(proc.Params, "Data")
	if boolHelper == "_ob_present_Flag" || fileHelper == "_fc_Data" {
		t.Fatalf("helper names still collide: bool=%q file=%q", boolHelper, fileHelper)
	}
	body.Set(boolHelper, "1")
	body.Set(fileHelper, "actual-file-content")
	body.Set("_ob_present_Flag", "legal-marker-value")
	body.Set("_fc_Data", "legal-file-value")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"false", "actual-file-content", "legal-marker-value", "legal-file-value"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("collision-safe helpers corrupted values: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_ServiceNamesAvoidDeclaredParamCollisions(t *testing.T) {
	formProgram := mustParse(t, `
Процедура RunCheck()
	Сообщить(Объект._event + ":" + Параметры._element + ":" + Объект._kind + ":" + Параметры._pick_result);
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ParamEvent", DataPath: "Объект._event"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ParamElement", DataPath: "Объект._element"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ParamKind", DataPath: "Объект._kind"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ParamPick", DataPath: "Объект._pick_result"},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Проверить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "RunCheck",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name: "ServiceNameCollisions",
		Params: []processor.Param{
			{Name: "_event", Type: "string"},
			{Name: "_element", Type: "string"},
			{Name: "_kind", Type: "string"},
			{Name: "_pick_result", Type: "string"},
			{Name: "_ob_service_event", Type: "string"},
			{Name: "_ob_service_event_", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := url.Values{
		"_event":       {"param-event"},
		"_element":     {"param-element"},
		"_kind":        {"param-kind"},
		"_pick_result": {"param-pick"},
	}
	serviceNames := processorServiceFieldNames(proc.Params)
	body.Set(serviceNames["_element"], "Проверить")
	body.Set(serviceNames["_event"], string(metadata.FormEventOnClick))
	body.Set(serviceNames["_kind"], "object")
	body.Set(serviceNames["_pick_result"], "[]")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := "param-event:param-element:param-kind:param-pick"
	if !resp.OK || resp.Error != "" || len(resp.Messages) != 1 || resp.Messages[0] != want {
		t.Fatalf("service fields corrupted legal params: ok=%v error=%q messages=%v, want=%q", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_QueryCannotInjectHelpers(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementCheckbox, Name: "ПолеFlag",
			DataPath: "Объект.Flag",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеData",
			DataPath: "Объект.Data", Type: "file",
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name: "QueryHelpersОбр",
		Params: []processor.Param{
			{Name: "Flag", Type: "bool"},
			{Name: "Data", Type: "file"},
		},
		Forms: []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Flag = Истина, Data = "file-default")
	Если Flag Тогда
		Сообщить("true");
	Иначе
		Сообщить("false");
	КонецЕсли;
	Сообщить(Data);
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("Выполнить")
	query := url.Values{
		"_ob_present_Flag": {"1"},
		"_fc_Data":         {"query-file-content"},
	}

	rec := postProcessorFormEventExecutionWithQuery(t, srv, proc.Name, query,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"true", "file-default"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("query injected helper fields: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_RejectsHelpersForReadOnlyControls(t *testing.T) {
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementCheckbox, Name: "ПолеFlag",
			DataPath: "Объект.Flag", ReadOnly: true,
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеData",
			DataPath: "Объект.Data", Type: "file", ReadOnly: true,
		},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"},
	)
	proc := &processor.Processor{
		Name: "ReadOnlyHelpersОбр",
		Params: []processor.Param{
			{Name: "Flag", Type: "bool"},
			{Name: "Data", Type: "file"},
		},
		Forms: []*metadata.FormModule{form},
	}
	program := mustParse(t, `
Процедура Выполнить(Flag = Истина, Data = "file-default")
	Если Flag Тогда
		Сообщить("true");
	Иначе
		Сообщить("false");
	КонецЕсли;
	Сообщить(Data);
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("Выполнить")
	body.Set("_ob_present_Flag", "1")
	body.Set("_fc_Data", "forged-file-content")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"true", "file-default"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("read-only helpers changed values: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_BoundHandlerRejectsQueryParamInjection(t *testing.T) {
	formProgram := mustParse(t, `
Процедура RunCheck()
	Сообщить("видимое:" + Объект.Visible);
	Если Параметры.Hidden = Неопределено Тогда
		Сообщить("скрытое:нет");
	Иначе
		Сообщить("скрытое:" + Параметры.Hidden);
	КонецЕсли;
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеVisible",
			DataPath: "Объект.Visible",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Проверить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "RunCheck",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name: "BoundQueryInjection",
		Params: []processor.Param{
			{Name: "Visible", Type: "string"},
			{Name: "Hidden", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorClickBody("Проверить")
	body.Set("Visible", "body-value")
	query := url.Values{
		"Visible": {"query-value"},
		"Hidden":  {"query-hidden"},
	}

	rec := postProcessorFormEventExecutionWithQuery(t, srv, proc.Name, query,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"видимое:body-value", "скрытое:нет"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("query changed bound handler object: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_BoundHandlerRejectsReadOnlyAndUnrenderedParams(t *testing.T) {
	formProgram := mustParse(t, `
Процедура RunCheck()
	Сообщить("видимое:" + Объект.Visible);
	Если Объект.ReadOnlyParam = Неопределено Тогда
		Сообщить("толькочтение:нет");
	Иначе
		Сообщить("толькочтение:" + Объект.ReadOnlyParam);
	КонецЕсли;
	Если Параметры.Hidden = Неопределено Тогда
		Сообщить("скрытое:нет");
	Иначе
		Сообщить("скрытое:" + Параметры.Hidden);
	КонецЕсли;
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеVisible",
			DataPath: "Объект.Visible",
		},
		&metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеReadOnly",
			DataPath: "Объект.ReadOnlyParam", ReadOnly: true,
		},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Проверить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "RunCheck",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name: "BoundDirectSpoof",
		Params: []processor.Param{
			{Name: "Visible", Type: "string"},
			{Name: "ReadOnlyParam", Type: "string"},
			{Name: "Hidden", Type: "string"},
		},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorClickBody("Проверить")
	body.Set("Visible", "body-value")
	body.Set("ReadOnlyParam", "forged-readonly")
	body.Set("Hidden", "forged-hidden")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	want := []string{"видимое:body-value", "толькочтение:нет", "скрытое:нет"}
	if !resp.OK || resp.Error != "" || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forged params changed bound handler object: ok=%v error=%q messages=%v, want=%v", resp.OK, resp.Error, resp.Messages, want)
	}
}

func TestHandleProcessorFormEvent_MissingBoundHandlerDoesNotFallback(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Выполнить",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick: "НетТакойПроцедуры",
		},
	})
	proc := &processor.Processor{Name: "НетОбработчика", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	Сообщить("fallback не должен выполняться");
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("Выполнить")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "НетТакойПроцедуры") || len(resp.Messages) != 0 {
		t.Fatalf("missing bound handler fell through: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleProcessorFormEvent_FallbackRequiresRealButton(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementField, Name: "НеКнопка"})
	proc := &processor.Processor{Name: "ПоддельныйКлик", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	Сообщить("не должно выполняться");
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	body := processorClickBody("НеКнопка")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
		t.Fatalf("forged non-button click was not rejected: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleProcessorFormEvent_RespectsConcurrencyLimit(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"})
	proc := &processor.Processor{Name: "ЛимитОбр", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	Сообщить("не должно выполняться");
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	srv.cfg.Limits.ProcessorConcurrency = 1
	release, ok := srv.ops.tryAcquire(opProcessorRun, 1)
	if !ok {
		t.Fatal("failed to reserve processor slot")
	}
	defer release()
	body := processorClickBody("Выполнить")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 429 {
		t.Fatalf("status=%d, want 429; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProcessorFormEvent_RespectsRequestTimeout(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"})
	proc := &processor.Processor{Name: "ТаймаутОбр", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	НачатьТранзакцию();
	Пока Истина Цикл
	КонецЦикла;
КонецПроцедуры
`)
	srv, db := newProcessorFormEventExecutionServer(t, proc, program)
	srv.cfg.Limits.RequestTimeoutSec = 1
	body := processorClickBody("Выполнить")

	started := time.Now()
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	elapsed := time.Since(started)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || resp.Error == "" {
		t.Fatalf("timeout was not surfaced: ok=%v error=%q", resp.OK, resp.Error)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("request timeout took too long: %v", elapsed)
	}
	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRow(readCtx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("timeout cleanup left DB unavailable: one=%d err=%v", one, err)
	}
}

func TestHandleProcessorFormEvent_BoundHandlerRollsBackOpenTransaction(t *testing.T) {
	formProgram := mustParse(t, `
Процедура RunCheck()
	НачатьТранзакцию();
	ВызватьИсключение("boom");
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "Selected", DataPath: "Объект.Selected"},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Проверить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick: "RunCheck",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name:   "BoundOpenTransaction",
		Params: []processor.Param{{Name: "Selected", Type: "reference:ReferenceItems"}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, db := newProcessorFormEventExecutionServer(t, proc, nil)
	refEntity := &metadata.Entity{
		Name: "ReferenceItems", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, []*metadata.Entity{refEntity}); err != nil {
		t.Fatal(err)
	}
	srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{refEntity}})
	refID := uuid.New()
	if err := db.Upsert(ctx, refEntity.Name, refID, map[string]any{"Name": "item"}, refEntity); err != nil {
		t.Fatal(err)
	}
	srv.cfg.Limits.RequestTimeoutSec = 2
	body := processorClickBody("Проверить")
	body.Set("Selected", refID.String())

	started := time.Now()
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("bound handler response waited for transaction timeout: %v", elapsed)
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, "boom") {
		t.Fatalf("unexpected response: ok=%v error=%q", resp.OK, resp.Error)
	}

	readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := db.GetByID(readCtx, refEntity.Name, refID, refEntity); err != nil {
		t.Fatalf("open transaction still owns the database connection: %v", err)
	}
}

func TestProcessorExecutionBoundariesRejectSuccessfulOpenTransaction(t *testing.T) {
	program := mustParse(t, `
Процедура Выполнить()
	НачатьТранзакцию();
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"})
	proc := &processor.Processor{Name: "OpenBoundary", Forms: []*metadata.FormModule{form}}
	srv, db := newProcessorFormEventExecutionServer(t, proc, program)
	assertAvailable := func(stage string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var one int
		if err := db.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatalf("%s left DB unavailable: one=%d err=%v", stage, one, err)
		}
	}

	t.Run("fallback form event", func(t *testing.T) {
		body := processorClickBody("Выполнить")
		resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
			"application/x-www-form-urlencoded", strings.NewReader(body.Encode())).Body.Bytes())
		if resp.OK || !strings.Contains(resp.Error, errDSLTransactionLeftOpen.Error()) {
			t.Fatalf("open fallback transaction: ok=%v error=%q", resp.OK, resp.Error)
		}
		assertAvailable("fallback")
	})

	t.Run("processorRun", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/ui/processor/"+proc.Name, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", proc.Name)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.processorRun(rec, req)
		if !strings.Contains(rec.Body.String(), errDSLTransactionLeftOpen.Error()) {
			t.Fatalf("processorRun did not render open transaction error: %s", rec.Body.String())
		}
		assertAvailable("processorRun")
	})

	t.Run("offline", func(t *testing.T) {
		_, runErr, err := srv.RunProcessor(context.Background(), srv.reg, proc.Name, nil, nil, nil)
		if err != nil || runErr == nil || !strings.Contains(runErr.Error(), errDSLTransactionLeftOpen.Error()) {
			t.Fatalf("offline open transaction: setupErr=%v runErr=%v", err, runErr)
		}
		assertAvailable("offline")
	})
}

func TestBoundProcessorEventRejectsSuccessfulOpenTransaction(t *testing.T) {
	formProgram := mustParse(t, `
Процедура Нажать()
	НачатьТранзакцию();
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton, Name: "Кнопка",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Нажать"},
	})
	form.ProgramAST = formProgram
	proc := &processor.Processor{Name: "BoundOpenSuccess", Forms: []*metadata.FormModule{form}}
	srv, db := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorClickBody("Кнопка")
	resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded", strings.NewReader(body.Encode())).Body.Bytes())
	if resp.OK || !strings.Contains(resp.Error, errDSLTransactionLeftOpen.Error()) {
		t.Fatalf("bound open transaction: ok=%v error=%q", resp.OK, resp.Error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("bound cleanup left DB unavailable: one=%d err=%v", one, err)
	}
}

func TestHandleProcessorFormEvent_AuditsExternalFormHandler(t *testing.T) {
	formProgram := mustParse(t, `
Процедура Нажать()
	Сообщить("ok");
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Выполнить",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick: "Нажать",
		},
	})
	form.ProgramAST = formProgram
	proc := &processor.Processor{
		Name:     "ВнешняяСФормой",
		External: true,
		Trusted:  true,
		Forms:    []*metadata.FormModule{form},
	}
	srv, db := newProcessorFormEventExecutionServer(t, proc, nil)
	ctx := context.Background()
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	body := processorClickBody("Выполнить")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("form handler failed: %q", resp.Error)
	}
	entries, err := db.AuditSearch(ctx, storage.AuditFilter{
		Action:     "extprocessor.run",
		EntityName: proc.Name,
	}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
}
