//go:build integration

package storage

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Полнотекстовый поиск на PostgreSQL (план 82): тот же контракт, что в
// fts_test.go на SQLite, плюс морфология — её FTS5 не умеет, поэтому проверять
// её можно только здесь.

func connectPGForFTS(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func newPGFTSFixture(t *testing.T) (*DB, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db := connectPGForFTS(t)

	cat := &metadata.Entity{
		Name: "ФТСКонтрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
			{Name: "Tenant", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "ФТСНакладная",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Основание", Type: metadata.FieldTypeString},
		},
	}
	entities := []*metadata.Entity{cat, doc}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		for _, e := range entities {
			_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS "+metadata.TableName(e.Name))
			_, _ = db.Exec(context.Background(), "DELETE FROM "+ftsTable+" WHERE owner_name = $1", e.Name)
		}
	})
	// Прогон мог оставить строки от прошлого падения — начинаем с чистого листа.
	for _, e := range entities {
		if _, err := db.Exec(ctx, "DELETE FROM "+metadata.TableName(e.Name)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "DELETE FROM "+ftsTable+" WHERE owner_name = $1", e.Name); err != nil {
			t.Fatal(err)
		}
	}
	return db, cat, doc
}

func searchPG(t *testing.T, db *DB, text string, names ...string) []FTSHit {
	t.Helper()
	if len(names) == 0 {
		names = []string{"ФТСКонтрагент", "ФТСНакладная"}
	}
	hits, err := db.SearchFullText(context.Background(), FTSQuery{Text: text, Names: names, Limit: 20})
	if err != nil {
		t.Fatalf("поиск %q: %v", text, err)
	}
	return hits
}

func TestFullTextPG_FindsObjectsAcrossEntities(t *testing.T) {
	ctx := context.Background()
	db, cat, doc := newPGFTSFixture(t)

	romashka := uuid.New()
	if err := db.Upsert(ctx, cat.Name, romashka, map[string]any{
		"Наименование": "ООО Ромашка",
		"Комментарий":  "поставщик канцтоваров",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ЗАО Василёк"}, cat); err != nil {
		t.Fatal(err)
	}
	invoice := uuid.New()
	if err := db.Upsert(ctx, doc.Name, invoice, map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	hits := searchPG(t, db, "ромашк")
	if len(hits) != 2 {
		t.Fatalf("ожидались справочник и документ, получено %+v", hits)
	}
	if hits[0].ID != romashka {
		t.Fatalf("совпадение в представлении должно ранжироваться выше: %+v", hits)
	}
	if got := searchPG(t, db, "ромашк", doc.Name); len(got) != 1 || got[0].ID != invoice {
		t.Fatalf("выдача должна ограничиваться переданными объектами: %+v", got)
	}
}

func TestFullTextPG_ScopeAppliedBeforeTopN(t *testing.T) {
	ctx := context.Background()
	db, cat, _ := newPGFTSFixture(t)
	const marker = "scopeboundarytoken"

	for i := 0; i < 31; i++ {
		if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
			"Наименование": marker + " foreign",
			"Tenant":       "site-b",
		}, cat); err != nil {
			t.Fatal(err)
		}
	}
	localID := uuid.New()
	if err := db.Upsert(ctx, cat.Name, localID, map[string]any{
		"Наименование": "local page",
		"Комментарий":  marker,
		"Tenant":       "site-a",
	}, cat); err != nil {
		t.Fatal(err)
	}

	global, err := db.SearchFullText(ctx, FTSQuery{Text: marker, Names: []string{cat.Name}, Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if hitIDs(global)[localID] {
		t.Fatalf("инвариант регрессии нарушен: body-hit попал в глобальную top-30: %+v", global)
	}
	scoped, err := db.SearchFullText(ctx, FTSQuery{
		Text:  marker,
		Names: []string{cat.Name},
		Scopes: []FTSScope{{
			Entity:    cat,
			Predicate: Predicate{Field: "Tenant", Op: "eq", Value: "site-a"},
		}},
		Limit: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ID != localID {
		t.Fatalf("scope должен примениться до top-N, получено %+v", scoped)
	}
}

// Морфология: в PostgreSQL индекс лежит в стемированных лексемах, поэтому
// словоформа запроса не обязана совпадать со словоформой в данных.
func TestFullTextPG_Morphology(t *testing.T) {
	ctx := context.Background()
	db, cat, _ := newPGFTSFixture(t)

	if db.ftsConfig(ctx) != pgFTSConfigRussian {
		t.Skip("в сборке PostgreSQL нет конфигурации russian")
	}
	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ООО Ромашка",
		"Комментарий":  "поставка канцелярских товаров по договорам",
	}, cat); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"договор", "договоры", "товар", "поставки"} {
		if hits := searchPG(t, db, q); len(hits) != 1 {
			t.Fatalf("запрос %q: ожидалось совпадение по словоформе, получено %+v", q, hits)
		}
	}
}

// Тот же сценарий, что TestFullText_FindsPartsOfPunctuatedValues на SQLite —
// именно на PostgreSQL он и ломался: разборщик оставляет знак в лексеме числа
// («РН-000012» → «-000012»), поэтому без нормализации индексируемого текста
// поиск по «000012» или по телефону без плюса не находил ничего.
func TestFullTextPG_FindsPartsOfPunctuatedValues(t *testing.T) {
	ctx := context.Background()
	db, cat, doc := newPGFTSFixture(t)

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ООО «Ромашка-Плюс»",
		"Комментарий":  "тел. +79990001122, e-mail sales@romashka.ru",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "РН-000012"}, doc); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"000012", "79990001122", "7999", "плюс", "sales", "romashka"} {
		if hits := searchPG(t, db, q); len(hits) != 1 {
			t.Fatalf("запрос %q: ожидалось одно совпадение, получено %+v", q, hits)
		}
	}
}

func TestFullTextPG_IncrementalUpdateDeleteAndRollback(t *testing.T) {
	ctx := context.Background()
	db, cat, _ := newPGFTSFixture(t)

	id := uuid.New()
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if hits := searchPG(t, db, "ромашка"); len(hits) != 1 {
		t.Fatalf("после записи ожидалось совпадение: %+v", hits)
	}
	if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Василёк"}, cat); err != nil {
		t.Fatal(err)
	}
	if hits := searchPG(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("старое наименование не должно находиться: %+v", hits)
	}

	rollbackID := uuid.New()
	wantErr := context.Canceled
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		if err := db.Upsert(txCtx, cat.Name, rollbackID, map[string]any{"Наименование": "ООО Лютик"}, cat); err != nil {
			return err
		}
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("ожидалась ошибка транзакции, получено %v", err)
	}
	if hits := searchPG(t, db, "лютик"); len(hits) != 0 {
		t.Fatalf("откат записи должен откатывать и индекс: %+v", hits)
	}

	if err := db.Delete(ctx, cat.Name, id); err != nil {
		t.Fatal(err)
	}
	if hits := searchPG(t, db, "василёк"); len(hits) != 0 {
		t.Fatalf("удалённый объект остался в индексе: %+v", hits)
	}
}

func TestFullTextPG_RebuildRestoresIndexFromData(t *testing.T) {
	ctx := context.Background()
	db, cat, doc := newPGFTSFixture(t)

	if err := db.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "РН-000012"}, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM "+ftsTable+" WHERE owner_name IN ($1, $2)", cat.Name, doc.Name); err != nil {
		t.Fatal(err)
	}
	if hits := searchPG(t, db, "ромашка"); len(hits) != 0 {
		t.Fatalf("индекс должен быть пуст перед пересборкой: %+v", hits)
	}

	if _, err := db.RebuildFullTextIndex(ctx, []*metadata.Entity{cat, doc}, 1, nil); err != nil {
		t.Fatal(err)
	}
	if hits := searchPG(t, db, "ромашка"); len(hits) != 1 {
		t.Fatalf("после пересборки объект должен находиться: %+v", hits)
	}
	if hits := searchPG(t, db, "000012"); len(hits) != 1 {
		t.Fatalf("после пересборки документ должен находиться по номеру: %+v", hits)
	}
}
