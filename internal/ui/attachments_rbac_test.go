package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// attachTestServer returns a Server backed by a temp SQLite DB whose file
// storage points at a temp dir, plus a seeder for attachments owned by a
// catalog entity.
func attachTestServer(t *testing.T) (*Server, *storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	db.SetFilesDir(t.TempDir())
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("EnsureAttachmentTable: %v", err)
	}
	return &Server{store: db}, db, ctx
}

func seedAttachment(t *testing.T, db *storage.DB, ctx context.Context, ownerName string) storage.Attachment {
	t.Helper()
	att, err := db.UploadAttachment(ctx, "catalog", ownerName, uuid.New(),
		"f.txt", "text/plain", "", bytes.NewReader([]byte("вложение")), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	return att
}

func driveAttachment(t *testing.T, h http.HandlerFunc, method, aid string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/ui/attachments/"+aid, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("aid", aid)
	c := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		c = auth.ContextWithUser(c, user)
	}
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(c))
	return rec
}

// TestAttachmentDownload_AuthByOwner: скачивание вложения требует право ЧТЕНИЯ
// сущности-владельца — защита от IDOR. Право «запись» само по себе скачивание
// НЕ даёт (SEC-02, #777): единственное исключение — предпросмотр загрузчиком
// только что загруженного файла, проверяется отдельно.
func TestAttachmentDownload_AuthByOwner(t *testing.T) {
	s, db, ctx := attachTestServer(t)
	att := seedAttachment(t, db, ctx, "Контрагенты")
	id := att.ID.String()

	cases := []struct {
		name string
		user *auth.User
		want int
	}{
		{"владелец, есть read", catalogUser("Контрагенты", "read"), http.StatusOK},
		{"владелец, есть только write — скачивание запрещено", catalogUser("Контрагенты", "write"), http.StatusForbidden},
		{"нет прав на владельца", catalogUser("Другое", "read"), http.StatusForbidden},
		{"без пользователей (открытый деплой)", nil, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := driveAttachment(t, s.attachmentDownload, "GET", id, c.user)
			if rec.Code != c.want {
				t.Fatalf("код %d, ожидался %d", rec.Code, c.want)
			}
		})
	}
}

// TestAttachmentDownload_UploaderPreview: загрузчик может скачать ТОЛЬКО ЧТО
// загруженный им файл даже без права read на владельца (узкий предпросмотр,
// SEC-02, #777), но это не распространяется на других пользователей с write.
func TestAttachmentDownload_UploaderPreview(t *testing.T) {
	s, db, ctx := attachTestServer(t)
	att, err := db.UploadAttachment(ctx, "catalog", "Контрагенты", uuid.New(),
		"f.txt", "text/plain", "alice", bytes.NewReader([]byte("вложение")), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	id := att.ID.String()

	writeOnly := func(login string) *auth.User {
		return &auth.User{Login: login, Roles: []*auth.Role{{
			Permissions: auth.Permission{Catalogs: map[string][]string{"Контрагенты": {"write"}}},
		}}}
	}

	// Загрузчик (alice) без права read на владельца — предпросмотр разрешён.
	if rec := driveAttachment(t, s.attachmentDownload, "GET", id, writeOnly("alice")); rec.Code != http.StatusOK {
		t.Fatalf("загрузчик не смог скачать свой свежий файл: код %d", rec.Code)
	}
	// Другой пользователь (bob) с тем же write — доступа нет.
	if rec := driveAttachment(t, s.attachmentDownload, "GET", id, writeOnly("bob")); rec.Code != http.StatusForbidden {
		t.Fatalf("чужой write-пользователь скачал вложение: код %d", rec.Code)
	}
	// Совпадение login недостаточно после отзыва write.
	if rec := driveAttachment(t, s.attachmentDownload, "GET", id, &auth.User{Login: "alice"}); rec.Code != http.StatusForbidden {
		t.Fatalf("загрузчик без актуального write скачал вложение: код %d", rec.Code)
	}

	// Время загрузки из будущего не растягивает окно предпросмотра.
	future := att
	future.UploadedAt = time.Now().Add(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/ui/attachments/"+id, nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), writeOnly("alice")))
	if s.uploaderPreviewAllowed(req, &future) {
		t.Fatal("вложение с временем из будущего признано свежим")
	}
}

// TestAttachmentDelete_AuthByOwner: удаление вложения требует право записи на
// сущность-владельца — защита от IDOR.
func TestAttachmentDelete_AuthByOwner(t *testing.T) {
	s, db, ctx := attachTestServer(t)

	cases := []struct {
		name string
		user *auth.User
		want int
	}{
		{"нет прав на владельца", catalogUser("Другое", "write"), http.StatusForbidden},
		{"есть write", catalogUser("Контрагенты", "write"), http.StatusSeeOther},
		{"без пользователей (открытый деплой)", nil, http.StatusSeeOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			att := seedAttachment(t, db, ctx, "Контрагенты")
			rec := driveAttachment(t, s.attachmentDelete, "POST", att.ID.String(), c.user)
			if rec.Code != c.want {
				t.Fatalf("код %d, ожидался %d", rec.Code, c.want)
			}
		})
	}
}
