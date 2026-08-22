package ui

// Тесты плана 127: отдача опубликованных вложений по /pub/{token}.
// Запросы идут через смонтированный роутер — тем же путём, что у анонимного
// посетителя, без cookie и токенов.

import (
	"context"
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
	if err := db.EnsureBlobTable(ctx); err != nil {
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
	if cc := w.Header().Get("Cache-Control"); !publicFileCacheRequiresRevalidation(cc) {
		t.Errorf("Cache-Control=%q — отзываемая ссылка может остаться fresh в кэше", cc)
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

// Картинка из поля image отдаётся тем же публичным маршрутом, что и вложение:
// у пользователя «ссылка на файл» одна, независимо от того, где платформа
// хранит содержимое.
func TestPublicFile_BlobServedAnonymously(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	blob, err := db.PutBlob(t.Context(), "image/png", strings.NewReader("PNG-BYTES"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	token, err := db.PublishBlob(t.Context(), blob.ID, storage.PublishOptions{CacheSeconds: 300, Filename: "logo.png"})
	if err != nil {
		t.Fatalf("PublishBlob: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "PNG-BYTES" {
		t.Errorf("тело=%q", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type=%q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "logo.png") {
		t.Errorf("Content-Disposition=%q — имя из опций не применилось", cd)
	}
	if cc := w.Header().Get("Cache-Control"); !publicFileCacheRequiresRevalidation(cc) {
		t.Errorf("Cache-Control=%q — отзываемая ссылка может остаться fresh в кэше", cc)
	}
}

func publicFileCacheRequiresRevalidation(cc string) bool {
	return strings.Contains(cc, "no-cache") &&
		strings.Contains(cc, "max-age=0") &&
		strings.Contains(cc, "must-revalidate")
}

// Range работает и для картинок: они читаются в память, но отдаются тем же
// ServeContent.
func TestPublicFile_BlobRange(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	blob, err := db.PutBlob(t.Context(), "image/png", strings.NewReader("0123456789"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	token, err := db.PublishBlob(t.Context(), blob.ID, storage.PublishOptions{})
	if err != nil {
		t.Fatalf("PublishBlob: %v", err)
	}

	req := httptest.NewRequest("GET", "/pub/"+token, nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("status=%d, ожидался 206", w.Code)
	}
	if got := w.Body.String(); got != "0123" {
		t.Errorf("кусок=%q", got)
	}
}

// Удалённая картинка даёт 404, а не 500: у блобов нет каскада, поэтому запись
// публикации переживает файл, и отдача обязана это пережить корректно.
func TestPublicFile_DeletedBlob404(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	blob, err := db.PutBlob(t.Context(), "image/png", strings.NewReader("PNG"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	token, err := db.PublishBlob(t.Context(), blob.ID, storage.PublishOptions{})
	if err != nil {
		t.Fatalf("PublishBlob: %v", err)
	}
	if err := db.DeleteBlob(t.Context(), blob.ID); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, ожидался 404", w.Code)
	}
}

// У блобов нет времени загрузки, поэтому условные запросы для них держатся на
// ETag от токена: повторный визит с If-None-Match не должен тянуть тело заново.
func TestPublicFile_BlobConditionalByETag(t *testing.T) {
	_, r, db := newPublicFilesServer(t)
	blob, err := db.PutBlob(t.Context(), "image/png", strings.NewReader("PNG-BYTES"), 1<<20,
		storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	token, err := db.PublishBlob(t.Context(), blob.ID, storage.PublishOptions{})
	if err != nil {
		t.Fatalf("PublishBlob: %v", err)
	}

	first := httptest.NewRecorder()
	r.ServeHTTP(first, httptest.NewRequest("GET", "/pub/"+token, nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("нет ETag — условные запросы для блобов не работают")
	}
	// Делаем содержимое заведомо нечитаемым, не удаляя живой capability-токен.
	// Если условный запрос попытается открыть blob до проверки ETag, он вернёт
	// 404 из-за size mismatch вместо 304.
	if _, err := db.Exec(t.Context(), `UPDATE _blobs SET size = size + 1 WHERE id = ?`, blob.ID.String()); err != nil {
		t.Fatalf("повредить blob для проверки ранней ревалидации: %v", err)
	}

	req := httptest.NewRequest("GET", "/pub/"+token, nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	r.ServeHTTP(second, req)
	if second.Code != http.StatusNotModified {
		t.Fatalf("повтор с If-None-Match: status=%d, ожидался 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 пришёл с телом: %q", second.Body.String())
	}
}

// Анонимная поверхность обязана иметь предел одновременных отдач: без него
// N медленных клиентов держат N буферов тела в памяти единственного бинаря.
// Но превышение предела — это ОЖИДАНИЕ, а не отказ: страница с двумя десятками
// картинок штатно даёт всплеск запросов, и 503 читался бы как сломанный сайт.
func TestPublicFile_ConcurrencyWaitsNotRejects(t *testing.T) {
	s, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "big.bin", "application/pdf", "данные", storage.PublishOptions{})

	// Занимаем все слоты лимитера — как будто столько отдач уже висит.
	s.ops = newOperationLimiter()
	var releases []func()
	for i := 0; i < publicFileServeConcurrency; i++ {
		release, ok := s.ops.tryAcquire(opPublicFileServe, publicFileServeConcurrency)
		if !ok {
			t.Fatalf("слот %d не занялся", i)
		}
		releases = append(releases, release)
	}

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil))
		done <- w.Code
	}()

	// Пока слоты заняты, запрос обязан ждать, а не отвечать отказом.
	select {
	case code := <-done:
		t.Fatalf("запрос при занятых слотах ответил сразу (%d) вместо ожидания", code)
	case <-time.After(150 * time.Millisecond):
	}

	// Освобождение слота пропускает ожидающего.
	releases[0]()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("после освобождения слота status=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ожидающий запрос не подхватил освободившийся слот")
	}
	for _, rel := range releases[1:] {
		rel()
	}
}

// Безнадёжное ожидание всё-таки обрывается: отключившийся клиент (отменённый
// контекст) не должен висеть в очереди до упора.
func TestPublicFile_AbandonedWaitGivesUp(t *testing.T) {
	s, r, db := newPublicFilesServer(t)
	token := publishTestFile(t, db, "big.bin", "application/pdf", "данные", storage.PublishOptions{})

	s.ops = newOperationLimiter()
	for i := 0; i < publicFileServeConcurrency; i++ {
		if _, ok := s.ops.tryAcquire(opPublicFileServe, publicFileServeConcurrency); !ok {
			t.Fatalf("слот %d не занялся", i)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/pub/"+token, nil).WithContext(ctx))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("отменённый запрос при занятых слотах: status=%d, ожидался 503", w.Code)
	}
}
