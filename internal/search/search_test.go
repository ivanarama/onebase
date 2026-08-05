package search

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Слой прав над полнотекстовым индексом (план 82). Здесь проверяется сама
// механика добора и пагинации; интеграция с RBAC/RLS — в тестах ui и api.

type fakeDeps struct {
	entities []*metadata.Entity
	// allowRow решает судьбу строки: так тест изображает строковую политику.
	allowRow func(row map[string]any) bool
	masked   []string
}

func (d fakeDeps) Entities() []*metadata.Entity { return d.entities }

func (d fakeDeps) CanRead(context.Context, *metadata.Entity) bool { return true }

func (d fakeDeps) RowAllowed(_ context.Context, _ *metadata.Entity, row map[string]any) bool {
	if d.allowRow == nil {
		return true
	}
	return d.allowRow(row)
}

func (d fakeDeps) MaskedIndexedFields(context.Context, *metadata.Entity) []string { return d.masked }

func (d fakeDeps) MaskedLabel(_ context.Context, e *metadata.Entity, row map[string]any) string {
	return metadata.RowLabel(row, e)
}

func newSearchFixture(t *testing.T, count int) (*storage.DB, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	e := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Менеджер", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		manager := "petrov"
		if i == count-1 {
			manager = "ivanov" // единственная разрешённая строка — последняя
		}
		// Идентификаторы возрастают: выдача упорядочена по owner_id при равной
		// релевантности, поэтому разрешённая строка гарантированно окажется в
		// конце — иначе тест добора проходил бы случайно.
		if err := db.Upsert(ctx, e.Name, seqUUID(i), map[string]any{
			"Наименование": "ООО Ромашка", "Менеджер": manager,
		}, e); err != nil {
			t.Fatal(err)
		}
	}
	return db, e
}

func seqUUID(i int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
}

// Строки, отсеянные правами, не должны укорачивать страницу: поиск обязан
// добрать следующую пачку индекса, а не вернуть неполную выдачу.
func TestRun_RefillsPageAfterFilteredRows(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 10)

	deps := fakeDeps{
		entities: []*metadata.Entity{e},
		allowRow: func(row map[string]any) bool { return row["Менеджер"] == "ivanov" },
	}
	page, err := Run(ctx, db, deps, "ромашка", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("страница должна была набраться добором: %+v", page)
	}
	if page.NextOffset <= 1 {
		t.Fatalf("NextOffset должен считаться по просмотренным строкам индекса: %+v", page)
	}
}

// Пустая выдача по значению, скрытому правами, обязана быть НЕОТЛИЧИМА от выдачи
// по заведомо отсутствующему значению — при любом числе скрытых совпадений.
// Иначе has_more работает оракулом: подтверждает существование строки, которую
// видеть нельзя (issue #578). Раньше добор, упёршийся в лимит пачек на сплошь
// скрытой выдаче, ставил has_more=true — этот тест на старом коде падал.
func TestRun_HiddenMatchesIndistinguishableFromAbsent(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 300) // 300 строк «ООО Ромашка», все скрыты политикой

	hiddenAll := fakeDeps{
		entities: []*metadata.Entity{e},
		allowRow: func(map[string]any) bool { return false }, // ничего не видно
	}
	hidden, err := Run(ctx, db, hiddenAll, "ромашка", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := Run(ctx, db, hiddenAll, "нетакогослова", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Items) != 0 || len(absent.Items) != 0 {
		t.Fatalf("обе выдачи должны быть пустыми: hidden=%+v absent=%+v", hidden, absent)
	}
	if hidden.HasMore || hidden.HasMore != absent.HasMore {
		t.Fatalf("has_more по скрытому значению отличается от отсутствующего (оракул): hidden=%v absent=%v", hidden.HasMore, absent.HasMore)
	}
	if (hidden.Cursor == "") != (absent.Cursor == "") {
		t.Fatalf("курсор выдаёт наличие скрытых совпадений: hidden=%q absent=%q", hidden.Cursor, absent.Cursor)
	}
	// Бюджет просмотра не даёт вычитывать весь индекс на каждый запрос.
	if hidden.NextOffset > 10*scanBudgetFactor {
		t.Fatalf("бюджет просмотра превышен (вычитан весь индекс?): NextOffset=%d", hidden.NextOffset)
	}
}

// Полная видимая страница сообщает «есть ещё» и продолжает листаться; неполная
// (видимых меньше страницы) — нет.
func TestRun_HasMoreReflectsVisiblePageFill(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 5) // 5 видимых строк
	deps := fakeDeps{entities: []*metadata.Entity{e}}

	full, err := Run(ctx, db, deps, "ромашка", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Items) != 2 || !full.HasMore {
		t.Fatalf("полная страница должна сообщать has_more: %+v", full)
	}
	tail, err := Run(ctx, db, deps, "ромашка", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Items) != 5 || tail.HasMore {
		t.Fatalf("неполная страница не должна сообщать has_more: %+v", tail)
	}
}

func TestRun_PaginatesWithoutRepeats(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 5)
	deps := fakeDeps{entities: []*metadata.Entity{e}}

	first, err := Run(ctx, db, deps, "ромашка", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(ctx, db, deps, "ромашка", 2, first.NextOffset)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, it := range append(append([]Result{}, first.Items...), second.Items...) {
		if seen[it.ID] {
			t.Fatalf("объект %s повторился на второй странице", it.ID)
		}
		seen[it.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("две страницы по 2 должны дать 4 разных объекта, получено %d", len(seen))
	}
}

// Объект с `fulltext: []` не участвует в поиске даже при полном доступе.
func TestRun_SkipsEntitiesExcludedFromIndex(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 3)
	e.FullTextSet = true
	e.FullText = nil

	page, err := Run(ctx, db, fakeDeps{entities: []*metadata.Entity{e}}, "ромашка", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("объект вне индекса попал в выдачу: %+v", page)
	}
}

func TestRun_EmptyQueryReturnsNothing(t *testing.T) {
	ctx := context.Background()
	db, e := newSearchFixture(t, 2)
	page, err := Run(ctx, db, fakeDeps{entities: []*metadata.Entity{e}}, "   ", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.HasMore {
		t.Fatalf("пустой запрос должен давать пустую выдачу: %+v", page)
	}
}
