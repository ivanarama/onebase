package access_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQueryMaskLongField_Matrix(t *testing.T) {
	const field = "ПерсональныеДанныеСОченьДлиннымИменемРеквизита"
	entity := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: field, Type: metadata.FieldTypeString}}}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{field: "секрет1234"}, entity); err != nil {
			t.Fatal(err)
		}
		for _, selection := range []string{"*", "К.*", "К." + field, "К." + field + " КАК ОченьДлинныйПсевдонимПерсональныхДанныхКлиента"} {
			for _, policy := range []string{"hide", "mask_tail"} {
				t.Run(selection+"/"+policy, func(t *testing.T) {
					res, err := query.Compile("ВЫБРАТЬ "+selection+" ИЗ Справочник.Клиент КАК К", query.CompileOpts{Entities: []*metadata.Entity{entity}})
					if err != nil {
						t.Fatal(err)
					}
					user := maskUser(auth.FieldPolicies{field: {Read: policy, Keep: 4}})
					plan := access.QueryMaskPlanFor(user, res, func(_, _ string) *metadata.Entity { return entity })
					if plan.Denied != "" {
						t.Fatal(plan.Denied)
					}
					rows, _, err := query.Run(ctx, db, &res)
					if err != nil {
						t.Fatal(err)
					}
					tracked, err := plan.ApplyTracked(rows)
					if err != nil {
						t.Fatal(err)
					}
					key := strings.ToLower(field)
					if strings.Contains(selection, " КАК ") {
						key = strings.ToLower("ОченьДлинныйПсевдонимПерсональныхДанныхКлиента")
					}
					want := any(nil)
					if policy == "mask_tail" {
						want = "••••••1234"
					}
					if len(rows) != 1 || rows[0][key] != want {
						t.Fatalf("защищённое значение: %v, ожидалось %v", rows, want)
					}
					if len(tracked) != 1 {
						t.Fatal("не отмечена маска")
					}
					if _, ok := tracked[0][key]; !ok {
						t.Fatal("не отмечено защищённое поле", tracked)
					}
				})
			}
		}
	})
}
