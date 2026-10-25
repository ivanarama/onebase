package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Real managed HTML, static JS, ref-options, RLS and save routes are served by
// production handlers. Only the authenticated test user is supplied by middleware;
// no endpoint response, selected_allowed result or client function is mocked.
func TestChoiceFilterBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, testChoiceFilterBrowserAcceptance)
}

func testChoiceFilterBrowserAcceptance(t *testing.T, db *storage.DB) {
	f := newChoiceHTTPFixtureWithStore(t, db, false)
	f.target.Hierarchical = true
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.target}); err != nil {
		t.Fatal(err)
	}
	form, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(browseracceptance.Root(), "e2e/fixtures/choice-filter/forms/заявка/объекта.form.yaml"), f.owner.Name)
	if err != nil {
		t.Fatal(err)
	}
	f.owner.Forms = []*metadata.FormModule{form}
	folder := choiceHTTPUUID(0x20, 55)
	if err := f.server.store.Upsert(context.Background(), f.target.Name, folder, map[string]any{
		"Наименование": "needle folder", "Направление": f.rootA.String(), "Аудитория": "anna", "is_folder": true,
	}, f.target); err != nil {
		t.Fatal(err)
	}
	pageTwoOwner := choiceHTTPUUID(0x30, 2)
	if err := f.server.store.Upsert(context.Background(), f.owner.Name, pageTwoOwner, map[string]any{
		"Направление": choiceHTTPUUID(0x10, 2).String(), "Неисправность": f.pageTwo.String(),
	}, f.owner); err != nil {
		t.Fatal(err)
	}
	f.server = New(f.server.reg, f.server.store, interpreter.New(), nil, Config{}, nil)
	router := chi.NewRouter()
	f.server.MountStatic(router)
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), f.user)))
			})
		})
		f.server.Mount(r)
	})
	server := httptest.NewServer(router)
	defer func() {
		f.server.CloseEventStreams()
		server.Close()
		f.server.Close()
	}()
	browseracceptance.Run(t, "runtime", map[string]string{
		"OB_ACCEPTANCE_URL": server.URL,
		"OB_CHOICE_IDS": fmt.Sprintf(`{"a":%q,"b":%q,"legacy":%q,"selected":%q,"hidden":%q,"folder":%q,"owner":%q,"pageTwoOwner":%q}`,
			f.rootA, f.rootB, f.legacySelected, f.pageTwo, f.hidden, folder, f.ownerID, pageTwoOwner),
	})
}
