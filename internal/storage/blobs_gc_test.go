package storage

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid.Parse(%q): %v", s, err)
	}
	return id
}

// openClosed открывает блоб и сразу закрывает его содержимое, возвращая только
// ошибку. Тесты проверяют лишь факт наличия/отсутствия блоба, но ОБЯЗАНЫ закрыть
// rc (контракт OpenBlob): иначе в дисковом режиме на Windows открытый *os.File
// не даёт удалить каталог при очистке t.TempDir.
func openClosed(t *testing.T, db *DB, id uuid.UUID) error {
	t.Helper()
	_, rc, err := db.OpenBlob(context.Background(), id)
	if rc != nil {
		_ = rc.Close()
	}
	return err
}

// putRef создаёт блоб и возвращает его UUID-строку (ссылку поля image).
func putRef(t *testing.T, db *DB, owner BlobOwner) string {
	t.Helper()
	b, err := db.PutBlob(context.Background(), "image/png",
		bytes.NewReader([]byte("\x89PNG bytes")), 1<<20, owner)
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	return b.ID.String()
}

// TestSweepOrphanBlobs проверяет mark-and-sweep: удаляются только блобы, на
// которые НЕ ссылается ни одно image-поле; общий блоб (на него ссылаются две
// строки) и свежие блобы (в пределах grace-окна) сохраняются.
func TestSweepOrphanBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	// Сущность Img с одним image-полем Pic → таблица img, колонка pic.
	entity := &metadata.Entity{
		Name:   "Img",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Pic", Type: metadata.FieldTypeImage}},
	}
	if _, err := db.Exec(ctx, "CREATE TABLE img (pic TEXT)"); err != nil {
		t.Fatalf("create img: %v", err)
	}

	owner := BlobOwner{Kind: "catalog", Entity: "Img"}
	referenced := putRef(t, db, owner) // на него ссылается одна строка
	shared := putRef(t, db, owner)     // на него ссылаются ДВЕ строки
	orphan := putRef(t, db, owner)     // ни одной ссылки → удалить
	for _, ref := range []string{referenced, shared, shared} {
		if _, err := db.Exec(ctx, "INSERT INTO img (pic) VALUES (?)", ref); err != nil {
			t.Fatalf("insert ref: %v", err)
		}
	}

	entities := []*metadata.Entity{entity}

	// Сбор живых ссылок: referenced + shared (без дубля), orphan отсутствует.
	live, err := db.CollectImageRefs(ctx, entities)
	if err != nil {
		t.Fatalf("CollectImageRefs: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("живых ссылок = %d, ожидалось 2", len(live))
	}

	// grace=1h: все блобы свежие → ничего не удаляем, orphan защищён.
	st, err := db.SweepOrphanBlobs(ctx, entities, time.Hour, false)
	if err != nil {
		t.Fatalf("SweepOrphanBlobs(grace): %v", err)
	}
	if st.Deleted != 0 || st.Protected != 1 {
		t.Fatalf("grace-окно: deleted=%d protected=%d (ожидалось 0/1)", st.Deleted, st.Protected)
	}
	if st.ProtectedRecent != 1 || st.ProtectedDSL != 0 {
		t.Fatalf("причина защиты: recent=%d dsl=%d (ожидалось 1/0)", st.ProtectedRecent, st.ProtectedDSL)
	}
	if err := openClosed(t, db, mustUUID(t, orphan)); err != nil {
		t.Fatalf("orphan в пределах grace не должен удаляться: %v", err)
	}

	// grace=0: orphan удаляется, referenced и shared живут.
	st, err = db.SweepOrphanBlobs(ctx, entities, 0, false)
	if err != nil {
		t.Fatalf("SweepOrphanBlobs: %v", err)
	}
	if st.Deleted != 1 {
		t.Fatalf("удалено %d, ожидался 1 (orphan)", st.Deleted)
	}
	if err := openClosed(t, db, mustUUID(t, orphan)); err == nil {
		t.Fatal("orphan должен быть удалён")
	}
	if err := openClosed(t, db, mustUUID(t, referenced)); err != nil {
		t.Fatalf("referenced не должен удаляться: %v", err)
	}
	if err := openClosed(t, db, mustUUID(t, shared)); err != nil {
		t.Fatalf("shared (две ссылки) не должен удаляться: %v", err)
	}
}

// TestSweepOrphanBlobs_DSLManaged: блоб, созданный через DSL (СохранитьКартинку,
// owner-less, помечен DSLManaged), НЕ попадает в кандидаты на удаление — даже без
// ссылки из image-поля и вне grace-окна. Прикладной код мог сохранить его UUID в
// строковое поле/константу/реквизит инфорегистра, которые GC не сканирует (ревью
// #11).
func TestSweepOrphanBlobs_DSLManaged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	managed := putRef(t, db, BlobOwner{DSLManaged: true}) // создан из DSL
	orphan := putRef(t, db, BlobOwner{})                  // owner-less, НЕ managed

	// grace=0: orphan удаляется, managed-блоб защищён несмотря на отсутствие ссылок.
	st, err := db.SweepOrphanBlobs(ctx, nil, 0, false)
	if err != nil {
		t.Fatalf("SweepOrphanBlobs: %v", err)
	}
	if st.Deleted != 1 {
		t.Fatalf("удалено %d, ожидался 1 (только не-managed orphan)", st.Deleted)
	}
	if err := openClosed(t, db, mustUUID(t, managed)); err != nil {
		t.Fatalf("DSL-managed блоб не должен удаляться: %v", err)
	}
	if err := openClosed(t, db, mustUUID(t, orphan)); err == nil {
		t.Fatal("не-managed orphan должен быть удалён")
	}
	// Причина защиты различима: постоянная (dsl-managed), а не «моложе grace».
	// Одним счётчиком отчёт команды уверял, что блоб моложе grace-окна, даже
	// когда grace=0 и блоб создан давно.
	if st.ProtectedDSL != 1 || st.ProtectedRecent != 0 {
		t.Fatalf("ProtectedDSL=%d ProtectedRecent=%d, ожидалось 1/0", st.ProtectedDSL, st.ProtectedRecent)
	}
	if st.Protected != st.ProtectedDSL+st.ProtectedRecent {
		t.Fatalf("Protected=%d не равно сумме причин (%d+%d)", st.Protected, st.ProtectedDSL, st.ProtectedRecent)
	}
}

// TestSweepOrphanBlobs_LegacyZeroCreatedAt: легаси-блоб с created_at=0 (неизвестное
// время создания, до появления колонки) трактуется как ЗАЩИЩЁННЫЙ, а не «старый» —
// иначе 0 > cutoff ложно при положительном grace и блоб бы удалялся. Минимальная
// защита от TOCTOU/легаси (ревью #18).
func TestSweepOrphanBlobs_LegacyZeroCreatedAt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	legacy := putRef(t, db, BlobOwner{})
	// Эмулируем легаси-блоб без известного времени создания.
	if _, err := db.Exec(ctx, "UPDATE _blobs SET created_at=0 WHERE id=?", legacy); err != nil {
		t.Fatalf("set created_at=0: %v", err)
	}

	// grace=0 (cutoff = now): обычный orphan удалился бы, но created_at=0 защищён.
	st, err := db.SweepOrphanBlobs(ctx, nil, 0, false)
	if err != nil {
		t.Fatalf("SweepOrphanBlobs: %v", err)
	}
	if st.Deleted != 0 {
		t.Fatalf("удалено %d, ожидалось 0 (created_at=0 защищён)", st.Deleted)
	}
	if st.Protected != 1 {
		t.Fatalf("protected=%d, ожидалось 1", st.Protected)
	}
	if err := openClosed(t, db, mustUUID(t, legacy)); err != nil {
		t.Fatalf("легаси-блоб (created_at=0) не должен удаляться: %v", err)
	}
}

// TestSweepOrphanBlobs_DryRun: dry-run сообщает об orphan, но не удаляет.
func TestSweepOrphanBlobs_DryRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}
	orphan := putRef(t, db, BlobOwner{})

	st, err := db.SweepOrphanBlobs(ctx, nil, 0, true)
	if err != nil {
		t.Fatalf("SweepOrphanBlobs(dry): %v", err)
	}
	if len(st.Orphans) != 1 || st.Deleted != 0 {
		t.Fatalf("dry-run: orphans=%d deleted=%d (ожидалось 1/0)", len(st.Orphans), st.Deleted)
	}
	if err := openClosed(t, db, mustUUID(t, orphan)); err != nil {
		t.Fatalf("dry-run не должен удалять: %v", err)
	}
}
