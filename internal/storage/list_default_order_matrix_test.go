package storage_test

// Порядок списка по умолчанию — «непустые по полю, пустые в конце, при
// равенстве по id» — на обоих диалектах, постранично. Раньше он задавался
// «ORDER BY CASE WHEN col IS NULL …», что запрещало индекс: справочник на 2 млн
// строк сортировался целиком. Теперь где диалект сам ставит NULL в конец —
// обычный ORDER BY, иначе непустые и пустые читаются раздельно. Тест сверяет,
// что порядок и страницы прежние.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func collectPages(t *testing.T, ctx context.Context, db *storage.DB, ent *metadata.Entity, field string, total int) string {
	t.Helper()
	var got []string
	for offset := 0; offset < total+3; offset += 3 {
		rows, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: 3, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			v := r[field]
			if v == nil {
				v = "∅"
			} else if tm, ok := v.(time.Time); ok {
				v = tm.Format("02")
			}
			got = append(got, fmt.Sprint(v)+"#"+fmt.Sprint(r["id"])[:1])
		}
	}
	return strings.Join(got, ",")
}

func TestListDefaultOrderNullsLastPaged(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := &metadata.Entity{Name: "ПорядокСписка", Kind: metadata.KindCatalog,
			Fields:  []metadata.Field{{ID: "n", Name: "Наименование", Type: metadata.FieldTypeString}},
			Indexes: []metadata.IndexSpec{{Fields: []string{"Наименование"}}}}
		doc := &metadata.Entity{Name: "ПорядокДокументов", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{ID: "d", Name: "Дата", Type: metadata.FieldTypeDate}}}
		if err := db.Migrate(ctx, []*metadata.Entity{cat, doc}); err != nil {
			t.Fatal(err)
		}
		// Первая цифра UUID задаёт порядок по id при равных значениях.
		id := func(d int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("%d0000000-0000-0000-0000-000000000000", d)) }
		names := []any{"Б", "А", nil, "В", nil, "А", "Г"}
		for i, n := range names {
			vals := map[string]any{}
			if n != nil {
				vals["Наименование"] = n
			}
			if err := db.Upsert(ctx, cat.Name, id(i+1), vals, cat); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := collectPages(t, ctx, db, cat, "Наименование", len(names)), "А#2,А#6,Б#1,В#4,Г#7,∅#3,∅#5"; got != want {
			t.Fatalf("справочник: %s, ожидалось %s", got, want)
		}
		base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
		days := []any{3, nil, 5, 3, nil, 1}
		for i, d := range days {
			vals := map[string]any{}
			if d != nil {
				vals["Дата"] = base.AddDate(0, 0, d.(int)-1)
			}
			if err := db.Upsert(ctx, doc.Name, id(i+1), vals, doc); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := collectPages(t, ctx, db, doc, "Дата", len(days)), "05#3,03#1,03#4,01#6,∅#2,∅#5"; got != want {
			t.Fatalf("документы: %s, ожидалось %s", got, want)
		}
		// A borrowed transaction may use READ COMMITTED on PostgreSQL.
		// Both the paged and unlimited List paths must retain the same order
		// and include this transaction's writes without changing its outcome.
		if err := db.WithTx(ctx, func(txCtx context.Context) error {
			if got, want := collectPages(t, txCtx, db, cat, "Наименование", len(names)), "А#2,А#6,Б#1,В#4,Г#7,∅#3,∅#5"; got != want {
				t.Fatalf("справочник в транзакции: %s, ожидалось %s", got, want)
			}
			if got, want := collectPages(t, txCtx, db, doc, "Дата", len(days)), "05#3,03#1,03#4,01#6,∅#2,∅#5"; got != want {
				t.Fatalf("документы в транзакции: %s, ожидалось %s", got, want)
			}
			for _, ent := range []*metadata.Entity{cat, doc} {
				rows, err := db.List(txCtx, ent.Name, ent, storage.ListParams{})
				if err != nil {
					return err
				}
				paged, err := db.List(txCtx, ent.Name, ent, storage.ListParams{Limit: 100})
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(rows, paged) {
					t.Fatalf("%s: unlimited and paged List disagree", ent.Name)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}
