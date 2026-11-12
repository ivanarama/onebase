package ui

import (
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListFormByRoleDoesNotChangeKC(t *testing.T) {
	base := &metadata.FormModule{Name: "Base", Kind: "list", LayoutKind: metadata.FormLayoutManaged, ListRowStyle: &metadata.FormListRowStyle{Rules: []metadata.FormListRowStyleRule{{Field: "Priority", Equals: "1", Color: "#ff0000"}}}}
	otk := &metadata.FormModule{Name: "СписокСотрудника", Kind: "list", LayoutKind: metadata.FormLayoutManaged, ExceptRoles: []string{"ОператорКЦ"}, ListRowStyle: &metadata.FormListRowStyle{Rules: []metadata.FormListRowStyleRule{{Field: "Priority", Equals: "1", Color: "#008000"}}}}
	ent := &metadata.Entity{Name: "TaskRoles", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Priority", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{base, otk}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	if err := s.store.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "task", "Priority": "1"}, ent); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ role, color, other string }{{"ОТКСотрудник", "#008000", "#ff0000"}, {"ОператорКЦ", "#ff0000", "#008000"}, {"ОТКСотрудник", "#008000", "#ff0000"}} {
		req := reqWithChi("GET", "/ui/catalog/TaskRoles", nil, map[string]string{"entity": ent.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "test", Login: "test", IsAdmin: true, Roles: []*auth.Role{{Name: tc.role}}}))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "color:"+tc.color) || strings.Contains(rec.Body.String(), "cursor:pointer;color:"+tc.other) {
			t.Fatalf("role %s: %d %s", tc.role, rec.Code, rec.Body.String())
		}
	}
	if len(otk.ExceptRoles) != 1 || len(ent.Forms) != 2 {
		t.Fatal("shared registry metadata mutated")
	}
}
