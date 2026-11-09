package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the mounted image route behind real session authentication, with
// roles assigned before serving. Inline and attachment share the same RLS gate
// and streaming backend; a query parameter must never grant extra access.
func TestImageDownloadRouter(t *testing.T) {
	for _, filesystem := range []bool{false, true} {
		name := "database"
		if filesystem {
			name = "filesystem"
		}
		t.Run(name, func(t *testing.T) {
			entity := ownerCatalog("Photos", metadata.Field{Name: "Picture", Type: metadata.FieldTypeImage})
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
			srv.store.SetFilesDir(t.TempDir())
			mode := storage.FileStorageDB
			if filesystem {
				mode = storage.FileStorageDisk
			}
			if err := srv.store.SaveFileStorageMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			if err := srv.store.EnsureBlobTable(ctx); err != nil {
				t.Fatal(err)
			}
			repo := auth.NewRepo(srv.store)
			if err := repo.EnsureSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := repo.EnsureRolesSchema(ctx); err != nil {
				t.Fatal(err)
			}
			role := rowOwnerUser("viewer", entity.Name, "read").Roles[0]
			role.Name = "image-reader"
			if err := repo.SyncRoles(ctx, []*auth.Role{role}); err != nil {
				t.Fatal(err)
			}
			user, err := repo.Create(ctx, "viewer", "V13wer-Test-Password!", "Viewer", false)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.AssignRole(ctx, user.ID, role.ID); err != nil {
				t.Fatal(err)
			}
			token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
			if err != nil {
				t.Fatal(err)
			}
			srv.authRepo = repo
			router := chi.NewRouter()
			srv.Mount(router)
			handler := repo.Middleware(router)
			request := func(path string, authenticated bool) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if authenticated {
					req.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			for _, tc := range []struct{ mime, ext string }{{"image/png", ".png"}, {"image/jpeg", ".jpg"}, {"image/gif", ".gif"}, {"image/webp", ".webp"}, {"application/octet-stream", ".bin"}, {"image/svg+xml", ".bin"}} {
				t.Run(tc.mime, func(t *testing.T) {
					body := []byte("same original bytes: " + tc.mime)
					blob, err := srv.store.PutBlob(ctx, tc.mime, bytes.NewReader(body), 1<<20, storage.BlobOwner{Kind: "catalog", Entity: entity.Name})
					if err != nil {
						t.Fatal(err)
					}
					seedOwnerRow(t, ctx, srv, entity, "viewer", map[string]any{"Picture": blob.ID.String()})
					path := "/ui/_image/" + blob.ID.String()
					inline := request(path, true)
					download := request(path+"?download=1&filename=evil.html&mime=text/html", true)
					for _, rec := range []*httptest.ResponseRecorder{inline, download} {
						if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
							t.Fatalf("status=%d body=%q", rec.Code, rec.Body.Bytes())
						}
						if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
							t.Fatalf("unsafe headers: %v", rec.Header())
						}
					}
					if got := download.Header().Get("Content-Disposition"); got != `attachment; filename="image-`+blob.ID.String()+tc.ext+`"` {
						t.Fatalf("disposition=%q", got)
					}
					for _, h := range []string{"Content-Type", "Content-Length", "Content-Security-Policy", "Cache-Control", "X-Content-Type-Options"} {
						if download.Header().Get(h) != inline.Header().Get(h) {
							t.Fatalf("%s changed between inline/download", h)
						}
					}
					for _, query := range []string{"", "?download=0", "?download=true", "?download=01"} {
						if got := request(path+query, true).Header().Get("Content-Disposition"); got != "inline" {
							t.Fatalf("query=%q disposition=%q", query, got)
						}
					}
					if got := request(path+"?download=1", false).Code; got != http.StatusUnauthorized {
						t.Fatalf("anonymous status=%d", got)
					}
				})
			}
			hidden, err := srv.store.PutBlob(ctx, "image/png", strings.NewReader("private"), 1<<20, storage.BlobOwner{Kind: "catalog", Entity: entity.Name})
			if err != nil {
				t.Fatal(err)
			}
			seedOwnerRow(t, ctx, srv, entity, "someone-else", map[string]any{"Picture": hidden.ID.String()})
			for _, query := range []string{"", "?download=1"} {
				if rec := request("/ui/_image/"+hidden.ID.String()+query, true); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "private") {
					t.Fatalf("RLS status=%d body=%s", rec.Code, rec.Body.String())
				}
				if got := request("/ui/_image/"+uuid.NewString()+query, true).Code; got != http.StatusNotFound {
					t.Fatalf("missing blob status=%d", got)
				}
			}
		})
	}
}
