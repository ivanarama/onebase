package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

const (
	formCloseReplayTTL        = 10 * time.Minute
	formCloseReplayPerUser    = 128
	formCloseReplayGlobal     = 2048
	formCloseReplayMaxEntry   = int64(4 << 20)
	formCloseReplayByteBudget = int64(64 << 20)
	formCloseDefaultClientMS  = 30_000
)

var formCloseReasons = map[string]bool{
	"cross":          true,
	"escape":         true,
	"close":          true,
	"ok":             true,
	"post_and_close": true,
	"programmatic":   true,
	"popup_cancel":   true,
}

// formCloseDecision is appended to the ordinary managed-form event response.
// The client applies values/messages first and destroys UI only when this
// decision carries its exact intent id and allowed=true.
type formCloseDecision struct {
	IntentID string `json:"intentId"`
	Allowed  bool   `json:"allowed"`
	Saved    bool   `json:"saved"`
	FormURL  string `json:"formUrl,omitempty"`
}

type formCloseInvocation struct {
	processor bool
	intentID  string
	reason    string
	mode      string
	cancelled bool

	reservation *formCloseReservation
	replay      *formCloseReplayResult
}

type formCloseHTTPError struct {
	status int
	msg    string
}

func (e *formCloseHTTPError) Error() string { return e.msg }

func resolveFormCloseHandler(form *metadata.FormModule) string {
	if form == nil {
		return ""
	}
	return strings.TrimSpace(form.Handlers[metadata.FormEventBeforeClose])
}

func validateFormCloseProcedure(decl *ast.ProcedureDecl) ([]any, error) {
	if decl == nil {
		return nil, errors.New("процедура ПередЗакрытием не найдена в .form.os")
	}
	switch len(decl.Params) {
	case 0:
		return nil, nil
	case 1:
		name := strings.TrimSpace(decl.Params[0].Literal)
		if strings.EqualFold(name, "Отказ") || strings.EqualFold(name, "Cancel") {
			return []any{false}, nil
		}
		return nil, fmt.Errorf("ПередЗакрытием: единственный параметр должен называться Отказ или Cancel, получено %q", name)
	default:
		return nil, fmt.Errorf("ПередЗакрытием поддерживает 0 или 1 параметр, получено %d", len(decl.Params))
	}
}

func formCloseBindingCancelled(decl *ast.ProcedureDecl, bindings map[string]any) bool {
	if decl == nil || len(decl.Params) != 1 {
		return false
	}
	v := bindings[decl.Params[0].Literal]
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return value != ""
	case int:
		return value != 0
	case int8:
		return value != 0
	case int16:
		return value != 0
	case int32:
		return value != 0
	case int64:
		return value != 0
	case uint:
		return value != 0
	case uint8:
		return value != 0
	case uint16:
		return value != 0
	case uint32:
		return value != 0
	case uint64:
		return value != 0
	case float32:
		return value != 0
	case float64:
		return value != 0
	case decimal.Decimal:
		return !value.IsZero()
	case *decimal.Decimal:
		// DSL truthiness treats a nil decimal pointer as an unknown non-nil
		// value (numericZero cannot convert it), and therefore as true.
		return value == nil || !value.IsZero()
	default:
		return v != nil
	}
}

func (s *Server) withFormCloseOperationDeadline(r *http.Request, kind string) (*http.Request, context.CancelFunc) {
	timeout := s.operationTimeout(kind)
	if timeout <= 0 {
		return r, func() {}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return r.WithContext(ctx), cancel
}

func (s *Server) prepareFormCloseInvocation(r *http.Request, inv *formCloseInvocation, routeKey string, value func(string) string) error {
	rawIntent := strings.TrimSpace(value("_close_intent_id"))
	id, err := uuid.Parse(rawIntent)
	if err != nil {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "некорректный _close_intent_id"}
	}
	inv.intentID = id.String()
	inv.reason = strings.TrimSpace(value("_close_reason"))
	if !formCloseReasons[inv.reason] {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "некорректная _close_reason"}
	}
	inv.mode = strings.TrimSpace(value("_close_mode"))
	// Slice A closes without saving. Save/post modes are introduced by the next
	// Plan 181 slice together with the three-way dirty dialog and shared save
	// service; accepting them early would silently discard data.
	if inv.mode != "discard" {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "режим закрытия пока поддерживает только discard"}
	}

	payloadHash, err := formCloseRequestFingerprint(r)
	if err != nil {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "не удалось прочитать состояние формы для проверки закрытия"}
	}
	identity := formCloseIdentity(r)
	// Identity is part of the replay key: two users may independently generate
	// the same UUID without observing or blocking one another. The identity is
	// also retained on the entry for the per-user capacity bound.
	replayKey := identity + "\x00" + routeKey + "|" + inv.intentID
	reservation, replay, err := s.formCloseLedger().reserve(r.Context(), identity, replayKey, payloadHash)
	if err != nil {
		if errors.Is(err, errFormCloseReplayConflict) {
			return &formCloseHTTPError{status: http.StatusConflict, msg: "_close_intent_id уже использован с другим состоянием формы"}
		}
		if errors.Is(err, errFormCloseReplayFull) {
			return &formCloseHTTPError{status: http.StatusTooManyRequests, msg: "слишком много незавершённых запросов закрытия"}
		}
		return &formCloseHTTPError{status: http.StatusRequestTimeout, msg: "ожидание повторного запроса закрытия отменено"}
	}
	inv.reservation = reservation
	inv.replay = replay
	return nil
}

// formCloseRequestFingerprint hashes the logical form payload rather than the
// raw HTTP body. Multipart boundaries may legitimately change on a retry, but
// every value, file byte and file-part header must remain identical for an
// intent id to be replayed.
func formCloseRequestFingerprint(r *http.Request) ([sha256.Size]byte, error) {
	h := sha256.New()
	writeFormCloseFingerprintString(h, "onebase-form-close-v2")

	valueKeys := make([]string, 0, len(r.PostForm))
	for key := range r.PostForm {
		valueKeys = append(valueKeys, key)
	}
	sort.Strings(valueKeys)
	for _, key := range valueKeys {
		writeFormCloseFingerprintString(h, "value")
		writeFormCloseFingerprintString(h, key)
		values := r.PostForm[key]
		writeFormCloseFingerprintUint64(h, uint64(len(values)))
		for _, value := range values {
			writeFormCloseFingerprintString(h, value)
		}
	}

	if r.MultipartForm != nil {
		fileKeys := make([]string, 0, len(r.MultipartForm.File))
		for key := range r.MultipartForm.File {
			fileKeys = append(fileKeys, key)
		}
		sort.Strings(fileKeys)
		for _, key := range fileKeys {
			writeFormCloseFingerprintString(h, "file-field")
			writeFormCloseFingerprintString(h, key)
			files := r.MultipartForm.File[key]
			writeFormCloseFingerprintUint64(h, uint64(len(files)))
			for _, fileHeader := range files {
				if fileHeader == nil {
					writeFormCloseFingerprintString(h, "nil-file")
					continue
				}
				writeFormCloseFingerprintString(h, fileHeader.Filename)
				writeFormCloseFingerprintString(h, strconv.FormatInt(fileHeader.Size, 10))

				headerKeys := make([]string, 0, len(fileHeader.Header))
				for headerKey := range fileHeader.Header {
					headerKeys = append(headerKeys, headerKey)
				}
				sort.Strings(headerKeys)
				writeFormCloseFingerprintUint64(h, uint64(len(headerKeys)))
				for _, headerKey := range headerKeys {
					writeFormCloseFingerprintString(h, headerKey)
					headerValues := fileHeader.Header[headerKey]
					writeFormCloseFingerprintUint64(h, uint64(len(headerValues)))
					for _, headerValue := range headerValues {
						writeFormCloseFingerprintString(h, headerValue)
					}
				}

				file, err := fileHeader.Open()
				if err != nil {
					return [sha256.Size]byte{}, err
				}
				contentHash := sha256.New()
				size, copyErr := io.Copy(contentHash, file)
				closeErr := file.Close()
				if copyErr != nil {
					return [sha256.Size]byte{}, copyErr
				}
				if closeErr != nil {
					return [sha256.Size]byte{}, closeErr
				}
				writeFormCloseFingerprintString(h, strconv.FormatInt(size, 10))
				_, _ = h.Write(contentHash.Sum(nil))
			}
		}
	}

	var result [sha256.Size]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

func writeFormCloseFingerprintString(w io.Writer, value string) {
	writeFormCloseFingerprintUint64(w, uint64(len(value)))
	_, _ = io.WriteString(w, value)
}

func writeFormCloseFingerprintUint64(w io.Writer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = w.Write(encoded[:])
}

func formCloseIdentity(r *http.Request) string {
	var parts []string
	if user := auth.UserFromContext(r.Context()); user != nil {
		parts = append(parts, user.ID, user.Login)
	}
	if cookie, err := r.Cookie("onebase_session"); err == nil && cookie.Value != "" {
		sum := sha256.Sum256([]byte(cookie.Value))
		parts = append(parts, hex.EncodeToString(sum[:]))
	}
	if len(parts) == 0 {
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		parts = append(parts, remote)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

type formCloseReplayResult struct {
	status int
	header http.Header
	body   []byte
}

var (
	errFormCloseReplayConflict = errors.New("close-intent replay payload conflict")
	errFormCloseReplayFull     = errors.New("close-intent replay ledger full")
)

type formCloseReplayEntry struct {
	identity string
	hash     [sha256.Size]byte
	created  time.Time
	ready    chan struct{}
	done     bool
	result   formCloseReplayResult
}

type formCloseReplayLedger struct {
	mu             sync.Mutex
	entries        map[string]*formCloseReplayEntry
	resultBytes    int64
	maxEntryBytes  int64
	maxResultBytes int64
}

type formCloseReservation struct {
	ledger *formCloseReplayLedger
	key    string
	entry  *formCloseReplayEntry
}

func newFormCloseReplayLedger() *formCloseReplayLedger {
	return newFormCloseReplayLedgerWithLimits(formCloseReplayMaxEntry, formCloseReplayByteBudget)
}

func newFormCloseReplayLedgerWithLimits(maxEntryBytes, maxResultBytes int64) *formCloseReplayLedger {
	if maxEntryBytes <= 0 {
		maxEntryBytes = formCloseReplayMaxEntry
	}
	if maxResultBytes <= 0 {
		maxResultBytes = formCloseReplayByteBudget
	}
	if maxEntryBytes > maxResultBytes {
		maxEntryBytes = maxResultBytes
	}
	return &formCloseReplayLedger{
		entries:        make(map[string]*formCloseReplayEntry),
		maxEntryBytes:  maxEntryBytes,
		maxResultBytes: maxResultBytes,
	}
}

func (s *Server) formCloseLedger() *formCloseReplayLedger {
	s.closeIntentMu.Lock()
	defer s.closeIntentMu.Unlock()
	if s.closeIntents == nil {
		s.closeIntents = newFormCloseReplayLedger()
	}
	return s.closeIntents
}

func (l *formCloseReplayLedger) reserve(ctx context.Context, identity, key string, hash [sha256.Size]byte) (*formCloseReservation, *formCloseReplayResult, error) {
	now := time.Now()
	l.mu.Lock()
	l.pruneLocked(now)
	if entry := l.entries[key]; entry != nil {
		if entry.hash != hash || entry.identity != identity {
			l.mu.Unlock()
			return nil, nil, errFormCloseReplayConflict
		}
		if entry.done {
			result := cloneFormCloseReplayResult(entry.result)
			l.mu.Unlock()
			return nil, &result, nil
		}
		ready := entry.ready
		l.mu.Unlock()
		select {
		case <-ready:
			l.mu.Lock()
			result := cloneFormCloseReplayResult(entry.result)
			l.mu.Unlock()
			return nil, &result, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if !l.makeRoomLocked(identity) {
		l.mu.Unlock()
		return nil, nil, errFormCloseReplayFull
	}
	entry := &formCloseReplayEntry{identity: identity, hash: hash, created: now, ready: make(chan struct{})}
	l.entries[key] = entry
	l.mu.Unlock()
	return &formCloseReservation{ledger: l, key: key, entry: entry}, nil, nil
}

func (l *formCloseReplayLedger) pruneLocked(now time.Time) {
	for key, entry := range l.entries {
		if entry.done && now.Sub(entry.created) > formCloseReplayTTL {
			l.removeLocked(key)
		}
	}
}

func (l *formCloseReplayLedger) makeRoomLocked(identity string) bool {
	count := 0
	for _, entry := range l.entries {
		if entry.identity == identity {
			count++
		}
	}
	for len(l.entries) >= formCloseReplayGlobal || count >= formCloseReplayPerUser {
		keys := make([]string, 0, len(l.entries))
		for key, entry := range l.entries {
			if entry.done && (len(l.entries) >= formCloseReplayGlobal || entry.identity == identity) {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			return false
		}
		sort.Slice(keys, func(i, j int) bool { return l.entries[keys[i]].created.Before(l.entries[keys[j]].created) })
		removed := l.entries[keys[0]]
		l.removeLocked(keys[0])
		if removed.identity == identity {
			count--
		}
	}
	return true
}

func (r *formCloseReservation) complete(result formCloseReplayResult) {
	if r == nil || r.ledger == nil || r.entry == nil {
		return
	}
	r.ledger.mu.Lock()
	defer r.ledger.mu.Unlock()
	entry := r.ledger.entries[r.key]
	if entry != r.entry || entry.done {
		return
	}
	result = r.ledger.boundResult(result)
	r.ledger.makeResultRoomLocked(int64(len(result.body)), entry)
	entry.result = cloneFormCloseReplayResult(result)
	entry.done = true
	r.ledger.resultBytes += int64(len(entry.result.body))
	close(entry.ready)
}

func (l *formCloseReplayLedger) boundResult(result formCloseReplayResult) formCloseReplayResult {
	if int64(len(result.body)) <= l.maxEntryBytes {
		return result
	}
	return formCloseReplayResult{
		status: http.StatusInternalServerError,
		header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		body:   []byte("{\"ok\":false,\"error\":\"ответ проверки закрытия формы превысил допустимый размер\"}\n"),
	}
}

func (l *formCloseReplayLedger) makeResultRoomLocked(needed int64, keep *formCloseReplayEntry) {
	for l.resultBytes+needed > l.maxResultBytes {
		var oldestKey string
		var oldest *formCloseReplayEntry
		for key, entry := range l.entries {
			if entry == keep || !entry.done {
				continue
			}
			if oldest == nil || entry.created.Before(oldest.created) {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			return
		}
		l.removeLocked(oldestKey)
	}
}

func (l *formCloseReplayLedger) removeLocked(key string) {
	entry := l.entries[key]
	if entry == nil {
		return
	}
	if entry.done {
		l.resultBytes -= int64(len(entry.result.body))
		if l.resultBytes < 0 {
			l.resultBytes = 0
		}
	}
	delete(l.entries, key)
}

func cloneFormCloseReplayResult(in formCloseReplayResult) formCloseReplayResult {
	return formCloseReplayResult{status: in.status, header: in.header.Clone(), body: append([]byte(nil), in.body...)}
}

type capturedCloseResponse struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
	maxBody     int64
	tooLarge    bool
}

func newCapturedCloseResponse(maxBody int64) *capturedCloseResponse {
	return &capturedCloseResponse{header: make(http.Header), status: http.StatusOK, maxBody: maxBody}
}

func (w *capturedCloseResponse) Header() http.Header { return w.header }
func (w *capturedCloseResponse) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
}
func (w *capturedCloseResponse) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.tooLarge {
		return len(p), nil
	}
	remaining := w.maxBody - int64(w.body.Len())
	if remaining <= 0 {
		w.tooLarge = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = w.body.Write(p[:int(remaining)])
		w.tooLarge = true
		return len(p), nil
	}
	_, _ = w.body.Write(p)
	return len(p), nil
}

func (s *Server) handleManagedFormCloseIntent(w http.ResponseWriter, r *http.Request) {
	inv := &formCloseInvocation{}
	s.captureFormCloseResponse(w, r, inv, func(cw http.ResponseWriter) {
		s.handleManagedFormEventMode(cw, r, inv)
	})
}

func (s *Server) handleProcessorFormCloseIntent(w http.ResponseWriter, r *http.Request) {
	inv := &formCloseInvocation{processor: true}
	s.captureFormCloseResponse(w, r, inv, func(cw http.ResponseWriter) {
		s.handleProcessorFormEventMode(cw, r, inv)
	})
}

func (s *Server) captureFormCloseResponse(w http.ResponseWriter, r *http.Request, inv *formCloseInvocation, run func(http.ResponseWriter)) {
	ledger := s.formCloseLedger()
	captured := newCapturedCloseResponse(ledger.maxEntryBytes)
	// Never leave an exact retry waiting forever if an unexpected panic escapes
	// the normal DSL/HTTP error conversion. Preserve the panic for the server's
	// recovery middleware, but publish a fail-closed terminal replay first.
	defer func() {
		if recovered := recover(); recovered != nil {
			if inv.reservation != nil {
				body := []byte(`{"ok":false,"error":"внутренняя ошибка проверки закрытия формы","close":{"intentId":"` + inv.intentID + `","allowed":false,"saved":false}}` + "\n")
				inv.reservation.complete(formCloseReplayResult{
					status: http.StatusInternalServerError,
					header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
					body:   body,
				})
			}
			panic(recovered)
		}
	}()
	run(captured)
	if inv.replay != nil {
		writeFormCloseReplay(w, *inv.replay)
		return
	}

	status := captured.status
	var response formEventResponse
	if captured.tooLarge {
		status = http.StatusInternalServerError
		response = formEventResponse{Error: "ответ проверки закрытия формы превысил допустимый размер"}
	} else if err := json.Unmarshal(captured.body.Bytes(), &response); err != nil {
		status = http.StatusInternalServerError
		response = formEventResponse{Error: "внутренняя ошибка ответа закрытия формы"}
	}
	response.Close = &formCloseDecision{
		IntentID: inv.intentID,
		Allowed:  response.OK && !inv.cancelled,
		Saved:    false,
	}
	body, err := json.Marshal(response)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"ok":false,"error":"внутренняя ошибка ответа закрытия формы"}`)
	}
	body = append(body, '\n')
	if int64(len(body)) > ledger.maxEntryBytes {
		status = http.StatusInternalServerError
		response = formEventResponse{
			Error: "ответ проверки закрытия формы превысил допустимый размер",
			Close: &formCloseDecision{IntentID: inv.intentID, Allowed: false, Saved: false},
		}
		body, _ = json.Marshal(response)
		body = append(body, '\n')
	}
	header := captured.header.Clone()
	header.Set("Content-Type", "application/json; charset=utf-8")
	result := formCloseReplayResult{status: status, header: header, body: body}
	if inv.reservation != nil {
		inv.reservation.complete(result)
	}
	writeFormCloseReplay(w, result)
}

func writeFormCloseReplay(w http.ResponseWriter, result formCloseReplayResult) {
	for key, values := range result.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := result.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(result.body)
}

func formCloseClientTimeoutMS(timeout time.Duration) int64 {
	if timeout <= 0 {
		return formCloseDefaultClientMS
	}
	return timeout.Milliseconds() + 1500
}
