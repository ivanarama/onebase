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
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	"github.com/shopspring/decimal"
)

const (
	formCloseReplayTTL        = 10 * time.Minute
	formCloseReplayPerUser    = 128
	formCloseReplayGlobal     = 2048
	formCloseReplayMaxEntry   = int64(4 << 20)
	formCloseReplayByteBudget = int64(64 << 20)
	formCloseDefaultClientMS  = 30_000
	formCloseReplayMaxWaiters = 32
	formCloseReplayWaitMax    = 30 * time.Second
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

// A close intent is valid only for the process generation that rendered the
// form. After a restart the in-memory idempotency ledger is gone, so accepting
// an old unknown-outcome retry could repeat a committed insert. The epoch makes
// that state loss explicit and turns the retry into a correlated manual-
// reconciliation response instead of executing it again.
var formCloseProcessEpoch = uuid.NewString()

func entityFormCloseSchema(entity *metadata.Entity, form *metadata.FormModule) string {
	if entity == nil || form == nil {
		return ""
	}
	payload := struct {
		Name         string
		Kind         metadata.Kind
		Fields       []metadata.Field
		TableParts   []metadata.TablePart
		Posting      bool
		Hierarchical bool
		Form         *metadata.FormModule
	}{
		Name: entity.Name, Kind: entity.Kind, Fields: entity.Fields,
		TableParts: entity.TableParts, Posting: entity.Posting,
		Hierarchical: entity.Hierarchical, Form: form,
	}
	return formCloseSchemaHash(payload)
}

func processorFormCloseSchema(proc *processorpkg.Processor, form *metadata.FormModule) string {
	if proc == nil || form == nil {
		return ""
	}
	payload := struct {
		Name       string
		Params     []processorpkg.Param
		TableParts []metadata.TablePart
		External   bool
		Trusted    bool
		Form       *metadata.FormModule
	}{
		Name: proc.Name, Params: proc.Params, TableParts: proc.TableParts,
		External: proc.External, Trusted: proc.Trusted, Form: form,
	}
	return formCloseSchemaHash(payload)
}

func formCloseSchemaHash(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// formCloseDecision is appended to the ordinary managed-form event response.
// The client applies values/messages first and destroys UI only when this
// decision carries its exact intent id and allowed=true.
type formCloseDecision struct {
	IntentID  string `json:"intentId"`
	Allowed   bool   `json:"allowed"`
	Saved     bool   `json:"saved"`
	FormURL   string `json:"formUrl,omitempty"`
	Reload    bool   `json:"reload,omitempty"`
	Terminal  bool   `json:"terminal,omitempty"`
	Reconcile bool   `json:"reconcile,omitempty"`
}

type formCloseInvocation struct {
	processor  bool
	intentID   string
	reason     string
	mode       string
	cancelled  bool
	saved      bool
	savedID    string
	savedLabel string
	version    int64
	formURL    string
	// suppressState is set after a durable write when the caller cannot read
	// the resulting row. The lifecycle may still finish server-side, but no
	// canonical fields, table parts, labels or handler-derived messages may be
	// reflected back through the close response.
	suppressState        bool
	forceReload          bool
	terminal             bool
	reconcile            bool
	accessCheckFailed    bool
	recheckAccess        func()
	authorization        [sha256.Size]byte
	authorizationOK      bool
	authorizationChanged bool

	// Baseline is the browser-authorized state parsed from this exact request,
	// before save/hooks. Successful close responses retain only server-side
	// deltas from it, avoiding a second multi-megabyte rich-text copy while still
	// allowing the client to merge hook changes with edits made in flight.
	baselineValues     map[string]any
	baselineTableParts map[string][]map[string]any
	baselineFormTables map[string][]map[string]any

	reservation *formCloseReservation
	replay      *formCloseReplayResult
}

type formCloseHTTPError struct {
	status int
	msg    string
	cause  error
}

func (e *formCloseHTTPError) Error() string { return e.msg }
func (e *formCloseHTTPError) Unwrap() error { return e.cause }

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
	return s.prepareFormCloseInvocationWithPayload(r, inv, routeKey, value, true)
}

// prepareMissingFormCloseInvocation is used only after route metadata has
// disappeared. The server deliberately does not read the stale form body: its
// former metadata-derived limit is unknowable. An existing key may therefore
// be joined/replayed by its authenticated principal and fixed envelope without
// comparing the unavailable payload hash. Stored results are sanitized, and a
// new key can only produce the identity-free terminal decision of the missing
// route handler.
func (s *Server) prepareMissingFormCloseInvocation(r *http.Request, inv *formCloseInvocation, routeKey string, value func(string) string) error {
	return s.prepareFormCloseInvocationWithPayload(r, inv, routeKey, value, false)
}

func (s *Server) prepareFormCloseInvocationWithPayload(r *http.Request, inv *formCloseInvocation, routeKey string, value func(string) string, verifyPayload bool) error {
	if err := validateFormCloseEnvelope(inv, value); err != nil {
		return err
	}

	payloadHash, err := formCloseRequestFingerprint(r)
	if err != nil {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "не удалось прочитать состояние формы для проверки закрытия"}
	}
	identity := formCloseIdentity(r)
	admissionIdentity := formCloseAdmissionIdentity(r, identity)
	// Identity is part of the replay key: two users may independently generate
	// the same UUID without observing or blocking one another. The identity is
	// also retained on the entry for the per-user capacity bound. Anonymous
	// admission is bounded separately by a server-observed actor identity: the
	// browser-provided close client UUID must remain stable for exact replay, but
	// must not mint a fresh capacity bucket when it changes.
	replayKey := identity + "\x00" + routeKey + "|" + inv.intentID
	inv.authorization, inv.authorizationOK = formCloseAuthorizationFingerprint(r.Context())
	issuedAtMS, issuedErr := strconv.ParseInt(strings.TrimSpace(value("_close_issued_at")), 10, 64)
	issuedAt := time.UnixMilli(issuedAtMS)
	now := time.Now()
	allowNew := strings.TrimSpace(value("_close_epoch")) == formCloseProcessEpoch &&
		issuedErr == nil && issuedAtMS > 0 &&
		!issuedAt.Before(now.Add(-formCloseReplayTTL)) &&
		!issuedAt.After(now.Add(5*time.Minute))
	ledger := s.formCloseLedger()
	var reservation *formCloseReservation
	var replay *formCloseReplayResult
	if verifyPayload {
		reservation, replay, err = ledger.reserveWithActorAdmission(
			r.Context(), identity, admissionIdentity, replayKey, payloadHash, allowNew,
		)
	} else {
		reservation, replay, err = ledger.reserveWithoutPayloadActorAdmission(
			r.Context(), identity, admissionIdentity, replayKey, payloadHash, allowNew,
		)
	}
	if err != nil {
		return formCloseReservationError(inv, err)
	}
	inv.reservation = reservation
	inv.replay = replay
	markFormCloseAuthorizationChange(inv, replay)
	return nil
}

func validateFormCloseEnvelope(inv *formCloseInvocation, value func(string) string) error {
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
	allowedMode := inv.mode == "discard"
	if !inv.processor {
		allowedMode = allowedMode || inv.mode == "save" || inv.mode == "post" || inv.mode == "save_and_select"
	}
	if !allowedMode {
		return &formCloseHTTPError{status: http.StatusBadRequest, msg: "некорректная _close_mode"}
	}
	return nil
}

func formCloseReservationError(inv *formCloseInvocation, err error) error {
	if err != nil {
		if errors.Is(err, errFormCloseReplayConflict) {
			return &formCloseHTTPError{status: http.StatusConflict, msg: "_close_intent_id уже использован с другим состоянием формы", cause: err}
		}
		if errors.Is(err, errFormCloseReplayWaitersFull) {
			// This exact intent is still running. Unlike admission pressure for a
			// new key, the client must retain this UUID/body and retry its replay;
			// changing UUID could repeat a write whose first response was lost.
			return &formCloseHTTPError{status: http.StatusRequestTimeout, msg: "исходный запрос закрытия ещё выполняется; повторите восстановление позже", cause: err}
		}
		if errors.Is(err, errFormCloseReplayFull) {
			return &formCloseHTTPError{status: http.StatusTooManyRequests, msg: "слишком много незавершённых запросов закрытия", cause: err}
		}
		if errors.Is(err, errFormCloseReplayExpired) {
			inv.reconcile = true
			return &formCloseHTTPError{status: http.StatusConflict, msg: "безопасный срок повтора истёк или сервер был перезапущен; проверьте, сохранились ли данные, прежде чем повторять действие", cause: err}
		}
		return &formCloseHTTPError{status: http.StatusRequestTimeout, msg: "ожидание повторного запроса закрытия отменено"}
	}
	return nil
}

func markFormCloseAuthorizationChange(inv *formCloseInvocation, replay *formCloseReplayResult) {
	if replay != nil && (!inv.authorizationOK || !replay.authorizationOK || replay.authorization != inv.authorization) {
		// A replay body was rendered under a different authorization snapshot.
		// It may contain handler-derived messages, form-only values or reference
		// labels that cannot be safely re-masked under the caller's current roles.
		inv.authorizationChanged = true
	}
}

// recoverExistingFormCloseInvocation performs an envelope-only lookup before
// applying limits derived from current metadata. It never creates a key. This
// lets an exact retained request join/replay an older valid large form after a
// hot reload removed fields or file controls, without making new requests
// bypass the current schema's normal body limits.
func (s *Server) recoverExistingFormCloseInvocation(r *http.Request, inv *formCloseInvocation, routeKey string, value func(string) string) (bool, error) {
	if err := validateFormCloseEnvelope(inv, value); err != nil {
		return false, err
	}
	identity := formCloseIdentity(r)
	admissionIdentity := formCloseAdmissionIdentity(r, identity)
	replayKey := identity + "\x00" + routeKey + "|" + inv.intentID
	inv.authorization, inv.authorizationOK = formCloseAuthorizationFingerprint(r.Context())
	_, replay, err := s.formCloseLedger().reserveWithoutPayloadActorAdmission(
		r.Context(), identity, admissionIdentity, replayKey, [sha256.Size]byte{}, false,
	)
	if errors.Is(err, errFormCloseReplayExpired) {
		return false, nil
	}
	if err != nil {
		return true, formCloseReservationError(inv, err)
	}
	inv.replay = replay
	markFormCloseAuthorizationChange(inv, replay)
	return true, nil
}

func formCloseAuthorizationFingerprint(ctx context.Context) ([sha256.Size]byte, bool) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return sha256.Sum256([]byte("anonymous")), true
	}
	payload := struct {
		IsAdmin      bool           `json:"isAdmin"`
		AIDataAccess bool           `json:"aiDataAccess"`
		Attrs        map[string]any `json:"attrs,omitempty"`
		Roles        []*auth.Role   `json:"roles,omitempty"`
	}{
		IsAdmin: user.IsAdmin, AIDataAccess: user.AIDataAccess,
		Attrs: user.Attrs, Roles: user.Roles,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// An unhashable host-provided attribute must fail closed. Both the first
		// response and every replay are marked non-comparable, so cached dynamic
		// payload is never disclosed across that boundary.
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(encoded), true
}

// formCloseRequestFingerprint hashes the logical form payload rather than the
// raw HTTP body. Multipart boundaries may legitimately change on a retry, but
// every value, file byte and file-part header must remain identical for an
// intent id to be replayed.
func formCloseRequestFingerprint(r *http.Request) ([sha256.Size]byte, error) {
	h := sha256.New()
	writeFormCloseFingerprintString(h, "onebase-form-close-v3")
	// Lifecycle metadata lives in fixed headers, not in form fields: entity
	// fields and form attributes are allowed to use `_close_*` names. Bind the
	// envelope to the payload hash so the same UUID cannot be replayed with a
	// different mode, process proof, timestamp or route identity.
	for _, name := range []string{
		"_close_intent_id", "_close_epoch", "_close_issued_at",
		"_close_reason", "_close_mode", "_close_client", "_close_schema", "_kind", "_id",
	} {
		writeFormCloseFingerprintString(h, "envelope")
		writeFormCloseFingerprintString(h, name)
		writeFormCloseFingerprintString(h, missingFormCloseValue(r, name))
	}

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
		// Session cookies rotate independently in different tabs and after auth
		// refresh. An authenticated close replay belongs to the stable principal,
		// otherwise a lost successful response could execute again after rotation.
		principal := strings.TrimSpace(user.ID)
		if principal == "" {
			principal = strings.TrimSpace(user.Login)
		}
		if principal != "" {
			parts = append(parts, "authenticated", principal)
		}
	}
	if len(parts) == 0 {
		if cookie, err := r.Cookie("onebase_session"); err == nil && cookie.Value != "" {
			sum := sha256.Sum256([]byte(cookie.Value))
			parts = append(parts, "session", hex.EncodeToString(sum[:]))
		}
	}
	if len(parts) == 0 {
		// Open-access installations intentionally have neither auth.User nor a
		// session cookie. The server renders a per-form UUID into the lifecycle
		// envelope so an exact retry remains stable across proxy/IP changes while
		// unrelated anonymous forms keep separate per-identity quotas.
		if clientID, err := uuid.Parse(missingFormCloseValue(r, "_close_client")); err == nil {
			parts = append(parts, "open-form", clientID.String())
		}
	}
	if len(parts) == 0 {
		parts = append(parts, clientIP(r))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// formCloseAdmissionIdentity is deliberately separate from formCloseIdentity.
// The latter includes the browser-rendered close client UUID so an exact retry
// can move between networks without executing a durable write twice. That UUID
// is untrusted, however, and therefore cannot define the capacity bucket for a
// new anonymous intent: rotating it would bypass the per-user bound and could
// fill the global ledger. For authenticated requests the stable principal is
// already suitable; for open access use the TCP peer observed by the server and
// never a spoofable forwarding header or caller-provided cookie/header.
func formCloseAdmissionIdentity(r *http.Request, replayIdentity string) string {
	if user := auth.UserFromContext(r.Context()); user != nil {
		if strings.TrimSpace(user.ID) != "" || strings.TrimSpace(user.Login) != "" {
			return replayIdentity
		}
	}
	sum := sha256.Sum256([]byte("anonymous-form-close\x00" + clientIP(r)))
	return hex.EncodeToString(sum[:])
}

type formCloseReplayResult struct {
	status          int
	header          http.Header
	body            []byte
	authorization   [sha256.Size]byte
	authorizationOK bool
}

var (
	errFormCloseReplayConflict    = errors.New("close-intent replay payload conflict")
	errFormCloseReplayFull        = errors.New("close-intent replay ledger full")
	errFormCloseReplayWaitersFull = errors.New("close-intent replay waiter capacity reached")
	errFormCloseReplayExpired     = errors.New("close-intent replay proof expired")
)

type formCloseReplayEntry struct {
	identity          string
	admissionIdentity string
	hash              [sha256.Size]byte
	created           time.Time
	completedAt       time.Time
	ready             chan struct{}
	done              bool
	waiters           int
	result            formCloseReplayResult
}

type formCloseReplayLedger struct {
	mu              sync.Mutex
	entries         map[string]*formCloseReplayEntry
	resultBytes     int64
	maxEntryBytes   int64
	maxResultBytes  int64
	maxActorEntries int
	maxWaiters      int
	waitTimeout     time.Duration
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
		entries:         make(map[string]*formCloseReplayEntry),
		maxEntryBytes:   maxEntryBytes,
		maxResultBytes:  maxResultBytes,
		maxActorEntries: formCloseReplayPerUser,
		maxWaiters:      formCloseReplayMaxWaiters,
		waitTimeout:     formCloseReplayWaitMax,
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
	return l.reserveWithAdmission(ctx, identity, key, hash, true)
}

func (l *formCloseReplayLedger) reserveWithAdmission(ctx context.Context, identity, key string, hash [sha256.Size]byte, allowNew bool) (*formCloseReservation, *formCloseReplayResult, error) {
	return l.reserveWithActorAdmission(ctx, identity, identity, key, hash, allowNew)
}

func (l *formCloseReplayLedger) reserveWithActorAdmission(ctx context.Context, identity, admissionIdentity, key string, hash [sha256.Size]byte, allowNew bool) (*formCloseReservation, *formCloseReplayResult, error) {
	return l.reserveWithAdmissionPolicy(ctx, identity, admissionIdentity, key, hash, allowNew, true)
}

func (l *formCloseReplayLedger) reserveWithoutPayloadActorAdmission(ctx context.Context, identity, admissionIdentity, key string, hash [sha256.Size]byte, allowNew bool) (*formCloseReservation, *formCloseReplayResult, error) {
	return l.reserveWithAdmissionPolicy(ctx, identity, admissionIdentity, key, hash, allowNew, false)
}

func (l *formCloseReplayLedger) reserveWithAdmissionPolicy(ctx context.Context, identity, admissionIdentity, key string, hash [sha256.Size]byte, allowNew, verifyPayload bool) (*formCloseReservation, *formCloseReplayResult, error) {
	now := time.Now()
	l.mu.Lock()
	l.pruneLocked(now)
	if entry := l.entries[key]; entry != nil {
		if entry.identity != identity || (verifyPayload && entry.hash != hash) {
			l.mu.Unlock()
			return nil, nil, errFormCloseReplayConflict
		}
		if entry.done {
			result := cloneFormCloseReplayResult(entry.result)
			l.mu.Unlock()
			return nil, &result, nil
		}
		if entry.waiters >= l.maxWaiters {
			l.mu.Unlock()
			return nil, nil, errFormCloseReplayWaitersFull
		}
		entry.waiters++
		ready := entry.ready
		l.mu.Unlock()
		defer func() {
			l.mu.Lock()
			if current := l.entries[key]; current == entry && entry.waiters > 0 {
				entry.waiters--
			}
			l.mu.Unlock()
		}()
		waitTimeout := l.waitTimeout
		if waitTimeout <= 0 {
			waitTimeout = formCloseReplayWaitMax
		}
		timer := time.NewTimer(waitTimeout)
		defer timer.Stop()
		select {
		case <-ready:
			l.mu.Lock()
			result := cloneFormCloseReplayResult(entry.result)
			l.mu.Unlock()
			return nil, &result, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
			return nil, nil, context.DeadlineExceeded
		}
	}
	if !allowNew {
		l.mu.Unlock()
		return nil, nil, errFormCloseReplayExpired
	}
	if !l.makeRoomLocked(identity, admissionIdentity) {
		l.mu.Unlock()
		return nil, nil, errFormCloseReplayFull
	}
	entry := &formCloseReplayEntry{
		identity: identity, admissionIdentity: admissionIdentity,
		hash: hash, created: now, ready: make(chan struct{}),
	}
	l.entries[key] = entry
	l.mu.Unlock()
	return &formCloseReservation{ledger: l, key: key, entry: entry}, nil, nil
}

func (l *formCloseReplayLedger) pruneLocked(now time.Time) {
	for key, entry := range l.entries {
		if entry.done && !entry.completedAt.IsZero() && now.Sub(entry.completedAt) > formCloseReplayTTL {
			l.removeLocked(key)
		}
	}
}

func (l *formCloseReplayLedger) makeRoomLocked(identity, admissionIdentity string) bool {
	identityCount := 0
	actorCount := 0
	for _, entry := range l.entries {
		if entry.identity == identity {
			identityCount++
		}
		if entry.admissionIdentity == admissionIdentity {
			actorCount++
		}
	}
	// An unexpired idempotency key must never be forgotten: doing so turns a
	// lost-response retry into a second durable write. Body pressure is handled
	// by compact recovery tombstones; count pressure rejects new intents until
	// an entry expires instead of evicting an exactly-once key.
	actorLimit := l.maxActorEntries
	if actorLimit <= 0 {
		actorLimit = formCloseReplayPerUser
	}
	return len(l.entries) < formCloseReplayGlobal &&
		identityCount < formCloseReplayPerUser && actorCount < actorLimit
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
	result = r.ledger.makeResultRoomLocked(result, entry)
	entry.result = cloneFormCloseReplayResult(result)
	entry.done = true
	entry.completedAt = time.Now()
	r.ledger.resultBytes += int64(len(entry.result.body))
	close(entry.ready)
}

func (l *formCloseReplayLedger) boundResult(result formCloseReplayResult) formCloseReplayResult {
	if int64(len(result.body)) <= l.maxEntryBytes {
		return result
	}
	result.status = http.StatusInternalServerError
	result.header = http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}
	result.body = []byte("{\"ok\":false,\"error\":\"ответ проверки закрытия формы превысил допустимый размер\"}\n")
	return result
}

func (l *formCloseReplayLedger) makeResultRoomLocked(result formCloseReplayResult, keep *formCloseReplayEntry) formCloseReplayResult {
	needed := int64(len(result.body))
	if l.resultBytes+needed <= l.maxResultBytes {
		return result
	}
	candidates := make([]*formCloseReplayEntry, 0, len(l.entries))
	for _, entry := range l.entries {
		if entry != keep && entry.done {
			candidates = append(candidates, entry)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].completedAt.Before(candidates[j].completedAt)
	})
	for _, entry := range candidates {
		compact := compactStoredFormCloseReplay(entry.result)
		if len(compact.body) >= len(entry.result.body) {
			continue
		}
		l.resultBytes -= int64(len(entry.result.body))
		entry.result = compact
		l.resultBytes += int64(len(entry.result.body))
		if l.resultBytes+needed <= l.maxResultBytes {
			return result
		}
	}
	compact := compactStoredFormCloseReplay(result)
	if len(compact.body) < len(result.body) {
		result = compact
	}
	return result
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
	return formCloseReplayResult{
		status: in.status, header: in.header.Clone(), body: append([]byte(nil), in.body...),
		authorization: in.authorization, authorizationOK: in.authorizationOK,
	}
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
	// the normal DSL/HTTP error conversion. Convert it into the same fail-closed
	// correlated response that is stored for an exact replay.
	defer func() {
		if recovered := recover(); recovered != nil {
			recheckFormCloseAccess(inv)
			body := marshalFormCloseFailure(inv, inv.version, "внутренняя ошибка проверки закрытия формы")
			status := http.StatusInternalServerError
			if inv.terminal {
				status = http.StatusOK
			}
			result := formCloseReplayResult{
				status: status,
				header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
				body:   body,
			}
			stampFormCloseReplayAuthorization(&result, inv)
			if inv.reservation != nil {
				inv.reservation.complete(compactStoredFormCloseReplay(result))
			}
			// The captured writer has not reached the real client yet. Return the
			// same correlated failure that was stored for an exact retry instead
			// of re-panicking into an outer plain-text recovery response.
			writeFormCloseReplay(w, result)
		}
	}()
	run(captured)
	recheckFormCloseAccess(inv)
	if inv.replay != nil {
		writeFormCloseReplay(w, sanitizeFormCloseReplay(inv, *inv.replay))
		return
	}

	status := captured.status
	var response formEventResponse
	if captured.tooLarge {
		if inv.saved {
			inv.forceReload = true
			status = http.StatusOK
			response = formEventResponse{Error: "результат сохранён; форма будет перезагружена"}
		} else {
			status = http.StatusInternalServerError
			response = formEventResponse{Error: "ответ проверки закрытия формы превысил допустимый размер", Dirty: boolPtr(true)}
		}
	} else if err := json.Unmarshal(captured.body.Bytes(), &response); err != nil {
		status = http.StatusInternalServerError
		response = formEventResponse{Error: "внутренняя ошибка ответа закрытия формы"}
	}
	// BeforeClose may persist a previously-new object even in discard mode.
	// Treat the handler's canonical identity as a saved result so a concurrent
	// client edit adopts it and a retry updates that row rather than inserting
	// a duplicate.
	if !inv.saved && response.SavedID != "" {
		inv.saved = true
		inv.savedID = response.SavedID
		inv.version = response.Version
	}
	response.Close = &formCloseDecision{
		IntentID:  inv.intentID,
		Allowed:   inv.terminal || (response.OK && !inv.cancelled && !inv.accessCheckFailed),
		Saved:     inv.saved,
		FormURL:   inv.formURL,
		Reload:    inv.forceReload,
		Terminal:  inv.terminal,
		Reconcile: inv.reconcile,
	}
	if inv.saved {
		response.SavedID = inv.savedID
		if len(inv.savedLabel) <= 1024 {
			response.SavedLabel = inv.savedLabel
		} else {
			response.SavedLabel = ""
		}
		// BeforeClose runs after the canonical save and may itself call
		// Объект.Записать(), advancing the optimistic version again. Preserve the
		// handler's final version; inv.version is only the lower-bound captured
		// immediately after the first save.
		if response.Version < inv.version {
			response.Version = inv.version
		}
	}
	if inv.suppressState {
		redactUnreadableSavedFormResponse(&response)
	}
	if inv.accessCheckFailed && !inv.terminal {
		status = http.StatusInternalServerError
		inv.reconcile = true
		response.OK = false
		response.Error = "не удалось проверить итоговый доступ к форме"
		response.Dirty = boolPtr(true)
		response.SavedID = ""
		response.SavedLabel = ""
		response.Version = 0
		response.Close.Saved = false
		response.Close.FormURL = ""
		response.Close.Reload = false
		response.Close.Reconcile = true
	}
	if inv.terminal {
		// A caller who no longer has read access has no safe follow-up request.
		// Return a deliberately identity-free terminal decision even when a later
		// hook attempted to cancel or failed after the durable write.
		status = http.StatusOK
		response.OK = true
		response.Error = ""
		response.SavedID = ""
		response.SavedLabel = ""
		response.Version = 0
		response.Dirty = boolPtr(false)
		response.Close.FormURL = ""
		response.Close.Saved = false
		response.Close.Reload = false
		response.Close.Reconcile = false
	}
	body, err := json.Marshal(response)
	if err != nil {
		status = http.StatusInternalServerError
		body = marshalFormCloseFailure(inv, response.Version, "внутренняя ошибка ответа закрытия формы")
	} else {
		body = append(body, '\n')
	}
	header := captured.header.Clone()
	header.Set("Content-Type", "application/json; charset=utf-8")
	result := formCloseReplayResult{status: status, header: header, body: body}
	stampFormCloseReplayAuthorization(&result, inv)
	replayResult := result
	if int64(len(body)) > ledger.maxEntryBytes {
		// The first caller receives the complete mergeable delta. Keep the ledger
		// bounded with a correlated recovery envelope: an exact transport retry
		// must reload the durable row before another write rather than replay an
		// incomplete state or perform the save twice.
		replayStatus := http.StatusInternalServerError
		message := "ответ проверки закрытия формы превысил допустимый размер"
		if inv.saved {
			inv.forceReload = true
			replayStatus = http.StatusOK
			message = "результат сохранён; форма будет перезагружена"
		}
		replayResult = formCloseReplayResult{
			status: replayStatus,
			header: header.Clone(),
			body:   marshalFormCloseFailure(inv, response.Version, message),
		}
		stampFormCloseReplayAuthorization(&replayResult, inv)
	}
	if inv.reservation != nil {
		// A replay happens after the original response may have been lost and
		// after arbitrary related-row/RLS data may have changed. Store only a
		// provenance-free recovery tombstone, never cached handler-derived state.
		inv.reservation.complete(compactStoredFormCloseReplay(replayResult))
	}
	writeFormCloseReplay(w, result)
}

func stampFormCloseReplayAuthorization(result *formCloseReplayResult, inv *formCloseInvocation) {
	if result == nil || inv == nil {
		return
	}
	result.authorization = inv.authorization
	result.authorizationOK = inv.authorizationOK
}

func recheckFormCloseAccess(inv *formCloseInvocation) {
	if inv == nil || inv.recheckAccess == nil {
		return
	}
	defer func() {
		if recover() != nil {
			inv.suppressState = true
			inv.accessCheckFailed = true
		}
	}()
	inv.recheckAccess()
}

func sanitizeFormCloseReplay(inv *formCloseInvocation, replay formCloseReplayResult) formCloseReplayResult {
	if inv == nil || (!inv.terminal && !inv.reconcile && !inv.accessCheckFailed && !inv.authorizationChanged) {
		return replay
	}
	var response formEventResponse
	if err := json.Unmarshal(replay.body, &response); err != nil {
		replay.status = http.StatusInternalServerError
		replay.body = marshalFormCloseFailure(inv, inv.version, "внутренняя ошибка ответа закрытия формы")
		return replay
	}
	if response.Close == nil {
		response.Close = &formCloseDecision{IntentID: inv.intentID}
	}
	response.Close.Allowed = false
	if inv.authorizationChanged {
		// Cached event payload can contain arbitrary handler-derived data. Field
		// masks cannot safely be re-applied to messages, picker data, form-only
		// attributes or derived element state, so discard all of it. A durable
		// write is recovered through a reload under current policy; an unsaved
		// result stays open and dirty for explicit reconciliation.
		redactUnreadableSavedFormResponse(&response)
		response.Close.Allowed = false
		response.Close.Terminal = false
		response.SavedLabel = ""
		response.Dirty = boolPtr(true)
		if response.Close.Saved {
			replay.status = http.StatusOK
			response.OK = true
			response.Error = "права доступа изменились; сохранённый результат нужно перечитать"
			response.Close.Reload = true
			response.Close.Reconcile = false
		} else {
			replay.status = http.StatusConflict
			response.OK = false
			response.Error = "права доступа изменились; обновите форму перед повтором"
			response.SavedID = ""
			response.Version = 0
			response.Close.Saved = false
			response.Close.FormURL = ""
			response.Close.Reload = false
		}
	}
	if inv.suppressState {
		redactUnreadableSavedFormResponse(&response)
	}
	if inv.accessCheckFailed && !inv.terminal {
		replay.status = http.StatusInternalServerError
		inv.reconcile = true
		response.OK = false
		response.Error = "не удалось проверить итоговый доступ к форме"
		response.Dirty = boolPtr(true)
		response.SavedID = ""
		response.SavedLabel = ""
		response.Version = 0
		response.Close.Saved = false
		response.Close.FormURL = ""
		response.Close.Reload = false
		response.Close.Terminal = false
		response.Close.Reconcile = true
	}
	if inv.reconcile && !inv.terminal && !inv.accessCheckFailed {
		// Missing metadata cannot safely re-authorize a cached durable identity.
		// Preserve the idempotency key, but return no saved id/version or dynamic
		// handler state and require the user to reconcile from a newly loaded form.
		replay.status = http.StatusConflict
		redactUnreadableSavedFormResponse(&response)
		response.OK = false
		response.Error = "описание объекта удалено с сервера; проверьте результат и обновите форму"
		response.Dirty = boolPtr(true)
		response.SavedID = ""
		response.SavedLabel = ""
		response.Version = 0
		response.Close.Allowed = false
		response.Close.Saved = false
		response.Close.FormURL = ""
		response.Close.Reload = false
		response.Close.Terminal = false
		response.Close.Reconcile = true
	}
	if inv.terminal {
		replay.status = http.StatusOK
		response.OK = true
		response.Error = ""
		response.SavedID = ""
		response.SavedLabel = ""
		response.Version = 0
		response.Dirty = boolPtr(false)
		response.Close.Allowed = true
		response.Close.Saved = false
		response.Close.FormURL = ""
		response.Close.Terminal = true
		response.Close.Reload = false
		response.Close.Reconcile = false
	}
	body, err := json.Marshal(response)
	if err != nil {
		replay.status = http.StatusInternalServerError
		replay.body = marshalFormCloseFailure(inv, inv.version, "внутренняя ошибка ответа закрытия формы")
		return replay
	}
	replay.body = append(body, '\n')
	replay.header = replay.header.Clone()
	if replay.header == nil {
		replay.header = make(http.Header)
	}
	replay.header.Set("Content-Type", "application/json; charset=utf-8")
	return replay
}

func redactUnreadableSavedFormResponse(response *formEventResponse) {
	if response == nil {
		return
	}
	response.Values = nil
	response.TableParts = nil
	response.FormTables = nil
	response.RefOptions = nil
	response.TPRefOptions = nil
	response.ConditionalCSS = ""
	response.ElementStates = nil
	response.Messages = nil
	response.PickerData = nil
	response.ChoiceList = nil
	response.SavedLabel = ""
	if response.Error != "" {
		response.Error = "доступ запрещён"
	}
}

// compactStoredFormCloseReplay replaces a rich completed response with a small
// recovery tombstone while retaining the idempotency key. The key must survive
// until TTL expiry: deleting it under memory pressure would let an exact retry
// execute a durable close for the second time.
func compactStoredFormCloseReplay(result formCloseReplayResult) formCloseReplayResult {
	var response formEventResponse
	if err := json.Unmarshal(result.body, &response); err != nil || response.Close == nil {
		return result
	}
	redactUnreadableSavedFormResponse(&response)
	response.SavedLabel = ""
	switch {
	case response.Close.Terminal:
		result.status = http.StatusOK
		response.OK = true
		response.Error = ""
		response.Dirty = boolPtr(false)
		response.SavedID = ""
		response.Version = 0
		response.Close.Allowed = true
		response.Close.Saved = false
		response.Close.FormURL = ""
		response.Close.Reload = false
		response.Close.Reconcile = false
	case response.Close.Saved:
		result.status = http.StatusOK
		response.OK = true
		response.Error = "результат сохранён; форма будет перезагружена"
		response.Dirty = boolPtr(true)
		response.Close.Allowed = false
		response.Close.Reload = true
		response.Close.Terminal = false
		response.Close.Reconcile = false
	case response.Close.Allowed:
		result.status = http.StatusOK
		response.OK = true
		response.Error = ""
		response.Dirty = boolPtr(false)
		response.Close.Reload = false
		response.Close.Terminal = false
		response.Close.Reconcile = false
	default:
		result.status = http.StatusConflict
		response.OK = false
		response.Error = "результат проверки закрытия восстановлен без состояния формы; повторите действие"
		response.Dirty = boolPtr(true)
	}
	body, err := json.Marshal(response)
	if err != nil {
		return result
	}
	body = append(body, '\n')
	result.body = body
	result.header = result.header.Clone()
	if result.header == nil {
		result.header = make(http.Header)
	}
	result.header.Set("Content-Type", "application/json; charset=utf-8")
	return result
}

func compactFormCloseDelta(response *formEventResponse, inv *formCloseInvocation) {
	if response == nil || inv == nil {
		return
	}
	response.Values = changedCloseValues(response.Values, inv.baselineValues)
	response.TableParts = changedCloseTables(response.TableParts, inv.baselineTableParts)
	response.FormTables = changedCloseTables(response.FormTables, inv.baselineFormTables)
	response.RefOptions = filterCloseRefOptions(response.RefOptions, response.Values)
	response.TPRefOptions = filterCloseTPRefOptions(response.TPRefOptions, response.TableParts)
}

func changedCloseValues(after, before map[string]any) map[string]any {
	if len(after) == 0 || before == nil {
		return after
	}
	changed := make(map[string]any)
	for name, value := range after {
		baseline, ok := mapValueCI(before, name)
		if !ok || !reflect.DeepEqual(value, baseline) {
			changed[name] = value
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return changed
}

func changedCloseTables(after, before map[string][]map[string]any) map[string][]map[string]any {
	if len(after) == 0 || before == nil {
		return after
	}
	changed := make(map[string][]map[string]any)
	for name, rows := range after {
		baseline, ok := tableRowsCI(before, name)
		if !ok || !reflect.DeepEqual(rows, baseline) {
			changed[name] = rows
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return changed
}

func mapValueCI(values map[string]any, name string) (any, bool) {
	for key, value := range values {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return nil, false
}

func filterCloseRefOptions(options map[string][]map[string]any, values map[string]any) map[string][]map[string]any {
	if len(options) == 0 || len(values) == 0 {
		return nil
	}
	filtered := make(map[string][]map[string]any)
	for name, rows := range options {
		if _, ok := mapValueCI(values, name); ok {
			filtered[name] = rows
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func filterCloseTPRefOptions(options map[string]map[string][]map[string]any, tables map[string][]map[string]any) map[string]map[string][]map[string]any {
	if len(options) == 0 || len(tables) == 0 {
		return nil
	}
	filtered := make(map[string]map[string][]map[string]any)
	for name, rows := range options {
		if _, ok := tableRowsCI(tables, name); ok {
			filtered[name] = rows
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func marshalFormCloseFailure(inv *formCloseInvocation, version int64, message string) []byte {
	response := formEventResponse{Error: message}
	decision := &formCloseDecision{Allowed: false}
	if inv != nil {
		decision.IntentID = inv.intentID
		decision.Saved = inv.saved
		decision.FormURL = inv.formURL
		decision.Reload = inv.forceReload
		decision.Terminal = inv.terminal
		decision.Reconcile = inv.reconcile
		decision.Allowed = inv.terminal
		if inv.saved {
			response.SavedID = inv.savedID
			// A failure never selects the popup value. Keep the durable identity
			// bounded even when the display field itself is enormous.
			if !inv.suppressState && len(inv.savedLabel) <= 1024 {
				response.SavedLabel = inv.savedLabel
			}
			if version < inv.version {
				version = inv.version
			}
			response.Version = version
			response.Dirty = boolPtr(true)
		}
		if inv.terminal {
			response.OK = true
			response.Error = ""
			response.SavedID = ""
			response.SavedLabel = ""
			response.Version = 0
			response.Dirty = boolPtr(false)
			decision.FormURL = ""
			decision.Saved = false
			decision.Reload = false
			decision.Reconcile = false
		}
		if inv.accessCheckFailed && !inv.terminal {
			response.OK = false
			response.Error = "не удалось проверить итоговый доступ к форме"
			response.SavedID = ""
			response.SavedLabel = ""
			response.Version = 0
			response.Dirty = boolPtr(true)
			decision.Saved = false
			decision.FormURL = ""
			decision.Reload = false
			decision.Reconcile = true
		}
	}
	response.Close = decision
	body, err := json.Marshal(response)
	if err != nil {
		// The response above contains only scalar values and cannot normally
		// fail. Keep a correlated last resort for defensive completeness.
		body = []byte(`{"ok":false,"error":"internal close response error","close":{"intentId":"` + decision.IntentID + `","allowed":false,"saved":false}}`)
	}
	return append(body, '\n')
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
