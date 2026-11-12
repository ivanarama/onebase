package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestTaskWidgetAppearanceThroughHTTP(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("MSK", 3*3600)
	t.Cleanup(func() { time.Local = local })
	s, router, _, _ := newWidgetPartialFixture(t)
	widget := s.reg.GetWidget("Товары")
	widget.Roles = []string{"OTK"}
	widget.ExceptRoles = []string{"KC"}
	widget.Background = "#e3edf7"
	widget.Query = "SELECT Наименование, '#F00' AS TextColor, '2026-11-12T08:05:07Z' AS Moment FROM Catalog.Товар"
	widget.RowStyle = &metadata.WidgetRowStyle{Color: "TextColor"}
	widget.Columns = []metadata.WidgetColumn{{Field: "Наименование"}, {Field: "Moment", Format: "time"}}
	get := func(target string, roles ...string) *httptest.ResponseRecorder {
		t.Helper()
		user := &auth.User{ID: "test-user", Login: "test", IsAdmin: true}
		for _, r := range roles {
			user.Roles = append(user.Roles, &auth.Role{Name: r})
		}
		req := httptest.NewRequest("GET", target, nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	endpoint := "/ui/_widget/" + url.PathEscape(widget.Name)
	if rec := get(endpoint, "KC"); rec.Code != 404 {
		t.Fatalf("hidden role: %d", rec.Code)
	}
	rec := get(endpoint, "OTK")
	if rec.Code != 200 {
		t.Fatalf("OTK: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.HTML, "color:#ff0000") || !strings.Contains(body.HTML, ">11:05:07<") || strings.Contains(body.HTML, "TextColor</th>") {
		t.Fatalf("color/time: %s", body.HTML)
	}
	if rec := get(endpoint, "OTK", "KC"); rec.Code != 404 {
		t.Fatalf("except after cached result: %d", rec.Code)
	}
	home := get("/ui/", "OTK")
	if home.Code != 200 || !strings.Contains(home.Body.String(), "background-color:#e3edf7") {
		t.Fatalf("background: %d %s", home.Code, home.Body.String())
	}
	widget.Background = "red;background-image:url(https://invalid/)"
	home = get("/ui/", "OTK")
	if strings.Contains(home.Body.String(), "background-image:url") {
		t.Fatal("unsafe background emitted")
	}
}

func TestTaskListColorRulesThroughHTTP(t *testing.T) {
	ent := &metadata.Entity{Name: "TasksColor", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Priority", Type: metadata.FieldTypeString}, {Name: "Color", Type: metadata.FieldTypeString},
	}, Forms: []*metadata.FormModule{{Name: "List", Kind: "list", LayoutKind: metadata.FormLayoutManaged, ListRowStyle: &metadata.FormListRowStyle{ColorFrom: "Color", Rules: []metadata.FormListRowStyleRule{
		{Field: "Owner", Equals: "@ТекущийПользователь", Color: "#008000"}, {Field: "Priority", Equals: "1", Color: "#ff0000"},
	}}}}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	user := &auth.User{ID: uuid.NewString(), Login: "tester", IsAdmin: true}
	for _, row := range []map[string]any{{"Наименование": "assigned", "Owner": user.ID, "Priority": "1"}, {"Наименование": "priority", "Priority": "1"}, {"Наименование": "completed", "Color": "#888888"}} {
		if err := s.store.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
			t.Fatal(err)
		}
	}
	req := reqWithChi(http.MethodGet, "/ui/catalog/TasksColor", nil, map[string]string{"entity": ent.Name})
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, color := range []string{"#008000", "#ff0000", "#888888"} {
		if !strings.Contains(rec.Body.String(), "color:"+color) {
			t.Fatalf("missing %s", color)
		}
	}
}
