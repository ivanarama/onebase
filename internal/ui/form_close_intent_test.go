package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/storage"
)

func closeIntentBody(intentID, reason, name string) url.Values {
	return url.Values{
		"_close_intent_id": {intentID},
		"_close_epoch":     {formCloseProcessEpoch},
		"_close_issued_at": {strconv.FormatInt(time.Now().UnixMilli(), 10)},
		"_close_reason":    {reason},
		"_close_mode":      {"discard"},
		"_close_client":    {uuid.NewString()},
		"_kind":            {"object"},
		"Наименование":     {name},
	}
}

func executeFormCloseIntent(t *testing.T, s *Server, ent *metadata.Entity, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	ensureEntityCloseIntentSchema(ent, body)
	kind := strings.ToLower(string(ent.Kind))
	req := httptest.NewRequest(http.MethodPost, "/ui/"+kind+"/"+ent.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setEntityCloseIntentHeaders(req, body)
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	s.Mount(router)
	router.ServeHTTP(rec, req)
	return rec
}

func decodeCloseIntentResponse(t *testing.T, rec *httptest.ResponseRecorder) formEventResponse {
	t.Helper()
	var response formEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode close response: %v; body=%s", err, rec.Body.String())
	}
	return response
}

func executeProcessorCloseIntent(t *testing.T, s *Server, proc *processor.Processor, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	ensureProcessorCloseIntentSchema(proc, body)
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setProcessorCloseIntentHeaders(req, proc, body)
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	s.Mount(router)
	router.ServeHTTP(rec, req)
	return rec
}

func setEntityCloseIntentHeaders(req *http.Request, body interface{ Get(string) string }) {
	req.Header.Set("X-OneBase-Close-Intent", body.Get("_close_intent_id"))
	req.Header.Set("X-OneBase-Close-Epoch", body.Get("_close_epoch"))
	req.Header.Set("X-OneBase-Close-Issued-At", body.Get("_close_issued_at"))
	req.Header.Set("X-OneBase-Close-Reason", body.Get("_close_reason"))
	req.Header.Set("X-OneBase-Close-Mode", body.Get("_close_mode"))
	req.Header.Set("X-OneBase-Close-Client", body.Get("_close_client"))
	req.Header.Set("X-OneBase-Close-Schema", body.Get("_close_schema"))
	req.Header.Set("X-OneBase-Form-Kind", body.Get("_kind"))
	req.Header.Set("X-OneBase-Record-ID", body.Get("_id"))
}

func setProcessorCloseIntentHeaders(req *http.Request, proc *processor.Processor, body interface{ Get(string) string }) {
	req.Header.Set("X-OneBase-Close-Intent", body.Get(processorServiceFieldName(proc.Params, "_close_intent_id")))
	req.Header.Set("X-OneBase-Close-Epoch", body.Get(processorServiceFieldName(proc.Params, "_close_epoch")))
	req.Header.Set("X-OneBase-Close-Issued-At", body.Get(processorServiceFieldName(proc.Params, "_close_issued_at")))
	req.Header.Set("X-OneBase-Close-Reason", body.Get(processorServiceFieldName(proc.Params, "_close_reason")))
	req.Header.Set("X-OneBase-Close-Mode", body.Get(processorServiceFieldName(proc.Params, "_close_mode")))
	req.Header.Set("X-OneBase-Close-Client", body.Get(processorServiceFieldName(proc.Params, "_close_client")))
	req.Header.Set("X-OneBase-Close-Schema", body.Get(processorServiceFieldName(proc.Params, "_close_schema")))
}

func ensureEntityCloseIntentSchema(entity *metadata.Entity, body url.Values) {
	if body.Get("_close_schema") != "" {
		return
	}
	formKind := strings.ToLower(strings.TrimSpace(body.Get("_kind")))
	if formKind == "" {
		formKind = "object"
	}
	body.Set("_close_schema", entityFormCloseSchema(entity, pickManagedForm(entity, formKind)))
}

func ensureProcessorCloseIntentSchema(proc *processor.Processor, body url.Values) {
	name := processorServiceFieldName(proc.Params, "_close_schema")
	if body.Get(name) == "" {
		body.Set(name, processorFormCloseSchema(proc, proc.ManagedForm()))
	}
}

func executeProcessorCloseIntentRaw(t *testing.T, s *Server, proc *processor.Processor, envelope url.Values, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	ensureProcessorCloseIntentSchema(proc, envelope)
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent", body)
	req.Header.Set("Content-Type", contentType)
	setProcessorCloseIntentHeaders(req, proc, envelope)
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	s.Mount(router)
	router.ServeHTTP(rec, req)
	return rec
}

func processorCloseIntentBody(proc *processor.Processor, intentID string) url.Values {
	body := url.Values{}
	body.Set(processorServiceFieldName(proc.Params, "_close_intent_id"), intentID)
	body.Set(processorServiceFieldName(proc.Params, "_close_epoch"), formCloseProcessEpoch)
	body.Set(processorServiceFieldName(proc.Params, "_close_issued_at"), strconv.FormatInt(time.Now().UnixMilli(), 10))
	body.Set(processorServiceFieldName(proc.Params, "_close_reason"), "cross")
	body.Set(processorServiceFieldName(proc.Params, "_close_mode"), "discard")
	body.Set(processorServiceFieldName(proc.Params, "_close_client"), uuid.NewString())
	body.Set(processorServiceFieldName(proc.Params, "_close_schema"), processorFormCloseSchema(proc, proc.ManagedForm()))
	return body
}

func processorCloseMultipartBody(t *testing.T, proc *processor.Processor, envelope url.Values, filename, content string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, values := range envelope {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatalf("write multipart field %s: %v", key, err)
			}
		}
	}
	file, err := writer.CreateFormFile("Данные", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func TestManagedFormCloseIntentOutputCancelAndReason(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Сообщить(ПричинаЗакрытия + ":" + CloseReason);
	Если Объект.Наименование = "" Тогда
		Отказ = Истина;
		Возврат;
	КонецЕсли;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)

	deniedID := uuid.NewString()
	denied := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, closeIntentBody(deniedID, "escape", "")))
	if !denied.OK || denied.Close == nil || denied.Close.IntentID != deniedID || denied.Close.Allowed {
		t.Fatalf("cancel output did not keep form open: %+v", denied)
	}
	if strings.Join(denied.Messages, "|") != "escape:escape" {
		t.Fatalf("close reason was not injected: %v", denied.Messages)
	}

	allowedID := uuid.NewString()
	allowed := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, closeIntentBody(allowedID, "cross", "готово")))
	if !allowed.OK || allowed.Close == nil || allowed.Close.IntentID != allowedID || !allowed.Close.Allowed {
		t.Fatalf("allowed close was rejected: %+v", allowed)
	}
}

func TestManagedFormCloseIntentExceptionAndMissingHandlerFailClosedCorrectly(t *testing.T) {
	t.Run("exception", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	ВызватьИсключение("заполните обязательные поля");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
		resp := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "cross", "x")))
		if resp.OK || resp.Close == nil || resp.Close.Allowed || !strings.Contains(resp.Error, "заполните обязательные поля") {
			t.Fatalf("exception did not fail closed: %+v", resp)
		}
	})

	t.Run("no handler permits close", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, "", nil, nil)
		resp := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "close", "x")))
		if !resp.OK || resp.Close == nil || !resp.Close.Allowed {
			t.Fatalf("form without BeforeClose must close: %+v", resp)
		}
	})

	t.Run("declared handler without module fails closed", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, "", map[metadata.FormEventType]string{
			metadata.FormEventBeforeClose: "НетТакойПроцедуры",
		}, nil)
		resp := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "cross", "x")))
		if resp.OK || resp.Close == nil || resp.Close.Allowed || !strings.Contains(resp.Error, "НетТакойПроцедуры") {
			t.Fatalf("declared handler without module did not fail closed: %+v", resp)
		}
	})
}

func TestManagedFormCloseIntentCannotBeForgedThroughFormEvent(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Сообщить("не должно выполняться");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := url.Values{"_event": {string(metadata.FormEventBeforeClose)}, "_kind": {"object"}}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
		t.Fatalf("lifecycle event was remotely forged through /form-event: %+v", resp)
	}
}

func TestManagedFormCloseIntentReplayAndConflict(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Объект.Записать();
	Сообщить("PII");
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "A"}, ent); err != nil {
		t.Fatal(err)
	}
	intentID := uuid.NewString()
	body := closeIntentBody(intentID, "programmatic", "A")
	body.Set("_id", id.String())
	body.Set("_version", "1")
	body.Set("_close_mode", "save")
	first := executeFormCloseIntent(t, srv, ent, body)
	second := executeFormCloseIntent(t, srv, ent, body)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("exact replay failed: first=%d/%s second=%d/%s", first.Code, first.Body, second.Code, second.Body)
	}
	recovered := decodeCloseIntentResponse(t, second)
	if recovered.Close == nil || recovered.Close.Allowed || !recovered.Close.Saved || !recovered.Close.Reload ||
		recovered.Version != 3 || len(recovered.Values) != 0 || len(recovered.Messages) != 0 {
		t.Fatalf("exact replay was not a safe durable recovery tombstone: %+v", recovered)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := row["_version"]; got != int64(3) {
		t.Fatalf("replayed canonical save/BeforeClose executed more than once: version=%v row=%v", got, row)
	}

	// The only changed payload field is the mode. It is part of the replay
	// fingerprint and must conflict before either save or BeforeClose runs.
	changed := make(url.Values, len(body))
	for key, values := range body {
		changed[key] = append([]string(nil), values...)
	}
	changed.Set("_close_mode", "discard")
	conflict := executeFormCloseIntent(t, srv, ent, changed)
	resp := decodeCloseIntentResponse(t, conflict)
	if conflict.Code != http.StatusConflict || resp.OK || resp.Close == nil || resp.Close.Allowed {
		t.Fatalf("same intent with another payload was accepted: status=%d response=%+v", conflict.Code, resp)
	}
}

func TestManagedFormCloseIntentRejectsInvalidProtocolAndSignature(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Первый, Второй)
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)

	badReason := closeIntentBody(uuid.NewString(), "forged", "x")
	badReasonResp := executeFormCloseIntent(t, srv, ent, badReason)
	if badReasonResp.Code != http.StatusBadRequest || decodeCloseIntentResponse(t, badReasonResp).Close.Allowed {
		t.Fatalf("unknown reason was accepted: %d %s", badReasonResp.Code, badReasonResp.Body)
	}

	badSig := executeFormCloseIntent(t, srv, ent, closeIntentBody(uuid.NewString(), "cross", "x"))
	resp := decodeCloseIntentResponse(t, badSig)
	if resp.OK || resp.Close == nil || resp.Close.Allowed || !strings.Contains(resp.Error, "0 или 1") {
		t.Fatalf("invalid BeforeClose signature was accepted: %+v", resp)
	}
}

func TestManagedFormCloseIntentUsesDSLDecimalTruthiness(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие(Отказ)
	Если Объект.Наименование = "zero" Тогда
		Отказ = 0;
	Иначе
		Отказ = 0.5;
	КонецЕсли;
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)

	zero := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent,
		closeIntentBody(uuid.NewString(), "cross", "zero")))
	if !zero.OK || zero.Close == nil || !zero.Close.Allowed {
		t.Fatalf("decimal zero must be false in DSL truthiness: %+v", zero)
	}

	nonzero := decodeCloseIntentResponse(t, executeFormCloseIntent(t, srv, ent,
		closeIntentBody(uuid.NewString(), "cross", "nonzero")))
	if !nonzero.OK || nonzero.Close == nil || nonzero.Close.Allowed {
		t.Fatalf("non-zero decimal must be true in DSL truthiness: %+v", nonzero)
	}
}

func TestProcessorFormCloseIntentUsesOutputCancelAndReason(t *testing.T) {
	program := mustParse(t, `
Процедура ПроверитьЗакрытие(Cancel)
	Сообщить(CloseReason);
	Если Объект.Имя = "" Тогда
		Cancel = Истина;
	КонецЕсли;
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеИмя", DataPath: "Объект.Имя",
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = program
	proc := &processor.Processor{
		Name: "ЗакрываемаяОбработка", Params: []processor.Param{{Name: "Имя", Type: "string"}},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	body := processorCloseIntentBody(proc, uuid.NewString())
	body.Set("Имя", "")
	body.Set(processorServiceFieldName(proc.Params, "_close_reason"), "popup_cancel")

	response := decodeCloseIntentResponse(t, executeProcessorCloseIntent(t, srv, proc, body))
	if !response.OK || response.Close == nil || response.Close.Allowed {
		t.Fatalf("processor output Cancel did not keep the form open: %+v", response)
	}
	if strings.Join(response.Messages, "|") != "popup_cancel" {
		t.Fatalf("processor close reason was not injected: %v", response.Messages)
	}
}

func TestProcessorFormCloseIntentExactDuplicatePrecedesConcurrencyGate(t *testing.T) {
	program := mustParse(t, `
Процедура ПроверитьЗакрытие()
	Приостановить(1);
	Сообщить("готово");
КонецПроцедуры
`)
	form := processorExecutionForm()
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = program
	proc := &processor.Processor{Name: "CloseSingleFlight", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	srv.cfg.Limits.ProcessorConcurrency = 1
	srv.cfg.Limits.RequestTimeoutSec = 3

	body := processorCloseIntentBody(proc, uuid.NewString())
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- executeProcessorCloseIntent(t, srv, proc, body) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.closeIntentMu.Lock()
		ledger := srv.closeIntents
		srv.closeIntentMu.Unlock()
		pending := false
		if ledger != nil {
			ledger.mu.Lock()
			for _, entry := range ledger.entries {
				pending = pending || !entry.done
			}
			ledger.mu.Unlock()
		}
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first close request did not publish its single-flight reservation")
		}
		time.Sleep(5 * time.Millisecond)
	}

	second := executeProcessorCloseIntent(t, srv, proc, body)
	var first *httptest.ResponseRecorder
	select {
	case first = <-firstDone:
	case <-time.After(4 * time.Second):
		t.Fatal("first close request did not finish")
	}
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("exact duplicate was rejected by concurrency gate: first=%d/%s second=%d/%s",
			first.Code, first.Body, second.Code, second.Body)
	}
	secondResponse := decodeCloseIntentResponse(t, second)
	if secondResponse.Close == nil || !secondResponse.Close.Allowed || len(secondResponse.Messages) != 0 || len(secondResponse.Values) != 0 {
		t.Fatalf("single-flight duplicate did not receive a redacted recovery decision: %+v", secondResponse)
	}
}

func TestProcessorFormCloseIntentReplayWaitUsesOperationDeadline(t *testing.T) {
	form := processorExecutionForm()
	proc := &processor.Processor{Name: "CloseReplayDeadline", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	srv.cfg.Limits.RequestTimeoutSec = 1

	intentID := uuid.NewString()
	body := processorCloseIntentBody(proc, intentID)
	seedReq := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	seedReq.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	setProcessorCloseIntentHeaders(seedReq, proc, body)
	if err := parseBoundedForm(seedReq, 32<<20); err != nil {
		t.Fatalf("parse seed request: %v", err)
	}
	hash, err := formCloseRequestFingerprint(seedReq)
	if err != nil {
		t.Fatalf("fingerprint seed request: %v", err)
	}
	identity := formCloseIdentity(seedReq)
	routeKey := "processor|" + strings.ToLower(proc.Name)
	ledger := srv.formCloseLedger()
	reservation, replay, err := ledger.reserve(context.Background(), identity,
		identity+"\x00"+routeKey+"|"+intentID, hash)
	if err != nil || reservation == nil || replay != nil {
		t.Fatalf("seed pending replay: reservation=%v replay=%v err=%v", reservation, replay, err)
	}
	defer reservation.complete(formCloseReplayResult{status: http.StatusOK, body: []byte("done")})

	started := time.Now()
	rec := executeProcessorCloseIntent(t, srv, proc, body)
	elapsed := time.Since(started)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("pending replay ignored operation deadline: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed < 750*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("replay wait elapsed=%v, expected configured one-second operation deadline", elapsed)
	}
	resp := decodeCloseIntentResponse(t, rec)
	if resp.Close == nil || resp.Close.IntentID != intentID || resp.Close.Allowed {
		t.Fatalf("deadline response was not correlated fail-closed: %+v", resp)
	}
}

func TestProcessorFormCloseIntentMultipartFingerprintIncludesFile(t *testing.T) {
	program := mustParse(t, `
Процедура ПроверитьЗакрытие()
КонецПроцедуры
`)
	form := processorExecutionForm(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеДанные", DataPath: "Объект.Данные", Type: "file",
	})
	form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}
	form.ProgramAST = program
	proc := &processor.Processor{
		Name: "CloseMultipartFingerprint", Params: []processor.Param{{Name: "Данные", Type: "file"}},
		Forms: []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	for _, tc := range []struct {
		name           string
		firstFilename  string
		firstContent   string
		secondFilename string
		secondContent  string
	}{
		{name: "content", firstFilename: "data.txt", firstContent: "alpha", secondFilename: "data.txt", secondContent: "beta"},
		{name: "metadata", firstFilename: "first.txt", firstContent: "same", secondFilename: "second.txt", secondContent: "same"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intentID := uuid.NewString()
			envelope := processorCloseIntentBody(proc, intentID)
			firstBody, firstType := processorCloseMultipartBody(t, proc, envelope, tc.firstFilename, tc.firstContent)
			first := executeProcessorCloseIntentRaw(t, srv, proc, envelope, firstType, bytes.NewReader(firstBody))
			if first.Code != http.StatusOK {
				t.Fatalf("initial multipart close failed: status=%d body=%s", first.Code, first.Body.String())
			}

			secondBody, secondType := processorCloseMultipartBody(t, proc, envelope, tc.secondFilename, tc.secondContent)
			second := executeProcessorCloseIntentRaw(t, srv, proc, envelope, secondType, bytes.NewReader(secondBody))
			resp := decodeCloseIntentResponse(t, second)
			if second.Code != http.StatusConflict || resp.Close == nil || resp.Close.Allowed {
				t.Fatalf("changed multipart file replay was accepted: status=%d response=%+v", second.Code, resp)
			}
		})
	}
}

func TestManagedFormCloseIntentReplayBodyLimits(t *testing.T) {
	t.Run("max entry", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Сообщить(Объект.Наименование);
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
		srv.closeIntents = newFormCloseReplayLedgerWithLimits(1024, 4096)
		intentID := uuid.NewString()
		body := closeIntentBody(intentID, "cross", strings.Repeat("x", 2048))

		first := executeFormCloseIntent(t, srv, ent, body)
		resp := decodeCloseIntentResponse(t, first)
		if first.Code != http.StatusInternalServerError || resp.Close == nil || resp.Close.Allowed ||
			!strings.Contains(resp.Error, "размер") {
			t.Fatalf("oversized replay response did not fail closed: status=%d response=%+v", first.Code, resp)
		}
		if int64(first.Body.Len()) > srv.closeIntents.maxEntryBytes {
			t.Fatalf("oversized response escaped per-entry bound: size=%d max=%d", first.Body.Len(), srv.closeIntents.maxEntryBytes)
		}
		replay := executeFormCloseIntent(t, srv, ent, body)
		replayResponse := decodeCloseIntentResponse(t, replay)
		if replay.Code != http.StatusConflict || replayResponse.Close == nil || replayResponse.Close.Allowed ||
			len(replayResponse.Values) != 0 || len(replayResponse.Messages) != 0 {
			t.Fatalf("bounded response replay was not fail-closed/redacted: first=%d/%s replay=%d/%s",
				first.Code, first.Body, replay.Code, replay.Body)
		}
	})

	t.Run("total budget", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Сообщить(Объект.Наименование);
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
		ledger := newFormCloseReplayLedgerWithLimits(4096, 3000)
		srv.closeIntents = ledger
		for range 3 {
			rec := executeFormCloseIntent(t, srv, ent,
				closeIntentBody(uuid.NewString(), "cross", strings.Repeat("y", 1400)))
			if rec.Code != http.StatusOK {
				t.Fatalf("bounded response unexpectedly failed: status=%d body=%s", rec.Code, rec.Body.String())
			}
		}

		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		if ledger.resultBytes > ledger.maxResultBytes {
			t.Fatalf("replay byte budget exceeded: bytes=%d max=%d", ledger.resultBytes, ledger.maxResultBytes)
		}
		if len(ledger.entries) != 3 {
			t.Fatalf("unexpired replay keys were forgotten under byte pressure: entries=%d bytes=%d", len(ledger.entries), ledger.resultBytes)
		}
		for key, entry := range ledger.entries {
			if int64(len(entry.result.body)) > ledger.maxEntryBytes {
				t.Fatalf("entry %q exceeds max body: %d > %d", key, len(entry.result.body), ledger.maxEntryBytes)
			}
		}
	})
}

func TestFormCloseFailureBoundsLabelAndPreservesSavedIdentity(t *testing.T) {
	inv := &formCloseInvocation{
		intentID: uuid.NewString(), saved: true, savedID: uuid.NewString(),
		savedLabel: strings.Repeat("очень длинная подпись", 1000), version: 7,
		formURL: "/ui/catalog/Test/" + uuid.NewString(),
	}
	body := marshalFormCloseFailure(inv, 0, "failure")
	if len(body) > 2048 {
		t.Fatalf("minimal failure is not bounded: %d bytes", len(body))
	}
	var response formEventResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Close == nil || response.Close.IntentID != inv.intentID || !response.Close.Saved ||
		response.Close.Allowed || response.SavedID != inv.savedID || response.Version != 7 {
		t.Fatalf("bounded failure lost durable identity: %+v", response)
	}
}

func TestCaptureFormClosePanicReturnsExactStoredReplay(t *testing.T) {
	srv := &Server{closeIntents: newFormCloseReplayLedgerWithLimits(4096, 8192)}
	var hash [sha256.Size]byte
	hash[0] = 1
	reservation, _, err := srv.closeIntents.reserve(context.Background(), "user", "key", hash)
	if err != nil {
		t.Fatal(err)
	}
	inv := &formCloseInvocation{
		intentID: uuid.NewString(), saved: true, savedID: uuid.NewString(), version: 3,
		reservation: reservation,
	}
	first := httptest.NewRecorder()
	srv.captureFormCloseResponse(first, httptest.NewRequest(http.MethodPost, "/close", nil), inv, func(http.ResponseWriter) {
		panic("boom")
	})
	_, replay, err := srv.closeIntents.reserve(context.Background(), "user", "key", hash)
	if err != nil || replay == nil {
		t.Fatalf("panic result missing from replay ledger: replay=%v err=%v", replay, err)
	}
	var recovered formEventResponse
	if err := json.Unmarshal(replay.body, &recovered); err != nil {
		t.Fatalf("decode panic recovery: %v body=%s", err, replay.body)
	}
	if replay.status != http.StatusOK || recovered.Close == nil || recovered.Close.Allowed ||
		!recovered.Close.Saved || !recovered.Close.Reload || recovered.SavedID != inv.savedID || recovered.Version != 3 {
		t.Fatalf("panic replay is not a safe saved recovery: status=%d response=%+v", replay.status, recovered)
	}
	resp := decodeCloseIntentResponse(t, first)
	if resp.Close == nil || !resp.Close.Saved || resp.SavedID != inv.savedID || resp.Version != 3 {
		t.Fatalf("panic response lost saved identity: %+v", resp)
	}
}

func TestCaptureFormCloseAccessCheckFailureIsIdentityFreeReconcile(t *testing.T) {
	srv := &Server{closeIntents: newFormCloseReplayLedgerWithLimits(4096, 8192)}
	hash := sha256.Sum256([]byte("access failure"))
	reservation, _, err := srv.closeIntents.reserve(context.Background(), "user", "key", hash)
	if err != nil {
		t.Fatal(err)
	}
	inv := &formCloseInvocation{
		intentID: uuid.NewString(), saved: true, savedID: uuid.NewString(), savedLabel: "secret label",
		version: 4, formURL: "/ui/catalog/Secret/" + uuid.NewString(), reservation: reservation,
	}
	inv.recheckAccess = func() {
		inv.suppressState = true
		inv.accessCheckFailed = true
	}
	first := httptest.NewRecorder()
	srv.captureFormCloseResponse(first, httptest.NewRequest(http.MethodPost, "/close", nil), inv, func(w http.ResponseWriter) {
		respondJSON(json.NewEncoder(w), formEventResponse{
			OK: true, Values: map[string]any{"Secret": "must not leak"},
			SavedID: inv.savedID, SavedLabel: inv.savedLabel, Version: inv.version,
		})
	})
	response := decodeCloseIntentResponse(t, first)
	if first.Code != http.StatusInternalServerError || response.Close == nil || !response.Close.Reconcile ||
		response.Close.Allowed || response.Close.Saved || response.SavedID != "" || response.SavedLabel != "" ||
		response.Version != 0 || len(response.Values) != 0 || response.Close.FormURL != "" {
		t.Fatalf("technical final access failure leaked durable identity: status=%d response=%+v", first.Code, response)
	}

	_, replay, err := srv.closeIntents.reserve(context.Background(), "user", "key", hash)
	if err != nil || replay == nil {
		t.Fatalf("identity-free recovery missing: replay=%v err=%v", replay, err)
	}
	var recovered formEventResponse
	if err := json.Unmarshal(replay.body, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Close == nil || !recovered.Close.Reconcile || recovered.SavedID != "" ||
		recovered.Version != 0 || recovered.Close.Saved || recovered.Close.FormURL != "" {
		t.Fatalf("stored access-failure recovery leaked identity: %+v", recovered)
	}
}

func TestFormCloseTerminalDecisionClearsReloadAndReconcile(t *testing.T) {
	intentID := uuid.NewString()
	rich := formEventResponse{
		OK: true, SavedID: uuid.NewString(), Version: 7,
		Close: &formCloseDecision{
			IntentID: intentID, Allowed: false, Saved: true,
			Reload: true, Reconcile: true,
		},
	}
	body, err := json.Marshal(rich)
	if err != nil {
		t.Fatal(err)
	}
	result := formCloseReplayResult{status: http.StatusConflict, body: append(body, '\n')}
	inv := &formCloseInvocation{intentID: intentID, terminal: true, reconcile: true, forceReload: true}

	for name, transformed := range map[string]formCloseReplayResult{
		"sanitized": sanitizeFormCloseReplay(inv, result),
		"compacted": compactStoredFormCloseReplay(formCloseReplayResult{
			status: result.status,
			body: func() []byte {
				rich.Close.Terminal = true
				encoded, marshalErr := json.Marshal(rich)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				return append(encoded, '\n')
			}(),
		}),
	} {
		t.Run(name, func(t *testing.T) {
			var response formEventResponse
			if err := json.Unmarshal(transformed.body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Close == nil || !response.Close.Terminal || !response.Close.Allowed ||
				response.Close.Reload || response.Close.Reconcile {
				t.Fatalf("terminal flags are not mutually exclusive: %+v", response)
			}
		})
	}

	var failure formEventResponse
	if err := json.Unmarshal(marshalFormCloseFailure(inv, 7, "failure"), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Close == nil || !failure.Close.Terminal || failure.Close.Reload || failure.Close.Reconcile {
		t.Fatalf("terminal failure kept recovery flags: %+v", failure)
	}

	t.Run("technical access failure overrides cached terminal", func(t *testing.T) {
		cached := rich
		cached.Close = &formCloseDecision{
			IntentID: intentID, Allowed: true, Terminal: true,
		}
		cachedBody, marshalErr := json.Marshal(cached)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		accessFailure := sanitizeFormCloseReplay(
			&formCloseInvocation{intentID: intentID, accessCheckFailed: true},
			formCloseReplayResult{status: http.StatusOK, body: append(cachedBody, '\n')},
		)
		var response formEventResponse
		if err := json.Unmarshal(accessFailure.body, &response); err != nil {
			t.Fatal(err)
		}
		if accessFailure.status != http.StatusInternalServerError || response.Close == nil ||
			response.Close.Terminal || response.Close.Allowed || !response.Close.Reconcile {
			t.Fatalf("technical access failure kept contradictory terminal flags: status=%d response=%+v",
				accessFailure.status, response)
		}
	})
}

func TestFormCloseReplayLedgerWaitsForConcurrentExactRequest(t *testing.T) {
	ledger := newFormCloseReplayLedger()
	hash := sha256.Sum256([]byte("same form state"))
	reservation, replay, err := ledger.reserve(context.Background(), "user", "user\x00route|intent", hash)
	if err != nil || reservation == nil || replay != nil {
		t.Fatalf("initial reserve: reservation=%v replay=%v err=%v", reservation, replay, err)
	}

	type outcome struct {
		replay *formCloseReplayResult
		err    error
	}
	ready := make(chan struct{})
	result := make(chan outcome, 1)
	var once sync.Once
	go func() {
		once.Do(func() { close(ready) })
		_, got, gotErr := ledger.reserve(context.Background(), "user", "user\x00route|intent", hash)
		result <- outcome{replay: got, err: gotErr}
	}()
	<-ready
	select {
	case got := <-result:
		t.Fatalf("concurrent exact request returned before completion: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	want := formCloseReplayResult{status: http.StatusOK, header: http.Header{"X-Test": {"yes"}}, body: []byte("terminal")}
	reservation.complete(want)
	select {
	case got := <-result:
		if got.err != nil || got.replay == nil || got.replay.status != want.status || string(got.replay.body) != string(want.body) {
			t.Fatalf("concurrent replay mismatch: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent exact request did not wake after completion")
	}
}

func TestManagedFormCloseIntentAnonymousClientUUIDCannotBypassActorCapacity(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	srv.closeIntents = newFormCloseReplayLedger()
	srv.closeIntents.maxActorEntries = 2

	router := chi.NewRouter()
	srv.Mount(router)
	execute := func(body url.Values, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
		t.Helper()
		ensureEntityCloseIntentSchema(ent, body)
		kind := strings.ToLower(string(ent.Kind))
		req := httptest.NewRequest(http.MethodPost,
			"/ui/"+kind+"/"+ent.Name+"/form-close-intent", strings.NewReader(body.Encode()))
		req.RemoteAddr = remoteAddr
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		req.Header.Set("X-Forwarded-For", forwardedFor)
		setEntityCloseIntentHeaders(req, body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	first := closeIntentBody(uuid.NewString(), "close", "")
	second := closeIntentBody(uuid.NewString(), "close", "")
	for i, body := range []url.Values{first, second} {
		if rec := execute(body, "203.0.113.10:4100", fmt.Sprintf("198.51.100.%d", i+1)); rec.Code != http.StatusOK {
			t.Fatalf("anonymous intent %d was rejected before actor capacity: status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	// A new browser-provided client UUID and a forged forwarding address must
	// not create a new anonymous capacity bucket for the same TCP peer.
	third := closeIntentBody(uuid.NewString(), "close", "")
	blocked := execute(third, "203.0.113.10:4999", "192.0.2.250")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("rotated anonymous close client bypassed actor capacity: status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	// Admission identity is supplemental only. An exact retained UUID/body can
	// still be replayed after a network change, which is the safety property the
	// client UUID exists to provide.
	replayed := execute(first, "198.51.100.77:5100", "203.0.113.10")
	if replayed.Code != http.StatusOK {
		t.Fatalf("exact replay lost mobility after actor quota split: status=%d body=%s", replayed.Code, replayed.Body.String())
	}
}

func TestFormCloseReplayLedgerNeverForgetsUnexpiredKeyUnderBytePressure(t *testing.T) {
	ledger := newFormCloseReplayLedgerWithLimits(4096, 900)
	makeResult := func(intentID string) formCloseReplayResult {
		body, err := json.Marshal(formEventResponse{
			OK: true, Messages: []string{strings.Repeat("sensitive", 80)},
			Close: &formCloseDecision{IntentID: intentID, Allowed: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return formCloseReplayResult{status: http.StatusOK, header: make(http.Header), body: append(body, '\n')}
	}
	hashA := sha256.Sum256([]byte("A"))
	resA, _, err := ledger.reserve(context.Background(), "user", "user\x00A", hashA)
	if err != nil {
		t.Fatal(err)
	}
	resA.complete(makeResult("A"))
	hashB := sha256.Sum256([]byte("B"))
	resB, _, err := ledger.reserve(context.Background(), "user", "user\x00B", hashB)
	if err != nil {
		t.Fatal(err)
	}
	resB.complete(makeResult("B"))

	reservation, replay, err := ledger.reserve(context.Background(), "user", "user\x00A", hashA)
	if err != nil || reservation != nil || replay == nil {
		t.Fatalf("unexpired A became executable again under pressure: reservation=%v replay=%v err=%v", reservation, replay, err)
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if len(ledger.entries) != 2 || ledger.resultBytes > ledger.maxResultBytes {
		t.Fatalf("ledger did not retain bounded tombstones: entries=%d bytes=%d max=%d",
			len(ledger.entries), ledger.resultBytes, ledger.maxResultBytes)
	}
}

func TestFormCloseReplayTTLStartsAtCompletion(t *testing.T) {
	ledger := newFormCloseReplayLedger()
	hash := sha256.Sum256([]byte("long handler"))
	reservation, _, err := ledger.reserve(context.Background(), "user", "key", hash)
	if err != nil {
		t.Fatal(err)
	}
	ledger.mu.Lock()
	reservation.entry.created = time.Now().Add(-2 * formCloseReplayTTL)
	ledger.mu.Unlock()
	reservation.complete(formCloseReplayResult{status: http.StatusOK, body: []byte("done")})

	newReservation, replay, err := ledger.reserve(context.Background(), "user", "key", hash)
	if err != nil || newReservation != nil || replay == nil {
		t.Fatalf("freshly completed long handler was immediately pruned: reservation=%v replay=%v err=%v",
			newReservation, replay, err)
	}
}

func TestFormCloseReplayLedgerCapsConcurrentWaiters(t *testing.T) {
	ledger := newFormCloseReplayLedger()
	ledger.maxWaiters = 1
	ledger.waitTimeout = time.Second
	hash := sha256.Sum256([]byte("same"))
	reservation, _, err := ledger.reserve(context.Background(), "user", "key", hash)
	if err != nil {
		t.Fatal(err)
	}
	waiterDone := make(chan error, 1)
	go func() {
		_, _, waitErr := ledger.reserve(context.Background(), "user", "key", hash)
		waiterDone <- waitErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		ledger.mu.Lock()
		waiters := reservation.entry.waiters
		ledger.mu.Unlock()
		if waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first replay waiter was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if _, _, err := ledger.reserve(context.Background(), "user", "key", hash); !errors.Is(err, errFormCloseReplayWaitersFull) {
		t.Fatalf("waiter cap did not reject excess duplicate: %v", err)
	}
	reservation.complete(formCloseReplayResult{status: http.StatusOK, body: []byte("done")})
	select {
	case err := <-waiterDone:
		if err != nil {
			t.Fatalf("admitted waiter did not receive completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("admitted waiter remained blocked")
	}
	if newReservation, replay, err := ledger.reserve(context.Background(), "user", "key", hash); err != nil || newReservation != nil || replay == nil || string(replay.body) != "done" {
		t.Fatalf("excess duplicate lost its original intent after completion: reservation=%v replay=%+v err=%v",
			newReservation, replay, err)
	}
}

func TestManagedFormCloseIntentReplaySurvivesSessionRotation(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, "", nil, nil)
	body := closeIntentBody(uuid.NewString(), "cross", "browser state")
	ensureEntityCloseIntentSchema(entity, body)
	user := &auth.User{ID: "stable-user-id", Login: "same-login"}
	execute := func(session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/form-close-intent",
			strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setEntityCloseIntentHeaders(req, body)
		req.AddCookie(&http.Cookie{Name: "onebase_session", Value: session})
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		srv.Mount(router)
		router.ServeHTTP(recorder, req)
		return recorder
	}

	first := execute("session-a")
	second := execute("session-b")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("session rotation broke exact replay: first=%d/%s second=%d/%s",
			first.Code, first.Body, second.Code, second.Body)
	}
	replayed := decodeCloseIntentResponse(t, second)
	if replayed.Close == nil || !replayed.Close.Allowed {
		t.Fatalf("rotated session did not recover the original decision: %+v", replayed)
	}
	ledger := srv.formCloseLedger()
	ledger.mu.Lock()
	entries := len(ledger.entries)
	ledger.mu.Unlock()
	if entries != 1 {
		t.Fatalf("session rotation created a second replay reservation: entries=%d", entries)
	}
}

func TestManagedFormCloseIntentOpenAccessReplaySurvivesIPChange(t *testing.T) {
	srv, entity := setupManagedEventsServer(t, "", nil, nil)
	body := closeIntentBody(uuid.NewString(), "cross", "browser state")
	ensureEntityCloseIntentSchema(entity, body)
	execute := func(remoteAddr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/ui/"+strings.ToLower(string(entity.Kind))+"/"+entity.Name+"/form-close-intent",
			strings.NewReader(body.Encode()))
		req.RemoteAddr = remoteAddr
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		setEntityCloseIntentHeaders(req, body)
		recorder := httptest.NewRecorder()
		router := chi.NewRouter()
		srv.Mount(router)
		router.ServeHTTP(recorder, req)
		return recorder
	}

	first := execute("192.0.2.10:12000")
	second := execute("198.51.100.20:24000")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("open-access IP change broke exact replay: first=%d/%s second=%d/%s",
			first.Code, first.Body, second.Code, second.Body)
	}
	replayed := decodeCloseIntentResponse(t, second)
	if replayed.Close == nil || !replayed.Close.Allowed {
		t.Fatalf("open-access retry did not recover original decision: %+v", replayed)
	}
	ledger := srv.formCloseLedger()
	ledger.mu.Lock()
	entries := len(ledger.entries)
	ledger.mu.Unlock()
	if entries != 1 {
		t.Fatalf("IP change created a second replay reservation: entries=%d", entries)
	}
}

func TestManagedFormCloseIntentRejectsStaleProcessProofWithoutExecuting(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Объект.Записать();
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
	body := closeIntentBody(uuid.NewString(), "close", "must not save")
	body.Set("_close_epoch", uuid.NewString())
	recorder := executeFormCloseIntent(t, srv, ent, body)
	response := decodeCloseIntentResponse(t, recorder)
	if recorder.Code != http.StatusConflict || response.Close == nil || !response.Close.Reconcile || response.Close.Allowed {
		t.Fatalf("stale process proof was not correlated/fenced: status=%d response=%+v", recorder.Code, response)
	}
	rows, err := srv.store.List(context.Background(), ent.Name, ent, storage.ListParams{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale process proof executed handler: rows=%v err=%v", rows, err)
	}
}
