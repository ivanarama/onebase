package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Real HTTP disclosure boundary: an object-level disclose grant must not
// reveal a field explicitly excluded by the reading role's field policy.
func TestUI_FieldDisclosurePolicy(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	if err := s.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	secret := "synthetic-protected-address"
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{"Адрес": secret}, cat); err != nil {
		t.Fatal(err)
	}
	no, yes := false, true
	for _, tc := range []struct {
		name     string
		policy   *bool
		disclose bool
		want     int
	}{
		{"field-denied", &no, true, http.StatusForbidden},
		{"legacy", nil, true, http.StatusOK},
		{"explicit-allow", &yes, true, http.StatusOK},
		{"field-allow-does-not-grant-object-right", &yes, false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := []string{"read"}
			if tc.disclose {
				ops = append(ops, "disclose")
			}
			u := uiMaskUser(ops, auth.FieldPolicies{"Адрес": {Read: "hide", Disclose: tc.policy}})
			router := chi.NewRouter()
			router.Post("/ui/{kind}/{entity}/{id}/disclose", func(w http.ResponseWriter, r *http.Request) {
				c := auth.ContextWithUser(r.Context(), u)
				c = storage.WithAuditUser(c, u.ID, u.Login)
				s.discloseField(w, r.WithContext(c))
			})
			body := url.Values{"field": {"Адрес"}, "reason": {"synthetic regression"}}
			r := httptest.NewRequest(http.MethodPost, "/ui/catalog/Клиент/"+id.String()+"/disclose", strings.NewReader(body.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden && strings.Contains(w.Body.String(), secret) {
				t.Fatal("denied response exposed field")
			}
			if tc.want == http.StatusOK && !strings.Contains(w.Body.String(), secret) {
				t.Fatal("allowed disclosure lost legacy value")
			}
		})
	}
}
