package ui

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func newAttachmentHTTPServer(t *testing.T) (*Server, *metadata.Entity, http.Handler) {
	t.Helper()
	entity := ownerCatalog("Вложения")
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	s.store.SetFilesDir(t.TempDir())
	if err := s.store.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	s.Mount(r)
	return s, entity, r
}

func uploadHTTPAttachment(t *testing.T, s *Server, entity *metadata.Entity, owner, filename, mimeType, uploadedBy, body string) storage.Attachment {
	t.Helper()
	id := seedOwnerRow(t, t.Context(), s, entity, owner, nil)
	att, err := s.store.UploadAttachment(t.Context(), "catalog", entity.Name, id,
		filename, mimeType, uploadedBy, strings.NewReader(body), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return att
}

func downloadAttachmentHTTP(r http.Handler, att storage.Attachment, user *auth.User, headers http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/ui/attachments/"+att.ID.String()+"/download", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	if headers != nil {
		req.Header = headers
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAttachmentHTTP_InlineMIMEPolicy(t *testing.T) {
	s, entity, r := newAttachmentHTTPServer(t)
	user := rowOwnerUser("alice", entity.Name, "read")
	cases := []struct {
		mimeType string
		inline   bool
	}{
		{"application/pdf", true}, {"text/plain", true},
		{"image/png", true}, {"image/jpeg", true},
		{"image/gif", true}, {"image/webp", true},
		{"", false}, {"application/octet-stream", false},
		{"image/svg+xml", false}, {"text/html", false},
		{"application/xhtml+xml", false}, {"image/bmp", false},
		{"image/tiff", false}, {"image/avif", false},
		{"video/mp4", false}, {"audio/mpeg", false},
		{"image/png-unknown", false}, {"text/plain-unknown", false},
	}
	for _, tc := range cases {
		// The same HTTP policy must hold for case/parameter variations.
		for _, mimeType := range []string{tc.mimeType, "  " + strings.ToUpper(tc.mimeType) + " ; charset=utf-8"} {
			t.Run(mimeType, func(t *testing.T) {
				// A safe-looking extension must never authorize active MIME content.
				filename := "отчёт \"июль\".png"
				body := "<script>alert(1)</script>"
				att := uploadHTTPAttachment(t, s, entity, "alice", filename, mimeType, "", body)
				w := downloadAttachmentHTTP(r, att, user, nil)
				if w.Code != http.StatusOK || w.Body.String() != body {
					t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
				}
				wantDisposition, wantType := "attachment", "application/octet-stream"
				if tc.inline {
					wantDisposition, wantType = "inline", mimeType
				}
				disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
				if err != nil || disposition != wantDisposition || params["filename"] != filename {
					t.Errorf("Content-Disposition=%q, err=%v", w.Header().Get("Content-Disposition"), err)
				}
				if got := w.Header().Get("Content-Type"); got != wantType {
					t.Errorf("Content-Type=%q, want %q", got, wantType)
				}
				if got := w.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
					t.Errorf("CSP=%q", got)
				}
				if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
					t.Errorf("X-Content-Type-Options=%q", got)
				}
			})
		}
	}
}

func TestAttachmentHTTP_RangeAndConditional(t *testing.T) {
	s, entity, r := newAttachmentHTTPServer(t)
	user := rowOwnerUser("alice", entity.Name, "read")
	for _, mimeType := range []string{"application/pdf", "image/svg+xml"} {
		t.Run(mimeType, func(t *testing.T) {
			att := uploadHTTPAttachment(t, s, entity, "alice", "файл.pdf", mimeType, "", "0123456789")
			full := downloadAttachmentHTTP(r, att, user, nil)
			lastModified := full.Header().Get("Last-Modified")
			if full.Code != http.StatusOK || lastModified == "" {
				t.Fatalf("full status=%d Last-Modified=%q", full.Code, lastModified)
			}
			partial := downloadAttachmentHTTP(r, att, user, http.Header{"Range": {"bytes=2-5"}})
			if partial.Code != http.StatusPartialContent || partial.Body.String() != "2345" || partial.Header().Get("Content-Range") != "bytes 2-5/10" {
				t.Fatalf("range status=%d headers=%v body=%q", partial.Code, partial.Header(), partial.Body.String())
			}
			for _, header := range []string{"Content-Type", "Content-Disposition", "Content-Security-Policy", "X-Content-Type-Options"} {
				if partial.Header().Get(header) != full.Header().Get(header) {
					t.Errorf("range changed %s", header)
				}
			}
			conditional := downloadAttachmentHTTP(r, att, user, http.Header{"If-Modified-Since": {lastModified}})
			if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
				t.Fatalf("conditional status=%d body=%q", conditional.Code, conditional.Body.String())
			}
			if conditional.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" || conditional.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("conditional lost security headers: %v", conditional.Header())
			}
			denied := downloadAttachmentHTTP(r, att, rowOwnerUser("bob", entity.Name, "read"), http.Header{"If-Modified-Since": {lastModified}, "Range": {"bytes=2-5"}})
			if denied.Code != http.StatusForbidden {
				t.Fatalf("conditional/range bypassed RLS: status=%d", denied.Code)
			}
		})
	}
}

func TestAttachmentHTTP_OwnerAndUploaderAuthorization(t *testing.T) {
	s, entity, r := newAttachmentHTTPServer(t)
	att := uploadHTTPAttachment(t, s, entity, "alice", "картинка.png", "image/png", "alice", "secret")
	writer := catalogUser(entity.Name, "write")
	writer.Login = "alice"
	otherWriter := catalogUser(entity.Name, "write")
	otherWriter.Login = "bob"
	for _, tc := range []struct {
		name string
		user *auth.User
		want int
	}{
		{"owner read", rowOwnerUser("alice", entity.Name, "read"), http.StatusOK},
		{"hidden owner", rowOwnerUser("bob", entity.Name, "read"), http.StatusForbidden},
		{"fresh uploader", writer, http.StatusOK},
		{"other writer", otherWriter, http.StatusForbidden},
		{"revoked write", &auth.User{Login: "alice"}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := downloadAttachmentHTTP(r, att, tc.user, nil)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if tc.want == http.StatusOK && !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline;") {
				t.Errorf("authorized image is not inline: %v", w.Header())
			}
		})
	}
	for _, uploadedAt := range []time.Time{time.Now().Add(-attachmentPreviewWindow - time.Minute), time.Now().Add(time.Minute)} {
		if _, err := s.store.Exec(t.Context(), "UPDATE _attachments SET uploaded_at=? WHERE id=?", uploadedAt, att.ID.String()); err != nil {
			t.Fatal(err)
		}
		if w := downloadAttachmentHTTP(r, att, writer, nil); w.Code != http.StatusForbidden {
			t.Fatalf("stale/future uploader preview status=%d", w.Code)
		}
	}
}
