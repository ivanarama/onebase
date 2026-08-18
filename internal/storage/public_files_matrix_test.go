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

		// Контракт «опубликовать» — ссылка работает: повторный вызов без нового
		// срока обязан оживить истёкшую публикацию, а не вернуть мёртвый URL.
		t.Run("повторная публикация оживляет истёкшую", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "g.png")
			past := time.Now().Add(-time.Hour)
			token, err := db.PublishAttachment(ctx, id, storage.PublishOptions{ExpiresAt: &past})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			again, err := db.PublishAttachment(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("повторная публикация: %v", err)
			}
			if again != token {
				t.Fatalf("оживление сменило токен: %q → %q", token, again)
			}
			pf, err := db.PublicFileByToken(ctx, again)
			if err != nil || pf == nil {
				t.Fatalf("PublicFileByToken: %v (pf=%v)", err, pf)
			}
			if pf.Expired(time.Now()) {
				t.Fatalf("после повторной публикации срок остался в прошлом: %+v", pf)
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

// Публикация картинок (поле image): источник — блоб, а не вложение.
func TestPublicFiles_Blobs_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		putBlob := func() uuid.UUID {
			t.Helper()
			b, err := db.PutBlob(ctx, "image/png", strings.NewReader("PNG"), 1<<20,
				storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
			if err != nil {
				t.Fatalf("PutBlob: %v", err)
			}
			return b.ID
		}

		t.Run("публикация картинки идемпотентна", func(t *testing.T) {
			id := putBlob()
			first, err := db.PublishBlob(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			second, err := db.PublishBlob(ctx, id, storage.PublishOptions{CacheSeconds: 120})
			if err != nil {
				t.Fatalf("повторная публикация: %v", err)
			}
			if first != second {
				t.Fatalf("повторная публикация дала другой токен: %q → %q", first, second)
			}
			pf, err := db.PublicFileByToken(ctx, first)
			if err != nil || pf == nil {
				t.Fatalf("PublicFileByToken: %v (pf=%v)", err, pf)
			}
			if !pf.IsBlob() || pf.BlobID != id {
				t.Errorf("источник определён неверно: %+v", pf)
			}
			if pf.CacheSeconds != 120 {
				t.Errorf("опции не обновились: %+v", pf)
			}
		})

		// Оба источника живут в одной таблице, и перепутать их нельзя: поиск по
		// вложению не должен находить публикацию картинки и наоборот.
		t.Run("источники не путаются", func(t *testing.T) {
			blobID := putBlob()
			attID := uploadTestAttachment(t, db, "doc.pdf")
			blobToken, err := db.PublishBlob(ctx, blobID, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			attToken, err := db.PublishAttachment(ctx, attID, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			if blobToken == attToken {
				t.Fatal("разные источники получили один токен")
			}
			if pf, _ := db.PublicFileByBlob(ctx, blobID); pf == nil || pf.Token != blobToken {
				t.Errorf("публикация картинки не находится по её ид")
			}
			if pf, _ := db.PublicFileByAttachment(ctx, blobID); pf != nil {
				t.Errorf("поиск вложения нашёл публикацию картинки: %+v", pf)
			}
			if pf, _ := db.PublicFileByBlob(ctx, attID); pf != nil {
				t.Errorf("поиск картинки нашёл публикацию вложения: %+v", pf)
			}
		})

		t.Run("отзыв ломает ссылку", func(t *testing.T) {
			id := putBlob()
			token, err := db.PublishBlob(ctx, id, storage.PublishOptions{})
			if err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			if err := db.UnpublishBlob(ctx, id); err != nil {
				t.Fatalf("UnpublishBlob: %v", err)
			}
			if pf, _ := db.PublicFileByToken(ctx, token); pf != nil {
				t.Fatalf("отозванная ссылка на картинку всё ещё работает: %+v", pf)
			}
		})
	})
}
