package storage_test

// Матричные тесты публикации вложений (план 127): затрагивается схема и SQL,
// поэтому одно тело гоняется на SQLite и (при TEST_DATABASE_URL) на PostgreSQL —
// раздельные тесты расхождений диалектов не показывают.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// uploadTestAttachment кладёт вложение и возвращает его идентификатор.
func uploadTestAttachment(t *testing.T, db *storage.DB, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	att, err := db.UploadAttachment(ctx, "catalog", "Товары", uuid.New(),
		name, "image/png", "tester", strings.NewReader("данные"), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	return att.ID
}

func TestPublicFiles_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		t.Run("публикация идемпотентна", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "a.png")
			first, err := db.PublishAttachment(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			second, err := db.PublishAttachment(ctx, id, storage.PublishOptions{CacheSeconds: 60, Filename: "новое.png"})
			if err != nil {
				t.Fatalf("повторная публикация: %v", err)
			}
			if first != second {
				t.Fatalf("повторная публикация выдала другой токен: %q → %q", first, second)
			}
			pf, err := db.PublicFileByToken(ctx, first)
			if err != nil || pf == nil {
				t.Fatalf("PublicFileByToken: %v (pf=%v)", err, pf)
			}
			if pf.CacheSeconds != 60 || pf.Filename != "новое.png" {
				t.Errorf("параметры не обновились: %+v", pf)
			}
		})

		t.Run("токены непредсказуемы и уникальны", func(t *testing.T) {
			seen := map[string]bool{}
			for i := 0; i < 50; i++ {
				id := uploadTestAttachment(t, db, "f.png")
				token, err := db.PublishAttachment(ctx, id, storage.PublishOptions{})
				if err != nil {
					t.Fatalf("PublishAttachment: %v", err)
				}
				if len(token) != 43 {
					t.Fatalf("длина токена %d, ожидалось 43 (32 байта base64url)", len(token))
				}
				if seen[token] {
					t.Fatalf("повтор токена %q", token)
				}
				seen[token] = true
			}
		})

		t.Run("отзыв ломает ссылку", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "b.png")
			token, err := db.PublishAttachment(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			if err := db.UnpublishAttachment(ctx, id); err != nil {
				t.Fatalf("UnpublishAttachment: %v", err)
			}
			pf, err := db.PublicFileByToken(ctx, token)
			if err != nil {
				t.Fatalf("PublicFileByToken: %v", err)
			}
			if pf != nil {
				t.Fatalf("отозванный токен всё ещё действует: %+v", pf)
			}
		})

		t.Run("отзыв без публикации не ошибка", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "c.png")
			if err := db.UnpublishAttachment(ctx, id); err != nil {
				t.Fatalf("UnpublishAttachment без публикации: %v", err)
			}
		})

		t.Run("истёкшая публикация просрочена", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "d.png")
			past := time.Now().Add(-time.Hour)
			token, err := db.PublishAttachment(ctx, id, storage.PublishOptions{ExpiresAt: &past})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			pf, err := db.PublicFileByToken(ctx, token)
			if err != nil || pf == nil {
				t.Fatalf("PublicFileByToken: %v (pf=%v)", err, pf)
			}
			if !pf.Expired(time.Now()) {
				t.Errorf("публикация со сроком в прошлом не считается истёкшей: %+v", pf)
			}
		})

		// Без каскада ссылка пережила бы файл и отдавала 500 вместо 404.
		t.Run("удаление вложения снимает публикацию", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "e.png")
			token, err := db.PublishAttachment(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			if err := db.DeleteAttachment(ctx, id); err != nil {
				t.Fatalf("DeleteAttachment: %v", err)
			}
			pf, err := db.PublicFileByToken(ctx, token)
			if err != nil {
				t.Fatalf("PublicFileByToken: %v", err)
			}
			if pf != nil {
				t.Fatalf("публикация пережила удалённое вложение: %+v", pf)
			}
		})

		t.Run("неизвестный токен даёт nil без ошибки", func(t *testing.T) {
			pf, err := db.PublicFileByToken(ctx, "нет-такого-токена")
			if err != nil {
				t.Fatalf("PublicFileByToken: %v", err)
			}
			if pf != nil {
				t.Fatalf("неизвестный токен вернул %+v", pf)
			}
		})
	})
}
