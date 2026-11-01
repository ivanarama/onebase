package ui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const concealTTL = 10 * time.Minute

// Retain only instance metadata after a grant expires so a successful handler
// can authorize again. This lifetime never extends a disclosure grant.
const concealInstanceTTL = 24 * time.Hour
const concealSessionLimit = 128
const concealServerLimit = 2048
const concealHeader = "X-OneBase-Conceal-Context"
const concealQuery = "_ob_conceal"

type concealInstance struct {
	owner         [32]byte
	authorization [32]byte
	generation    uint64
	store         *storage.DB
	entity        *metadata.Entity
	form          *metadata.FormModule
	schema        string
	object        string // empty means this exact, separately identified unsaved form
	expires       time.Time
	grants        map[string]time.Time // field names only; no secret values or replay bodies
}

type concealRequestKey struct{}
type concealRequest struct {
	server  *Server
	request *http.Request
	entity  *metadata.Entity
	close   *formCloseInvocation
	native  bool
}

// Conceal requests are serialized through validation, hooks, writes and response
// serialization. A revoke and a submit/reveal therefore have a single order.
// The lock is separate from the close replay ledger and never authorizes replay.
func (s *Server) beginConcealRequest(r *http.Request, entity *metadata.Entity, native bool, close *formCloseInvocation) (*http.Request, func()) {
	has := false
	for _, form := range entity.Forms {
		if len(concealedFormFields(form)) > 0 {
			has = true
		}
	}
	if !has {
		return r, func() {}
	}
	s.concealMu.Lock()
	cr := &concealRequest{server: s, entity: entity, native: native, close: close}
	r = r.WithContext(context.WithValue(r.Context(), concealRequestKey{}, cr))
	cr.request = r
	// Permission/configuration changes invalidate old authority even when the
	// normal route subsequently rejects this request before parsing its body.
	owner, ownerOK := concealOwner(r)
	authz, authzOK := formCloseAuthorizationFingerprint(r.Context())
	for token, e := range s.concealInstances {
		if ownerOK && e.owner == owner && (e.authorization != authz || !authzOK || e.generation != s.reg.Generation()) {
			delete(s.concealInstances, token)
		}
	}
	if e := s.concealInstances[cr.token()]; e != nil && ownerOK && e.owner == owner && e.entity == entity && e.object != "" {
		id, err := uuid.Parse(e.object)
		allowed := false
		if err == nil && s.can(r, string(entity.Kind), entity.Name, "read") {
			allowed, err = s.rowAllowsIDResult(r.Context(), entity, "read", id)
		}
		if err != nil || !allowed {
			delete(s.concealInstances, cr.token())
		}
	}
	return r, func() {
		if close != nil && !close.cancelled {
			// Release must not parse a body rejected before its size limit.
			token := cr.token()
			if e := s.concealInstances[token]; e != nil && ownerOK && e.owner == owner {
				delete(s.concealInstances, token)
			}
		}
		s.concealMu.Unlock()
	}
}

func concealOwner(r *http.Request) ([32]byte, bool) {
	cookie, err := r.Cookie("onebase_session")
	principal := "open"
	if u := auth.UserFromContext(r.Context()); u != nil {
		principal = u.ID + "\x00" + u.Login
		if err != nil || cookie.Value == "" {
			return [32]byte{}, false
		}
	}
	if err != nil || cookie.Value == "" {
		cookie, err = r.Cookie("onebase_conceal_session")
	}
	if err != nil || cookie.Value == "" {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(principal + "\x00" + cookie.Value)), true
}

func (s *Server) concealNow() time.Time {
	if s.concealClock != nil {
		return s.concealClock()
	}
	return time.Now()
}

func (cr *concealRequest) token() string {
	if token := cr.request.Header.Get(concealHeader); token != "" {
		return token
	}
	return cr.request.URL.Query().Get(concealQuery)
}

func (cr *concealRequest) instance() (*concealInstance, string) {
	r, s := cr.request, cr.server
	token := cr.token()
	e := s.concealInstances[token]
	owner, ok := concealOwner(r)
	if e == nil || !ok || e.owner != owner {
		return nil, token
	}
	authorization, valid := formCloseAuthorizationFingerprint(r.Context())
	formKind := "object"
	object := chiConcealObject(r, cr.native)
	if !cr.native {
		if kind := r.FormValue("_kind"); kind != "" {
			formKind = kind
		}
	}
	form := pickManagedForm(cr.entity, formKind)
	if !valid || e.authorization != authorization || e.generation != s.reg.Generation() || e.store != s.store || e.entity != cr.entity || e.form != form || e.object != object || e.schema != entityFormCloseSchema(cr.entity, form) || !s.concealNow().Before(e.expires) {
		delete(s.concealInstances, token)
		return nil, token
	}
	return e, token
}

func concealRouteID(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "id")) }

func chiConcealObject(r *http.Request, native bool) string {
	if native {
		return concealRouteID(r)
	}
	return strings.TrimSpace(r.FormValue("_id"))
}

func (s *Server) issueConcealInstance(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, form *metadata.FormModule, data map[string]any) {
	if len(concealedFormFields(form)) == 0 {
		return
	}
	// Error re-render already owns the feature lock; ordinary GET takes it here.
	if cr, _ := r.Context().Value(concealRequestKey{}).(*concealRequest); cr == nil {
		s.concealMu.Lock()
		defer s.concealMu.Unlock()
	}
	blankConcealedValues(form, data["Values"])
	if _, err := metadata.FormConcealRules(entity, form); err != nil {
		return
	}
	owner, ok := concealOwner(r)
	if !ok && auth.UserFromContext(r.Context()) == nil {
		cookie := &http.Cookie{Name: "onebase_conceal_session", Value: uuid.NewString(), Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode}
		http.SetCookie(w, cookie)
		r = r.Clone(r.Context())
		r.AddCookie(cookie)
		owner, ok = concealOwner(r)
	}
	if !ok {
		return
	}
	authorization, valid := formCloseAuthorizationFingerprint(r.Context())
	if !valid {
		return
	}
	if s.concealInstances == nil {
		s.concealInstances = map[string]*concealInstance{}
	}
	// Reload of a rendered instance revokes it, while other tabs retain separate IDs.
	if previous := r.URL.Query().Get(concealQuery); previous != "" {
		if e := s.concealInstances[previous]; e != nil && e.owner == owner {
			delete(s.concealInstances, previous)
		}
	}
	now := s.concealNow()
	count := 0
	for token, e := range s.concealInstances {
		if !now.Before(e.expires) {
			delete(s.concealInstances, token)
		} else if e.owner == owner {
			count++
		}
	}
	// Bounded admission fails closed. No eviction may revive an old instance.
	if count >= concealSessionLimit || len(s.concealInstances) >= concealServerLimit {
		return
	}
	token := uuid.NewString()
	object := ""
	if isNew, _ := data["IsNew"].(bool); !isNew {
		object = fmt.Sprint(data["ID"])
	}
	s.concealInstances[token] = &concealInstance{owner: owner, authorization: authorization, generation: s.reg.Generation(), store: s.store, entity: entity, form: form, schema: entityFormCloseSchema(entity, form), object: object, expires: now.Add(concealInstanceTTL), grants: map[string]time.Time{}}
	data["ConcealContext"] = token
	target := *r.URL
	q := target.Query()
	q.Set(concealQuery, token)
	target.RawQuery = q.Encode()
	data["ConcealAction"] = target.RequestURI()
}

func concealedFormFields(form *metadata.FormModule) map[string]string {
	if form == nil {
		return nil
	}
	out := map[string]string{}
	form.Walk(func(el *metadata.FormElement) bool {
		if el != nil && el.Conceal {
			out[dpFieldName(el.DataPath)] = el.RevealWhen
		}
		return true
	})
	return out
}

func blankConcealedValues(form *metadata.FormModule, values any) {
	for name := range concealedFormFields(form) {
		switch m := values.(type) {
		case map[string]string:
			for k := range m {
				if strings.EqualFold(k, name) {
					m[k] = ""
				}
			}
		case map[string]any:
			for k := range m {
				if strings.EqualFold(k, name) {
					m[k] = ""
				}
			}
		}
	}
}

func (s *Server) concealedGrants(ctx context.Context, form *metadata.FormModule) map[string]bool {
	out := map[string]bool{}
	cr, _ := ctx.Value(concealRequestKey{}).(*concealRequest)
	if cr == nil || cr.server != s {
		return out
	}
	e, _ := cr.instance()
	if e == nil || e.form != form {
		return out
	}
	now := s.concealNow()
	for name, until := range e.grants {
		if now.Before(until) {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

func (s *Server) concealEventValues(ctx context.Context, form *metadata.FormModule, values map[string]any) []string {
	grants := s.concealedGrants(ctx, form)
	// A close replay is never a grant, nor a cache of disclosed values.
	if cr, _ := ctx.Value(concealRequestKey{}).(*concealRequest); cr != nil && cr.close != nil {
		grants = nil
	}
	var revealed []string
	for name := range concealedFormFields(form) {
		if grants[strings.ToLower(name)] {
			revealed = append(revealed, name)
			continue
		}
		for k := range values {
			if strings.EqualFold(k, name) {
				values[k] = ""
			}
		}
	}
	return revealed
}

func (s *Server) unrevealedConcealedFields(r *http.Request, entity *metadata.Entity, form *metadata.FormModule) map[string]bool {
	grants := s.concealedGrants(r.Context(), form)
	out := map[string]bool{}
	for name := range concealedFormFields(form) {
		if !grants[strings.ToLower(name)] {
			out[name] = true
		}
	}
	return out
}

// Reject input before defaults/hooks. Later trusted assignments are preserved.
func (s *Server) dropConcealedInput(r *http.Request, entity *metadata.Entity, form *metadata.FormModule, fields map[string]any) []string {
	var dropped []string
	for name := range s.unrevealedConcealedFields(r, entity, form) {
		for key := range fields {
			if strings.EqualFold(key, name) {
				delete(fields, key)
			}
		}
		dropped = append(dropped, name)
	}
	removeSubmittedFormFields(r, dropped)
	return dropped
}

func (s *Server) restoreConcealControllers(r *http.Request, entity *metadata.Entity, form *metadata.FormModule, obj *runtime.Object) {
	rules, err := metadata.FormConcealRules(entity, form)
	if err != nil {
		return
	}
	grants := s.concealedGrants(r.Context(), form)
	for _, rule := range rules {
		if rule.Controller != "" {
			obj.Set(rule.Controller, grants[strings.ToLower(rule.Field)] != rule.Negated)
		}
	}
}

// Only explicit assignments by a successful trusted event can issue or revoke.
// Ordinary reads/submits never refresh a grant's expiry.
func (s *Server) finishConcealEvent(r *http.Request, form *metadata.FormModule, entity *metadata.Entity, obj *runtime.Object, assigned map[string]struct{}, success bool) {
	cr, _ := r.Context().Value(concealRequestKey{}).(*concealRequest)
	if cr == nil {
		return
	}
	e, token := cr.instance()
	if e == nil {
		return
	}
	if obj.ID.String() != e.object && e.object != "" {
		delete(s.concealInstances, token)
		return
	}
	if e.object == "" && savedConcealObject(s, r, entity, obj) {
		delete(s.concealInstances, token)
		return
	}
	if !success {
		clear(e.grants)
		return
	}
	rules, err := metadata.FormConcealRules(entity, form)
	if err != nil {
		clear(e.grants)
		return
	}
	for _, rule := range rules {
		if rule.Controller == "" {
			continue
		}
		if _, ok := assigned[strings.ToLower(rule.Controller)]; !ok {
			continue
		}
		v, ok := obj.Get(rule.Controller).(bool)
		if ok && rule.Allows(v) {
			e.grants[rule.Field] = s.concealNow().Add(concealTTL)
			e.expires = s.concealNow().Add(concealInstanceTTL)
		} else {
			delete(e.grants, rule.Field)
		}
	}
}

func savedConcealObject(s *Server, r *http.Request, entity *metadata.Entity, obj *runtime.Object) bool {
	_, exists, err := s.store.EntityVersionExists(r.Context(), entity.Name, obj.ID)
	return err != nil || exists
}
