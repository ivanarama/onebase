package storage_test

// Свёртка и дата запрета смотрят на дату документа, а не на первый
// реквизит-дату.
//
// Удаление документов при свёртке и проверка даты запрета брали первый
// объявленный реквизит-дату. У документа предоплаты срок оплаты раньше даты
// документа, и если СрокОплаты объявлен первым, свёртка удаляла документ,
// датированный ПОСЛЕ даты свёртки, — вместе с его движениями и ссылками. Матрица,
// потому что отбор по дате — SQL, а даты SQLite хранит TEXT, PostgreSQL —
// TIMESTAMPTZ.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func prepaymentDoc() *metadata.Entity {
	return &metadata.Entity{
		Name:    "Предоплата" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
		Kind:    metadata.KindDocument,
		Posting: true,
		// Порядок намеренный: срок оплаты объявлен раньше даты документа.
		Fields: []metadata.Field{
			{Name: "СрокОплаты", Type: metadata.FieldTypeDate},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func writePrepayment(t *testing.T, ctx context.Context, db *storage.DB, doc *metadata.Entity, date, due time.Time) uuid.UUID {
	t.Helper()
	idStr, err := db.WriteCatalogRecord(ctx, doc, "", map[string]any{"Дата": date, "СрокОплаты": due, "Сумма": 100})
	if err != nil {
		t.Fatalf("запись документа: %v", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetPosted(ctx, doc.Name, id, true); err != nil {
		t.Fatalf("SetPosted: %v", err)
	}
	return id
}

func TestRollup_DeletesByDocumentDateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := prepaymentDoc()
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		cutoff := day(2025, 3, 1)
		// Датирован после свёртки, срок оплаты — до неё: обязан остаться.
		kept := writePrepayment(t, ctx, db, doc, day(2025, 6, 20), day(2025, 1, 10))
		// Датирован до свёртки, срок оплаты — после: подлежит удалению.
		gone := writePrepayment(t, ctx, db, doc, day(2025, 1, 15), day(2025, 7, 1))

		rep, err := db.Rollup(ctx, nil, []*metadata.Entity{doc}, nil, nil, storage.RollupOptions{
			Date: cutoff, DeleteDocuments: true,
		})
		if err != nil {
			t.Fatalf("Rollup: %v", err)
		}
		if rep.DeletedDocs != 1 {
			t.Errorf("удалено документов %d, ожидался 1 — датированный до свёртки", rep.DeletedDocs)
		}
		for id, want := range map[uuid.UUID]bool{kept: true, gone: false} {
			var n int
			if err := db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE id = %s",
				metadata.TableName(doc.Name), db.Dialect().Placeholder(1)), id.String()).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if (n == 1) != want {
				t.Errorf("документ %s: остался=%v, ожидалось %v", id, n == 1, want)
			}
		}
	})
}

func TestPostingLockViolation_ByDocumentDateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := prepaymentDoc()
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.EnsureRollupTable(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SavePostingLockDate(ctx, day(2025, 3, 1)); err != nil {
			t.Fatal(err)
		}
		closed := writePrepayment(t, ctx, db, doc, day(2025, 1, 15), day(2025, 7, 1))
		open := writePrepayment(t, ctx, db, doc, day(2025, 6, 20), day(2025, 1, 10))
		if v, _, err := db.PostingLockViolation(ctx, doc, closed); err != nil || !v {
			t.Errorf("документ от 15.01.2025 со сроком 01.07.2025: запрет=%v (err %v), ожидался запрет", v, err)
		}
		if v, _, err := db.PostingLockViolation(ctx, doc, open); err != nil || v {
			t.Errorf("документ от 20.06.2025 со сроком 10.01.2025: запрет=%v (err %v), запрета быть не должно", v, err)
		}
	})
}
