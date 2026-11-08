package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Раскрываем папку публичным HTTP-запросом и передаём его настоящий JSON
// штатной makeTreeRow: проверка только серверного ответа не ловит потерю CSS.
func TestListRowStyleLazyTree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("для проверки отображения дерева нужен node")
	}
	ad := &metadata.Entity{Name: "Реклама", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Цвет", Type: metadata.FieldTypeString},
	}}
	task := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Реклама", Type: metadata.FieldType("reference:Реклама"), RefEntity: "Реклама"},
		{Name: "Срочно", Type: metadata.FieldTypeBool},
	}}
	task.Forms = []*metadata.FormModule{{Name: "Список", Kind: "list", LayoutKind: metadata.FormLayoutManaged,
		ListRowStyle: &metadata.FormListRowStyle{BackgroundFrom: "Реклама.Цвет", BoldFrom: "Срочно",
			Rules: []metadata.FormListRowStyleRule{{Field: "Наименование", Equals: "Правило", Background: "#ffd"}}},
	}}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ad, task})
	put := func(e *metadata.Entity, values map[string]any) string {
		t.Helper()
		id := uuid.New()
		if err := s.store.Upsert(context.Background(), e.Name, id, values, e); err != nil {
			t.Fatal(err)
		}
		return id.String()
	}
	color := put(ad, map[string]any{"Цвет": "#00FF55"})
	bad := put(ad, map[string]any{"Цвет": "red;background:url(https://evil/x)"})
	parent := put(task, map[string]any{"Наименование": "Папка", "is_folder": true})
	child := put(task, map[string]any{"Наименование": "Цветная", "parent_id": parent, "Реклама": color, "Срочно": true})
	invalid := put(task, map[string]any{"Наименование": "Плохой цвет", "parent_id": parent, "Реклама": bad})
	folder := put(task, map[string]any{"Наименование": "Правило", "parent_id": parent, "is_folder": true, "Реклама": color})
	grandchild := put(task, map[string]any{"Наименование": "Вложенная", "parent_id": folder, "Реклама": color, "Срочно": true})
	operator := &auth.User{ID: "op", Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
		Catalogs: map[string][]string{"Задача": {"read"}},
	}}}}
	router := chi.NewRouter()
	s.Mount(router)
	for _, tc := range []struct {
		name, parent, depth string
		user                *auth.User
		want                map[string][]string
	}{
		{"children", parent, "0", nil, map[string][]string{child: {"#00ff55", "#111827", "600"}, invalid: {"", "", ""}, folder: {"#ffffdd", "#111827", ""}}},
		{"nested", folder, "1", nil, map[string][]string{grandchild: {"#00ff55", "#111827", "600"}}},
		{"denied_reference", parent, "0", operator, map[string][]string{child: {"", "", "600"}, invalid: {"", "", ""}, folder: {"#ffffdd", "#111827", ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ui/_tree-children/Задача?parent="+tc.parent+"&depth="+tc.depth, nil)
			if tc.user != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("раскрытие папки: %d %s", rec.Code, rec.Body.String())
			}
			input, err := json.Marshal(map[string]any{"response": json.RawMessage(rec.Body.Bytes()), "want": tc.want})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), node, "static/list_row_style_tree_probe.js") //nolint:gosec // test-only node resolved by LookPath; fixed script
			cmd.Stdin = bytes.NewReader(input)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("HTTP → makeTreeRow: %v\n%s\nJSON: %s", err, out, rec.Body.String())
			}
		})
	}
}
