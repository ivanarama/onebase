package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestRefOptionsIsRootAttribute(t *testing.T) {
	f := newParentChoiceFixture(t)
	f.target.Fields = append(f.target.Fields, metadata.Field{Name: "is_root", Type: metadata.FieldTypeBool})
	ctx := context.Background()
	if err := f.server.store.Migrate(ctx, []*metadata.Entity{f.target}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{f.tech, f.kitchen, f.other, f.kettle, f.iron, f.dryer, f.lamp} {
		if err := f.server.store.Upsert(ctx, f.target.Name, id, map[string]any{"is_root": id == f.kettle || id == f.dryer}, f.target); err != nil {
			t.Fatal(err)
		}
	}
	element := f.owner.Forms[0].Elements[1]
	element.ChoiceFolders = true
	element.ChoiceFilter = []metadata.FormChoiceCondition{{Field: "IS_ROOT", Op: metadata.FormChoiceOpEqual, Value: boolPtr(true)}}
	fetch := func(selected uuid.UUID) choiceHTTPResponse {
		t.Helper()
		query := url.Values{
			"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {element.ID},
			"sources": {"{}"}, "limit": {"100"}, "selected_id": {selected.String()},
		}
		router := chi.NewRouter()
		f.server.Mount(router)
		request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return decodeChoiceHTTP(t, recorder)
	}
	response := fetch(f.kettle)
	if labels := strings.Join(parentChoiceLabels(response), ","); labels != "чайник" || response.Total != 1 {
		t.Fatalf("configured boolean/RLS: total=%d items=%s", response.Total, labels)
	}
	if response.SelectedAllowed == nil || !*response.SelectedAllowed {
		t.Fatal("matching attribute rejected")
	}
	for _, id := range []uuid.UUID{f.tech, f.dryer} {
		if rejected := fetch(id); rejected.SelectedAllowed == nil || *rejected.SelectedAllowed {
			t.Fatalf("nonmatching or hidden record %s allowed", id)
		}
	}
	element.ChoiceFilter[0].Value = boolPtr(false)
	response = fetch(f.tech)
	if response.Total != 5 || response.SelectedAllowed == nil || !*response.SelectedAllowed {
		t.Fatalf("false attribute/root: %+v", response)
	}
}

// The public picker, not an internal predicate helper, must use is_root for
// items, total and selected_allowed under the operator's row access policy.
func TestRefOptionsIsRoot(t *testing.T) {
	f := newParentChoiceFixture(t)
	element := f.owner.Forms[0].Elements[1]
	fetch := func(selected uuid.UUID) choiceHTTPResponse {
		t.Helper()
		query := url.Values{
			"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"},
			"element": {element.ID}, "sources": {"{}"}, "limit": {"100"},
		}
		if selected != uuid.Nil {
			query.Set("selected_id", selected.String())
		}
		router := chi.NewRouter()
		f.server.Mount(router)
		request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+query.Encode(), nil)
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		return decodeChoiceHTTP(t, recorder)
	}

	element.ChoiceFilter = []metadata.FormChoiceCondition{
		{Field: "is_root", Op: metadata.FormChoiceOpEqual, Value: boolPtr(true)},
		{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPtr(true)},
	}
	root := fetch(f.tech)
	if labels := strings.Join(parentChoiceLabels(root), ","); labels != "Прочее,Техника" || root.Total != 2 {
		t.Fatalf("root folders: total=%d items=%s", root.Total, labels)
	}
	if root.SelectedAllowed == nil || !*root.SelectedAllowed {
		t.Fatalf("root selection rejected: %+v", root.SelectedAllowed)
	}
	if nested := fetch(f.kitchen); nested.SelectedAllowed == nil || *nested.SelectedAllowed {
		t.Fatalf("nested folder allowed as root: %+v", nested.SelectedAllowed)
	}

	element.ChoiceFilter = []metadata.FormChoiceCondition{
		{Field: "is_root", Op: metadata.FormChoiceOpEqual, Value: boolPtr(false)},
		{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPtr(false)},
	}
	nonRoot := fetch(f.kettle)
	if labels := strings.Join(parentChoiceLabels(nonRoot), ","); labels != "лампа,утюг,чайник" || nonRoot.Total != 3 {
		t.Fatalf("non-root items/RLS: total=%d items=%s", nonRoot.Total, labels)
	}
	if nonRoot.SelectedAllowed == nil || !*nonRoot.SelectedAllowed {
		t.Fatalf("nested selection rejected: %+v", nonRoot.SelectedAllowed)
	}
	if closed := fetch(f.dryer); closed.SelectedAllowed == nil || *closed.SelectedAllowed {
		t.Fatalf("RLS-protected record allowed: %+v", closed.SelectedAllowed)
	}
	// Combining the boolean pseudo-field with a reference subtree exclusion
	// must affect both the paginated list and selected_allowed.
	element.ChoiceFilter = append(element.ChoiceFilter, metadata.FormChoiceCondition{
		Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Ref: f.tech.String(),
	})
	outside := fetch(f.lamp)
	if labels := strings.Join(parentChoiceLabels(outside), ","); labels != "лампа" || outside.Total != 1 {
		t.Fatalf("nested items outside tech: total=%d items=%s", outside.Total, labels)
	}
	if outside.SelectedAllowed == nil || !*outside.SelectedAllowed {
		t.Fatal("nested item in another subtree rejected")
	}
	if excluded := fetch(f.kettle); excluded.SelectedAllowed == nil || *excluded.SelectedAllowed {
		t.Fatal("nested item in excluded subtree allowed")
	}

}
