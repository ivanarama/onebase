package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Поиск подстроки в списке и в подборе ссылки шёл по всем строковым реквизитам
// и только по ним. Артикул и штрихкод в реальных конфигурациях часто хранят
// числом — по ним не находилось ничего, а именно так набирают позицию те, кто
// работает с клавиатуры. Блок `search_fields:` позволяет перечислить состав
// явно; умолчание не изменилось.
//
// Тест матричный: приведение колонки к тексту делают разные выражения диалектов
// (LOWER(col::text) против ob_lower(CAST(col AS TEXT))), и на числовой колонке
// разойтись они могут молча.

func searchEntity(search []string, set bool) *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeNumber},
		},
		Search:    search,
		SearchSet: set,
	}
}

func seedNomenclature(t *testing.T, db *storage.DB, ent *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rows := []map[string]any{
		{"Наименование": "Яблоки Гала", "Артикул": 12345},
		{"Наименование": "Груша Конференция", "Артикул": 67890},
	}
	for _, r := range rows {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), r, ent); err != nil {
			t.Fatalf("Upsert %v: %v", r, err)
		}
	}
}

func searchNames(t *testing.T, db *storage.DB, ent *metadata.Entity, q string) []string {
	t.Helper()
	rows, err := db.List(context.Background(), ent.Name, ent, storage.ListParams{Search: q})
	if err != nil {
		t.Fatalf("List(search=%q): %v", q, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, toStr(r["Наименование"]))
	}
	return out
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return ""
}

// Умолчание сохранено: без блока ищем по строковым реквизитам и не ищем по числу.
func TestSearchFields_УмолчаниеТолькоСтроковые(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := searchEntity(nil, false)
		seedNomenclature(t, db, ent)

		if got := searchNames(t, db, ent, "ябло"); len(got) != 1 || got[0] != "Яблоки Гала" {
			t.Errorf("поиск по наименованию сломан: %v", got)
		}
		if got := searchNames(t, db, ent, "12345"); len(got) != 0 {
			t.Errorf("числовой реквизит не должен искаться без search_fields: %v", got)
		}
	})
}

// Явный список включает в поиск числовой артикул — ради этого блок и нужен.
func TestSearchFields_ЯвныйСписокВключаетЧисловой(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := searchEntity([]string{"Наименование", "Артикул"}, true)
		seedNomenclature(t, db, ent)

		if got := searchNames(t, db, ent, "12345"); len(got) != 1 || got[0] != "Яблоки Гала" {
			t.Errorf("по артикулу не нашлось: %v", got)
		}
		// Частичный ввод — как при наборе с клавиатуры, до конца артикул не добивают.
		if got := searchNames(t, db, ent, "678"); len(got) != 1 || got[0] != "Груша Конференция" {
			t.Errorf("частичный артикул не нашёлся: %v", got)
		}
		if got := searchNames(t, db, ent, "груш"); len(got) != 1 {
			t.Errorf("наименование перестало искаться: %v", got)
		}
	})
}

// Реквизит, не попавший в явный список, из поиска исключается.
func TestSearchFields_ЯвныйСписокСужает(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := searchEntity([]string{"Артикул"}, true)
		seedNomenclature(t, db, ent)

		if got := searchNames(t, db, ent, "12345"); len(got) != 1 {
			t.Errorf("по артикулу не нашлось: %v", got)
		}
		if got := searchNames(t, db, ent, "ябло"); len(got) != 0 {
			t.Errorf("наименование не указано в search_fields, искаться не должно: %v", got)
		}
	})
}

// Явный пустой список выключает поиск по строке: выдача не «всё подряд», иначе
// пользователь принял бы полный список за результат поиска.
func TestSearchFields_ПустойСписокВыключаетПоиск(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := searchEntity([]string{}, true)
		seedNomenclature(t, db, ent)

		if got := searchNames(t, db, ent, "ябло"); len(got) != 0 {
			t.Errorf("при search_fields: [] поиск должен давать пусто, получено: %v", got)
		}
		if got := searchNames(t, db, ent, ""); len(got) != 2 {
			t.Errorf("без строки поиска список обязан остаться полным: %v", got)
		}
	})
}
