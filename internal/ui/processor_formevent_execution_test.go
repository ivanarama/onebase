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
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/processor/"+procName+"/form-event", body)
	req.Header.Set("Content-Type", contentType)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", procName)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleProcessorFormEvent(rec, req)
	return rec
}

func processorExecutionForm(element *metadata.FormElement) *metadata.FormModule {
	return &metadata.FormModule{
		Name:       "ФормаОбработки",
		Kind:       "object",
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{element},
	}
}

func processorClickBody(elementName string) url.Values {
	body := url.Values{}
	body.Set("_element", elementName)
	body.Set("_event", string(metadata.FormEventOnClick))
	return body
}

func TestHandleProcessorFormEvent_FallbackReadsMultipartFile(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Запустить"})
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

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("_element", "Запустить"); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("_event", string(metadata.FormEventOnClick)); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("Данные", "input.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("содержимое файла")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	rec := postProcessorFormEventExecution(t, srv, proc.Name, mw.FormDataContentType(), &body)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "содержимое файла" {
		t.Fatalf("file parameter not passed: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleProcessorFormEvent_MissingBoundHandlerDoesNotFallback(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Запустить",
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
	body := processorClickBody("Запустить")

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
	if !resp.OK || resp.Error != "" || len(resp.Messages) != 0 {
		t.Fatalf("forged non-button click executed fallback: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
	}
}

func TestHandleProcessorFormEvent_RespectsConcurrencyLimit(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Запустить"})
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
	body := processorClickBody("Запустить")

	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	if rec.Code != 429 {
		t.Fatalf("status=%d, want 429; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProcessorFormEvent_RespectsRequestTimeout(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Запустить"})
	proc := &processor.Processor{Name: "ТаймаутОбр", Forms: []*metadata.FormModule{form}}
	program := mustParse(t, `
Процедура Выполнить()
	Пока Истина Цикл
	КонецЦикла;
КонецПроцедуры
`)
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
	srv.cfg.Limits.RequestTimeoutSec = 1
	body := processorClickBody("Запустить")

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
}

func TestHandleProcessorFormEvent_AuditsExternalFormHandler(t *testing.T) {
	formProgram := mustParse(t, `
Процедура Нажать()
	Сообщить("ok");
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementButton,
		Name: "Запустить",
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
	body := processorClickBody("Запустить")

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
