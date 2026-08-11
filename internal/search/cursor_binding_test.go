package search

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Курсор — состояние листания ОДНОГО запроса.
//
// Шифр защищал только от подделки числа: до привязки любой курсор принимался
// как позиция чтения по любому запросу. Тем самым возвращалось произвольное
// смещение — ровно то, ради запрета чего курсор и заведён: разница между
// «просмотрено» и «показано» выдаёт наличие совпадений, скрытых маской (план
// 88) или строковой политикой (план 79), и по ней скрытое значение
// восстанавливается побайтово (#615).
//
// Непригодный курсор — не ошибка: листание начинается сначала, как при первом
// запросе. Это и проверяем — выдача обязана совпасть с выдачей БЕЗ курсора.

func newTwoWordFixture(t *testing.T) (*storage.DB, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, e := newSearchFixture(t, 6) // шесть «ООО Ромашка»
	for i := 0; i < 6; i++ {
		if err := db.Upsert(ctx, e.Name, seqUUID(100+i), map[string]any{
			"Наименование": "ЗАО Василёк", "Менеджер": "petrov",
		}, e); err != nil {
			t.Fatal(err)
		}
	}
	return db, e
}

func ids(page Page) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, it.ID)
	}
	return out
}

func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCursor_ПеренесённыйНаДругойЗапросНеПринимается(t *testing.T) {
	ctx := context.Background()
	db, e := newTwoWordFixture(t)
	deps := fakeDeps{entities: []*metadata.Entity{e}}

	wide, err := Run(ctx, db, deps, "ромашка", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if wide.Cursor == "" {
		t.Fatal("курсор не выдан, проба некорректна")
	}

	// Тот же курсор подставляем к ДРУГОМУ запросу.
	moved, err := Run(ctx, db, deps, "василёк", 2, wide.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Run(ctx, db, deps, "василёк", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(ids(moved), ids(fresh)) {
		t.Errorf("курсор чужого запроса сдвинул чтение: с курсором %v, без него %v",
			ids(moved), ids(fresh))
	}
}

// Размер страницы входит в привязку: он задаёт бюджет просмотра индекса, и
// позиция, полученная при одном limit, для другого — то же произвольное
// смещение.
func TestCursor_ПеренесённыйНаДругойРазмерСтраницыНеПринимается(t *testing.T) {
	ctx := context.Background()
	db, e := newTwoWordFixture(t)
	deps := fakeDeps{entities: []*metadata.Entity{e}}

	first, err := Run(ctx, db, deps, "ромашка", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor == "" {
		t.Fatal("курсор не выдан, проба некорректна")
	}

	moved, err := Run(ctx, db, deps, "ромашка", 4, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Run(ctx, db, deps, "ромашка", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameIDs(ids(moved), ids(fresh)) {
		t.Errorf("курсор другого размера страницы сдвинул чтение: с курсором %v, без него %v",
			ids(moved), ids(fresh))
	}
}

// Свой курсор при этом обязан работать — иначе листание выродилось бы в одну
// страницу, и «непригодный курсор начинает сначала» стало бы описанием
// сломанной пагинации, а не защиты.
func TestCursor_СвойКурсорПродолжаетЛистание(t *testing.T) {
	ctx := context.Background()
	db, e := newTwoWordFixture(t)
	deps := fakeDeps{entities: []*metadata.Entity{e}}

	first, err := Run(ctx, db, deps, "ромашка", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(ctx, db, deps, "ромашка", 2, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) == 0 {
		t.Fatal("вторая страница пуста: свой курсор не принят")
	}
	if sameIDs(ids(first), ids(second)) {
		t.Errorf("вторая страница повторила первую: %v", ids(second))
	}
}
