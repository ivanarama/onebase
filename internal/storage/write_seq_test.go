package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Счётчик записей запроса сдвигают изменяющие операторы с его контекстом —
// и через DB, и в транзакции; чтение и записи с чужим контекстом — нет.
func TestWriteSeqCountsWritesNotReads(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "seq.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ent := &metadata.Entity{Name: "Товары", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "до"}, ent); err != nil {
		t.Fatal(err)
	}

	cctx, writes := WithWriteCounter(ctx)
	if _, err := db.GetByID(cctx, ent.Name, id, ent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryAll(cctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("чтение сдвинуло счётчик записей: %d", got)
	}
	// Запись с другим контекстом (фоновый исполнитель) счётчик запроса не трогает.
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "фон"}, ent); err != nil {
		t.Fatal(err)
	}
	if got := writes.Load(); got != 0 {
		t.Fatalf("чужая запись сдвинула счётчик запроса: %d", got)
	}
	if err := db.Upsert(cctx, ent.Name, id, map[string]any{"Наименование": "после"}, ent); err != nil {
		t.Fatal(err)
	}
	afterWrite := writes.Load()
	if afterWrite == 0 {
		t.Fatal("запись с контекстом запроса не сдвинула счётчик")
	}
	tx, txCtx, err := db.BeginTx(cctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(txCtx, `UPDATE "товары" SET "наименование" = 'в транзакции'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	if writes.Load() == afterWrite {
		t.Fatal("запись в транзакции запроса не сдвинула счётчик")
	}
}
