package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestAPI_FieldDisclosurePolicy(t *testing.T) {
	cat := clientEntityAPI()
	h, ctx := newAPITestHandler(t, []*metadata.Entity{cat}, nil)
	if err := h.store.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	secret := "synthetic-restricted-field"
	if err := h.store.Upsert(ctx, "Клиент", id, map[string]any{"Телефон": secret}, cat); err != nil {
		t.Fatal(err)
	}
	no := false
	u := maskUser([]string{"read", "disclose"}, auth.FieldPolicies{"Телефон": {Read: "hide", Disclose: &no}})
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", http.StatusOK},
		{"?disclose=1&reason=synthetic", http.StatusForbidden},
	} {
		r := withUser(reqWithEntity(http.MethodGet, "/catalogs/Клиент/"+id.String()+"/field/Телефон"+tc.query, nil, map[string]string{"name": "Клиент", "id": id.String(), "field": "Телефон"}, nil), u)
		w := httptest.NewRecorder()
		h.discloseField(metadata.KindCatalog).ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("restricted field exposed through REST")
		}
	}
}
