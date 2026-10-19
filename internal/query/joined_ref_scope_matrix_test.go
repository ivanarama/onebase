package query_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Все три блокера седьмого ревью #1459 проходят публичные Compile → Run.
func TestJoinedRefSelectScopeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := []*metadata.Entity{
			{Name: "Базы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}, {Name: "Наименование", Type: metadata.FieldTypeString}}},
			{Name: "Планы", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}, {Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "ГлавныйУзел", Type: "reference:Базы", RefEntity: "Базы"}}},
		}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		bid, pid := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, "Базы", bid, map[string]any{"Код": "GL", "Наименование": "Главная"}, ents[0]); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, "Планы", pid, map[string]any{"Код": "GL", "Наименование": "План-1", "ГлавныйУзел": bid}, ents[1]); err != nil {
			t.Fatal(err)
		}
		const direct = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`
		const derived = ` ИЗ (ВЫБРАТЬ Код, Наименование ИЗ Справочник.Базы) КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`
		for _, v := range []struct{ name, text, want string }{
			{"control-normal-joined", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + direct, "Главная"},
			{"following-derived-on", `ВЫБРАТЬ Под.Наименование КАК Значение` + direct + ` ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Код, Наименование ИЗ Справочник.Базы) КАК Под ПО Под.Наименование = П.ГлавныйУзел.Наименование`, "Главная"},
			{"control-derived-first-plain", `ВЫБРАТЬ П.Наименование КАК Значение` + derived, "План-1"},
			{"derived-first-reference", `ВЫБРАТЬ П.ГлавныйУзел КАК Значение` + derived, bid.String()},
			{"derived-first-explicit-id", `ВЫБРАТЬ П.ГлавныйУзел.Ссылка КАК Значение` + derived, bid.String()},
			{"derived-first-without-inner-from", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение ИЗ (ВЫБРАТЬ "GL" КАК Код) КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`, "Главная"},
			{"derived-first-navigation", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + derived, "Главная"},
			{"control-correlated-main", `ВЫБРАТЬ П.Наименование КАК Значение ИЗ Справочник.Планы КАК П ГДЕ П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Наименование = П.ГлавныйУзел.Наименование)`, "План-1"},
			{"correlated-joined", `ВЫБРАТЬ П.Наименование КАК Значение` + direct + ` ГДЕ П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Наименование = П.ГлавныйУзел.Наименование)`, "План-1"},
		} {
			t.Run(v.name, func(t *testing.T) {
				r, err := query.Compile(v.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal("Compile:", err)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nQUERY: %s\nSQL: %s", err, v.text, r.SQL)
				}
				if len(rows) != 1 || fmt.Sprint(rows[0]["значение"]) != v.want {
					t.Fatalf("unexpected rows: %v\nSQL: %s", rows, r.SQL)
				}
			})
		}
	})
}

func TestJoinedRefScopeShadowingMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := append(joinedRefEntities(),
			&metadata.Entity{Name: "ДругиеБазы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Код", Type: metadata.FieldTypeString}, {Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Метка", Type: metadata.FieldTypeString},
			}},
			&metadata.Entity{Name: "ДругиеПланы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Код", Type: metadata.FieldTypeString}, {Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "ГлавныйУзел", Type: "reference:ДругиеБазы", RefEntity: "ДругиеБазы"},
			}},
		)
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		seedJoinedRef(t, ctx, db, ents)
		otherID := uuid.New()
		if err := db.Upsert(ctx, "ДругиеБазы", otherID, map[string]any{"Код": "GL", "Наименование": "Другая", "Метка": "GL"}, ents[2]); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, "ДругиеПланы", uuid.New(), map[string]any{"Код": "GL", "Наименование": "Другой план", "ГлавныйУзел": otherID}, ents[3]); err != nil {
			t.Fatal(err)
		}
		const outer = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`
		const inner = ` ИЗ Справочник.Базы КАК Б2 ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДругиеПланы КАК П ПО П.Код = Б2.Код`
		const derivedInner = ` ИЗ (ВЫБРАТЬ Код ИЗ Справочник.Базы) КАК Б2 ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДругиеПланы КАК П ПО П.Код = Б2.Код`
		opts := query.CompileOpts{Entities: ents, Dialect: db.Dialect()}
		for _, c := range []struct {
			name, text string
			want       []string
		}{
			{"correlation-through-two-selects", `ВЫБРАТЬ П.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Код В (ВЫБРАТЬ Б3.Код ИЗ Справочник.Базы КАК Б3 ГДЕ Б3.Наименование = П.ГлавныйУзел.Наименование))`, []string{"План-1"}},
			{"local-main-shadows-outer", `ВЫБРАТЬ П.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ Справочник.ДругиеБазы КАК П ГДЕ П.Наименование = "Другая")`, []string{"План-1"}},
			{"local-derived-shadows-outer", `ВЫБРАТЬ П.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ (ВЫБРАТЬ Код, Наименование ИЗ Справочник.ДругиеБазы) КАК П ГДЕ П.Наименование = "Другая")`, []string{"План-1"}},
			{"same-alias-different-reference-target", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.ГлавныйУзел.Метка` + inner + `)`, []string{"Главная"}},
			{"derived-first-in-shadowing-scope", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.ГлавныйУзел.Метка` + derivedInner + `)`, []string{"Главная"}},
			{"union-sibling-reference-targets", `ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + outer + ` ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ П.ГлавныйУзел.Наименование КАК Значение` + inner, []string{"Главная", "Другая"}},
			{"sibling-nested-selects", `ВЫБРАТЬ П.Наименование КАК Значение` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.ГлавныйУзел.Метка` + inner + `) И П.Код В (ВЫБРАТЬ Б3.Код ИЗ Справочник.Базы КАК Б3 ГДЕ Б3.Наименование = П.ГлавныйУзел.Наименование)`, []string{"План-1"}},
			{"derived-on-with-correlated-reference", `ВЫБРАТЬ Под.Наименование КАК Значение` + outer + ` ЛЕВОЕ СОЕДИНЕНИЕ (ВЫБРАТЬ Код, Наименование ИЗ Справочник.Базы) КАК Под ПО Под.Код В (ВЫБРАТЬ Б3.Код ИЗ Справочник.Базы КАК Б3 ГДЕ Б3.Наименование = П.ГлавныйУзел.Наименование)`, []string{"Главная"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, err := query.Compile(c.text, opts)
				if err != nil {
					t.Fatal(err)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row["значение"]))
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Fatalf("got %v, want %v\nSQL: %s", got, c.want, r.SQL)
				}
			})
		}

		// Цель навигации отсутствует среди явных FROM: авто-JOIN владельца
		// обязан зарегистрировать её для RBAC и применить её строковую политику.
		t.Run("correlated-target-permissions", func(t *testing.T) {
			const text = `ВЫБРАТЬ П.Наименование КАК Значение ИЗ Справочник.ДругиеБазы КАК Ист ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Ист.Код ГДЕ П.Код В (ВЫБРАТЬ Ист2.Код ИЗ Справочник.ДругиеБазы КАК Ист2 ГДЕ Ист2.Код = П.ГлавныйУзел.Код)`
			for _, allowed := range []bool{true, false} {
				filtered := opts
				name := "Главная"
				if !allowed {
					name = "Недоступная"
				}
				filtered.RowFilters = map[query.SourceRef]*storage.Predicate{
					{Kind: "catalog", Name: "Базы"}: {Field: "Наименование", Op: "eq", Value: name},
				}
				r, err := query.Compile(text, filtered)
				if err != nil {
					t.Fatal(err)
				}
				target := query.SourceRef{Kind: "catalog", Name: "Базы"}
				found := false
				for _, src := range r.Sources {
					if src == target {
						found = true
					}
				}
				if !found {
					t.Fatalf("target missing from RBAC sources: %v", r.Sources)
				}
				rows, _, err := query.Run(ctx, db, &r)
				if err != nil {
					t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
				}
				if allowed && (len(rows) != 1 || rows[0]["значение"] != "План-1") || !allowed && len(rows) != 0 {
					t.Fatalf("allowed=%t rows=%v", allowed, rows)
				}
			}
		})
		for _, c := range []struct{ name, text, wantErr string }{
			{"missing-field-in-local-main", `ВЫБРАТЬ П.Наименование` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ Справочник.Базы КАК П ГДЕ П.ГлавныйУзел.Наименование = "Главная")`, "нет ссылочного реквизита"},
			{"missing-field-in-local-join", `ВЫБРАТЬ П.Наименование` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ Справочник.Базы КАК Б2 ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Базы КАК П ПО П.Код = Б2.Код ГДЕ П.ГлавныйУзел.Наименование = "Главная")`, "нет ссылочного реквизита"},
			{"missing-field-in-local-derived", `ВЫБРАТЬ П.Наименование` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ (ВЫБРАТЬ Код ИЗ Справочник.Базы) КАК П ГДЕ П.ГлавныйУзел.Наименование = "Главная")`, "нет ссылочного реквизита"},
			{"local-own-on-still-rejected", `ВЫБРАТЬ П.Наименование` + outer + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ Справочник.Базы КАК Б2 ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.ГлавныйУзел.Наименование = Б2.Наименование)`, "внутри ПО не поддерживается"},
			{"correlated-reference-in-own-on", `ВЫБРАТЬ П.Наименование ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Наименование = П.ГлавныйУзел.Наименование)`, "внутри ПО не поддерживается"},
		} {
			t.Run(c.name, func(t *testing.T) {
				r, err := query.Compile(c.text, opts)
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want %q, got %v\nSQL: %s", c.wantErr, err, r.SQL)
				}
			})
		}
	})
}
