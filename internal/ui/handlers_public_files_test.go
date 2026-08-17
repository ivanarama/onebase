package ui

// Тесты плана 127: отдача опубликованных вложений по /pub/{token}.
// Запросы идут через смонтированный роутер — тем же путём, что у анонимного
// посетителя, без cookie и токенов.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func newPublicFilesServer(t *testing.T) (*Server, http.Handler, *storage.DB) {
	t.Helper()
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "pub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		store:            db,
		reg:              runtime.NewRegistry(),
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
	}
	r := chi.NewRouter()
	s.MountServices(r)
	return s, r, db
}

func publishTestFile(t *testing.T, db *storage.DB, name, mime, body string, opts storage.PublishOptions) string {
	t.Helper()
	att, err := db.UploadAttachment(t.Context(), "catalog", "Товары", uuid.New(),
		name, mime, "tester", strings.NewReader(body), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	token, err := db.PublishAttachment(t.Context(), att.ID, opts)
	if err != nil {
		t.Fatalf("PublishAttachment: %v", err)
	}
	return token
}

// Главный сценарий: файл открывается вообще без авторизации.
func TestPublicFile_ServedAnonymously(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "logo.png", "image/png", "картинка", storage.PublishOptions{CacheSeconds: 600})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "картинка" {
		t.Errorf("тело=%q", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type=%q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=600") {
		t.Errorf("Cache-Control=%q", cc)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("Content-Disposition=%q — картинка должна открываться в браузере", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("нет nosniff")
	}
}

func TestPublicFile_UnknownToken404(t *testing.T) {
	_, r, _ := newPublicFilesServer(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/несуществующий", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, ожидался 404", w.Code)
	}
}

func TestPublicFile_RevokedToken404(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "a.png", "image/png", "данные", storage.PublishOptions{})
	pf, err := db.PublicFileByToken(t.Context(), token)
	if err != nil || pf == nil {
		t.Fatalf("PublicFileByToken: %v", err)
	}
	if err := db.UnpublishAttachment(t.Context(), pf.AttachmentID); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("отозванная ссылка отвечает %d вместо 404", w.Code)
	}
}

func TestPublicFile_Expired404(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	past := time.Now().Add(-time.Minute)
	token := publishTestFile(t, db, "a.png", "image/png", "данные", storage.PublishOptions{ExpiresAt: &past})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("истёкшая ссылка отвечает %d вместо 404", w.Code)
	}
}

func TestPublicFile_RangeAndConditional(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "big.bin", "video/mp4", "0123456789", storage.PublishOptions{})

	rr := httptest.NewRequest("GET", "/pub/"+token, nil)
	rr.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, rr)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("status=%d, ожидался 206", w.Code)
	}
	if got := w.Body.String(); got != "0123" {
		t.Errorf("кусок=%q, ожидался 0123", got)
	}

	full := httptest.NewRecorder()
	r.ServeHTTP(full, httptest.NewRequest("GET", "/pub/"+token, nil))
	lastMod := full.Header().Get("Last-Modified")
	if lastMod == "" {
		t.Fatal("нет Last-Modified — условные запросы работать не будут")
	}
	cond := httptest.NewRequest("GET", "/pub/"+token, nil)
	cond.Header.Set("If-Modified-Since", lastMod)
	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, cond)
	if cw.Code != http.StatusNotModified {
		t.Errorf("условный запрос вернул %d вместо 304", cw.Code)
	}
}

// HTML и SVG на своём домене — XSS с доступом к cookie админки, поэтому
// отдаются вложением с нейтральным типом. Регрессионный тест на вектор.
func TestPublicFile_HTMLAndSVGNotInline(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	for _, tc := range []struct{ name, mime string }{
		{"page.html", "text/html"},
		{"icon.svg", "image/svg+xml"},
	} {
		token := publishTestFile(t, db, tc.name, tc.mime, "<script>alert(1)</script>", storage.PublishOptions{})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))

		if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%s: Content-Type=%q — файл открылся бы как страница в origin платформы", tc.mime, ct)
		}
		if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s: Content-Disposition=%q", tc.mime, cd)
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
			t.Errorf("%s: CSP=%q", tc.mime, csp)
		}
	}
}

// Публичная отдача — та же поверхность наружу, что и /hs/*: при выключенной
// сети недоступна.
func TestPublicFile_NetworkDisabled(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "a.png", "image/png", "данные", storage.PublishOptions{})
	if err := db.SaveNetworkEnabled(t.Context(), false); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, ожидался 503 при выключенной сети", w.Code)
	}
}

// Имя из опций публикации важнее имени вложения: файл «прайс.pdf» можно отдать
// контрагенту как «прайс-август.pdf».
func TestPublicFile_FilenameOverride(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "internal-name.pdf", "application/pdf", "%PDF-1.4",
		storage.PublishOptions{Filename: "прайс-август.pdf"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "прайс-август.pdf") && !strings.Contains(cd, "%D0%BF") {
		t.Errorf("Content-Disposition=%q — имя из опций не применилось", cd)
	}
}
