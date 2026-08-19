package storage_test

// Матричные тесты по заявке #1001: гонка первой публикации, связка публикации с
// жизненным циклом блоба и честность аудита. Затрагивается SQL, поэтому одно
// тело гоняется на SQLite и (при TEST_DATABASE_URL) на PostgreSQL.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Две параллельные ПЕРВЫЕ публикации одного вложения обе видели «записи нет» и
// обе вставляли — второй INSERT падал на уникальном индексе, и ошибка уходила
// пользователю исключением из ОпубликоватьФайл.
func TestPublicFiles_ConcurrentFirstPublish_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		id := uploadTestAttachment(t, db, "гонка.png")

		const parallel = 8
		tokens := make([]string, parallel)
		errs := make([]error, parallel)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < parallel; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				tokens[i], errs[i] = db.PublishAttachment(ctx, id, storage.PublishOptions{})
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("публикация %d вернула ошибку: %v", i, err)
			}
		}
		for i, tok := range tokens {
			if tok != tokens[0] {
				t.Fatalf("публикация %d выдала токен %q, у первой %q — публикация перестала быть идемпотентной",
					i, tok, tokens[0])
			}
		}
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _public_files`).Scan(&n); err != nil {
			t.Fatalf("подсчёт публикаций: %v", err)
		}
		if n != 1 {
			t.Fatalf("в _public_files %d строк(и), ожидалась одна", n)
		}
	})
}

// Сборщик мусора считал живыми только ссылки из image-полей, поэтому
// опубликованная картинка, заменённая потом в карточке, собиралась — публичная
// ссылка отвечала 404 при живой строке публикации.
func TestPublicFiles_PublishedBlobSurvivesGC_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		// Блоб без ссылок из image-полей и заведомо старше grace-окна.
		putOldBlob := func() uuid.UUID {
			t.Helper()
			b, err := db.PutBlob(ctx, "image/png", strings.NewReader("PNG"), 1<<20,
				storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
			if err != nil {
				t.Fatalf("PutBlob: %v", err)
			}
			if _, err := db.Exec(ctx,
				fmt.Sprintf(`UPDATE _blobs SET created_at = 1000, dsl_managed = 0 WHERE id = '%s'`, b.ID)); err != nil {
				t.Fatalf("состаривание блоба: %v", err)
			}
			return b.ID
		}

		blobCount := func(id uuid.UUID) int {
			t.Helper()
			var n int
			if err := db.QueryRow(ctx,
				fmt.Sprintf(`SELECT COUNT(*) FROM _blobs WHERE id = '%s'`, id)).Scan(&n); err != nil {
				t.Fatalf("подсчёт блобов: %v", err)
			}
			return n
		}

		t.Run("действующая публикация держит блоб", func(t *testing.T) {
			id := putOldBlob()
			if _, err := db.PublishBlob(ctx, id, storage.PublishOptions{}); err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			if _, err := db.SweepOrphanBlobs(ctx, nil, time.Hour, false); err != nil {
				t.Fatalf("SweepOrphanBlobs: %v", err)
			}
			if pf, err := db.PublicFileByBlob(ctx, id); err != nil || pf == nil {
				t.Fatalf("публикация исчезла: pf=%v err=%v", pf, err)
			}
			if blobCount(id) != 1 {
				t.Fatal("опубликованный блоб собран сборщиком — публичная ссылка теперь отдаёт 404")
			}
		})

		t.Run("после отзыва блоб собирается", func(t *testing.T) {
			id := putOldBlob()
			if _, err := db.PublishBlob(ctx, id, storage.PublishOptions{}); err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			if err := db.UnpublishBlob(ctx, id); err != nil {
				t.Fatalf("UnpublishBlob: %v", err)
			}
			if _, err := db.SweepOrphanBlobs(ctx, nil, time.Hour, false); err != nil {
				t.Fatalf("SweepOrphanBlobs: %v", err)
			}
			if blobCount(id) != 0 {
				t.Fatal("отозванная публикация продолжает держать блоб")
			}
		})

		// Истёкшая публикация блоб не защищает (по такой ссылке /pub уже 404), но
		// и висеть строкой после сборки не должна.
		t.Run("истёкшая публикация не держит блоб и убирается вместе с ним", func(t *testing.T) {
			id := putOldBlob()
			past := time.Now().Add(-time.Hour)
			if _, err := db.PublishBlob(ctx, id, storage.PublishOptions{ExpiresAt: &past}); err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			if _, err := db.SweepOrphanBlobs(ctx, nil, time.Hour, false); err != nil {
				t.Fatalf("SweepOrphanBlobs: %v", err)
			}
			if blobCount(id) != 0 {
				t.Fatal("истёкшая публикация защитила блоб от сборки")
			}
			pf, err := db.PublicFileByBlob(ctx, id)
			if err != nil {
				t.Fatalf("PublicFileByBlob: %v", err)
			}
			if pf != nil {
				t.Fatal("блоб собран, а строка публикации осталась висеть")
			}
		})
	})
}

// Аудит помечал «attachment» ОБА источника, и при публикации картинки в
// record_id лежал ид блоба — по журналу такой файл не находился нигде. Плюс
// отзыв писался в журнал даже тогда, когда публикации не было.
func TestPublicFiles_Audit_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		auditKinds := func(action string, id uuid.UUID) []string {
			t.Helper()
			rows, err := db.Query(ctx, fmt.Sprintf(
				`SELECT entity_kind FROM _audit WHERE action = '%s' AND record_id = '%s'`, action, id))
			if err != nil {
				t.Fatalf("чтение журнала: %v", err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var kind string
				if err := rows.Scan(&kind); err != nil {
					t.Fatalf("scan журнала: %v", err)
				}
				out = append(out, kind)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("журнал: %v", err)
			}
			return out
		}

		t.Run("публикация картинки помечена блобом", func(t *testing.T) {
			b, err := db.PutBlob(ctx, "image/png", strings.NewReader("PNG"), 1<<20,
				storage.BlobOwner{Kind: "catalog", Entity: "Товары"})
			if err != nil {
				t.Fatalf("PutBlob: %v", err)
			}
			if _, err := db.PublishBlob(ctx, b.ID, storage.PublishOptions{}); err != nil {
				t.Fatalf("PublishBlob: %v", err)
			}
			kinds := auditKinds("publish", b.ID)
			if len(kinds) != 1 || kinds[0] != "blob" {
				t.Fatalf("вид записи аудита %v, ожидался [blob]: ид блоба, помеченный вложением, "+
					"при расследовании не находится среди _attachments", kinds)
			}
		})

		t.Run("публикация вложения помечена вложением", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "аудит.png")
			if _, err := db.PublishAttachment(ctx, id, storage.PublishOptions{}); err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			if kinds := auditKinds("publish", id); len(kinds) != 1 || kinds[0] != "attachment" {
				t.Fatalf("вид записи аудита %v, ожидался [attachment]", kinds)
			}
		})

		t.Run("отзыв несуществующей публикации в журнал не пишется", func(t *testing.T) {
			ghost := uuid.New()
			if err := db.UnpublishBlob(ctx, ghost); err != nil {
				t.Fatalf("UnpublishBlob: %v", err)
			}
			if kinds := auditKinds("unpublish", ghost); len(kinds) != 0 {
				t.Fatalf("в журнале %d запись(и) об отзыве публикации, которой не было", len(kinds))
			}
		})

		t.Run("настоящий отзыв в журнал пишется", func(t *testing.T) {
			id := uploadTestAttachment(t, db, "отзыв.png")
			if _, err := db.PublishAttachment(ctx, id, storage.PublishOptions{}); err != nil {
				t.Fatalf("PublishAttachment: %v", err)
			}
			if err := db.UnpublishAttachment(ctx, id); err != nil {
				t.Fatalf("UnpublishAttachment: %v", err)
			}
			if kinds := auditKinds("unpublish", id); len(kinds) != 1 {
				t.Fatalf("записей об отзыве %d, ожидалась одна", len(kinds))
			}
		})
	})
}
