package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Регрессия к issue #625: предикат строковой политики обязан вставляться в
// скобках и в ВИРТУАЛЬНЫХ ТАБЛИЦАХ регистров, а не только в верхнем ГДЕ.
//
// #574 закрыл приоритет ИЛИ для собственного ГДЕ пользователя
// (emitPendingRowFiltersAfterWhere ставит скобку), но rowFilterCondition —
// путь виртуальных таблиц и авто-JOIN — отдавала предикат голым. При политике
// вида `any: [Склад=A, Склад=B]` предикат это «склад=? OR склад=?», и склейка
// через AND давала
//
//	WHERE period <= ? AND склад='A' OR склад='B'
//
// то есть «(period<=? AND склад='A') OR склад='B'»: для склада B граница
// периода терялась, и в «остатки на дату» попадали более поздние движения.

func vtPolicyRegisters() []*metadata.Register {
	return []*metadata.Register{{
		Name: "ОстаткиПолитика",
		Dimensions: []metadata.Field{
			{Name: "Склад", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}}
}

// Политика «видно склады A и B» — именно многовариантная (any), потому что
// одиночное условие даёт предикат без OR и дефект не проявляется.
func vtPolicyFilters() map[query.SourceRef]*storage.Predicate {
	return map[query.SourceRef]*storage.Predicate{
		{Kind: "register", Name: "ОстаткиПолитика"}: {
			Any: []storage.Predicate{
				{Field: "Склад", Op: "eq", Value: "A"},
				{Field: "Склад", Op: "eq", Value: "B"},
			},
		},
	}
}

func TestRowFilterAnyKeepsMomentBoundInVirtualTable(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		regs := vtPolicyRegisters()
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatalf("миграция регистров: %v", err)
		}

		apr := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
		jun := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

		// До момента среза: на обоих складах по 5.
		if err := db.WriteMovements(ctx, regs[0].Name, "Пост", uuid.New(),
			[]map[string]any{
				{"ВидДвижения": "Приход", "Склад": "A", "Количество": float64(5)},
				{"ВидДвижения": "Приход", "Склад": "B", "Количество": float64(5)},
			}, regs[0], &apr); err != nil {
			t.Fatalf("движения апреля: %v", err)
		}
		// ПОСЛЕ момента среза: на склад B ещё 100. В остатки на конец апреля
		// это попасть не должно — именно эту границу теряет дефект #625,
		// причём только для второй ветви OR, то есть для склада B.
		if err := db.WriteMovements(ctx, regs[0].Name, "Пост", uuid.New(),
			[]map[string]any{
				{"ВидДвижения": "Приход", "Склад": "B", "Количество": float64(100)},
			}, regs[0], &jun); err != nil {
			t.Fatalf("движения июня: %v", err)
		}

		r, err := query.Compile(
			"ВЫБРАТЬ Склад, КоличествоОстаток ИЗ РегистрНакопления.ОстаткиПолитика.Остатки(&Момент)",
			query.CompileOpts{
				Registers:  regs,
				Dialect:    db.Dialect(),
				RowFilters: vtPolicyFilters(),
				Params:     map[string]any{"Момент": time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC)},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		got := execToMap(t, ctx, db, r)

		if got["A"] != 5 {
			t.Errorf("остаток склада A на конец апреля = %v, ожидалось 5", got["A"])
		}
		if got["B"] != 5 {
			t.Errorf("остаток склада B на конец апреля = %v, ожидалось 5; "+
				"105 означает, что граница периода потеряна для второй ветви ИЛИ (#625)\nSQL: %s",
				got["B"], r.SQL)
		}
	})
}
