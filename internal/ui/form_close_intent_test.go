package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func closeIntentBody(intentID, reason, name string) url.Values {
	return url.Values{
		"_close_intent_id": {intentID},
		"_close_reason":    {reason},
		"_close_mode":      {"discard"},
		"_kind":            {"object"},
		"Наименование":     {name},
	}
}

func executeFormCloseIntent(t *testing.T, s *Server, ent *metadata.Entity, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+ent.Name+"/form-close-intent", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
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
	return executeProcessorCloseIntentRaw(t, s, proc, "application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
}

func executeProcessorCloseIntentRaw(t *testing.T, s *Server, proc *processor.Processor, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-close-intent", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	s.Mount(router)
	router.ServeHTTP(rec, req)
	return rec
}

func processorCloseIntentBody(proc *processor.Processor, intentID string) url.Values {
	body := url.Values{}
	body.Set(processorServiceFieldName(proc.Params, "_close_intent_id"), intentID)
	body.Set(processorServiceFieldName(proc.Params, "_close_reason"), "cross")
	body.Set(processorServiceFieldName(proc.Params, "_close_mode"), "discard")
	return body
}

func processorCloseMultipartBody(t *testing.T, proc *processor.Processor, intentID, filename, content string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, values := range processorCloseIntentBody(proc, intentID) {
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
	first := executeFormCloseIntent(t, srv, ent, body)
	second := executeFormCloseIntent(t, srv, ent, body)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("exact replay changed terminal response: first=%d/%s second=%d/%s", first.Code, first.Body, second.Code, second.Body)
	}
	row, err := srv.store.GetByID(context.Background(), ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if got := row["_version"]; got != int64(2) {
		t.Fatalf("replayed handler executed more than once: version=%v row=%v", got, row)
	}

	changed := closeIntentBody(intentID, "programmatic", "B")
	changed.Set("_id", id.String())
	changed.Set("_version", "2")
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
	body := url.Values{"Имя": {""}}
	body.Set(processorServiceFieldName(proc.Params, "_close_intent_id"), uuid.NewString())
	body.Set(processorServiceFieldName(proc.Params, "_close_reason"), "popup_cancel")
	body.Set(processorServiceFieldName(proc.Params, "_close_mode"), "discard")

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
	if first.Body.String() != second.Body.String() {
		t.Fatalf("single-flight duplicate did not replay exact result: first=%s second=%s", first.Body, second.Body)
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
	if err := parseBoundedForm(seedReq, 32<<20); err != nil {
		t.Fatalf("parse seed request: %v", err)
	}
	hash, err := formCloseRequestFingerprint(seedReq)
	if err != nil {
		t.Fatalf("fingerprint seed request: %v", err)
	}
	identity := formCloseIdentity(seedReq)
	routeKey := "processor|" + strings.ToLower(proc.Name) + "|" + strings.ToLower(form.Name)
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
			firstBody, firstType := processorCloseMultipartBody(t, proc, intentID, tc.firstFilename, tc.firstContent)
			first := executeProcessorCloseIntentRaw(t, srv, proc, firstType, bytes.NewReader(firstBody))
			if first.Code != http.StatusOK {
				t.Fatalf("initial multipart close failed: status=%d body=%s", first.Code, first.Body.String())
			}

			secondBody, secondType := processorCloseMultipartBody(t, proc, intentID, tc.secondFilename, tc.secondContent)
			second := executeProcessorCloseIntentRaw(t, srv, proc, secondType, bytes.NewReader(secondBody))
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
		if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
			t.Fatalf("bounded terminal response was not replayed exactly: first=%d/%s replay=%d/%s",
				first.Code, first.Body, replay.Code, replay.Body)
		}
	})

	t.Run("total budget", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, `
Процедура ПроверитьЗакрытие()
	Сообщить(Объект.Наименование);
КонецПроцедуры
`, map[metadata.FormEventType]string{metadata.FormEventBeforeClose: "ПроверитьЗакрытие"}, nil)
		ledger := newFormCloseReplayLedgerWithLimits(4096, 5000)
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
		if len(ledger.entries) >= 3 {
			t.Fatalf("completed replay entries were not evicted for byte budget: entries=%d bytes=%d", len(ledger.entries), ledger.resultBytes)
		}
		for key, entry := range ledger.entries {
			if int64(len(entry.result.body)) > ledger.maxEntryBytes {
				t.Fatalf("entry %q exceeds max body: %d > %d", key, len(entry.result.body), ledger.maxEntryBytes)
			}
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
