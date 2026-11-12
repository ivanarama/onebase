package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestWidgetColumnWidthsPerUserThroughHTTP(t *testing.T) {
	_, router, _, _ := newWidgetPartialFixture(t)
	anna := &auth.User{ID: "stable-anna", Login: "anna", IsAdmin: true}
	boris := &auth.User{ID: "stable-boris", Login: "boris", IsAdmin: true}
	endpoint := "/ui/_widget/" + url.PathEscape("Товары")
	do := func(method, target, raw string, u *auth.User) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	save := func(raw string, u *auth.User) *httptest.ResponseRecorder {
		return do("POST", endpoint+"/widths", url.Values{"widths": {raw}}.Encode(), u)
	}
	if rec := save(`{"наименование":240}`, anna); rec.Code != 204 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	html := func(u *auth.User) string {
		t.Helper()
		rec := do("GET", endpoint, "", u)
		if rec.Code != 200 {
			t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
		}
		var res struct {
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res.HTML
	}
	if got := html(anna); !strings.Contains(got, "width:240px") || !strings.Contains(got, "data-ob-widget-column-grip") {
		t.Fatalf("saved widths missing: %s", got)
	}
	if got := html(boris); strings.Contains(got, "ob-widget-fixed") {
		t.Fatalf("other user inherited widths: %s", got)
	}
	anna.Login = "renamed"
	if got := html(anna); !strings.Contains(got, "width:240px") {
		t.Fatalf("rename lost stable ID setting: %s", got)
	}
	for _, bad := range []string{`{"unknown":200}`, `{"наименование":0}`, `{"наименование":1201}`, `[]`} {
		if rec := save(bad, anna); rec.Code != 400 {
			t.Fatalf("bad %s: %d", bad, rec.Code)
		}
	}
	noRights := &auth.User{ID: "no-rights", Login: "none"}
	if rec := save(`{"наименование":200}`, noRights); rec.Code != http.StatusForbidden {
		t.Fatalf("permission: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/ui/_widget/Скрытый/widths", url.Values{"widths": {`{}`}}.Encode(), anna); rec.Code != 404 {
		t.Fatalf("hidden: %d", rec.Code)
	}
	if rec := save(`{}`, anna); rec.Code != 204 {
		t.Fatalf("reset: %d", rec.Code)
	}
	if strings.Contains(html(anna), "ob-widget-fixed") {
		t.Fatal("reset did not clear widths")
	}
}
