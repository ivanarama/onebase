package storage_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Изоляция эфемерной схемы (план 136) была дырявой: search_path кончается
// на public, а интроспекция каталога фильтровалась по литералу 'public', и
// служебные таблицы платформы резолвились в ОБЩУЮ схему. Прогоны видели
// состояние друг друга, а реструктуризация (план 81) во втором и последующих
// прогонах молча не выполнялась — карта полей приходила из public и утверждала,
// что колонка уже нужного типа (issue #638).
//
// Тесты идут только через экспортированный API storage: пакет внешний
// (storage_test), приватную функцию отсюда не позвать физически.

func isolationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — тест изоляции требует PostgreSQL")
	}
	return dsn
}

// ephemeralDB поднимает подключение к свежей эфемерной схеме.
func ephemeralDB(t *testing.T, ctx context.Context, dsn string) *storage.DB {
	t.Helper()
	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("ConnectWithSchema: %v", err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatalf("CreateSchema: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropSchemaCascade(context.Background(), schema); err != nil {
			t.Errorf("DropSchemaCascade(%s): %v", schema, err)
		}
		db.Close()
	})
	return db
}

// Настройки рабочей базы не должны быть видны из эфемерной схемы, а запись в
// схеме — протекать обратно в public.
func TestSchemaIsolation_SettingsDoNotLeak_PG(t *testing.T) {
	dsn := isolationDSN(t)
	ctx := context.Background()
	const key = "iso638_probe"

	pub, err := storage.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(pub.Close) // регистрируем первым — cleanup'ы идут LIFO, закроется последним
	// Засеваем public сами: иначе тест зависел бы от того, что накопили в
	// сервисной базе соседние прогоны.
	if err := pub.SaveSetting(ctx, key, "public"); err != nil {
		t.Fatalf("SaveSetting в public: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pub.Exec(context.Background(), `DELETE FROM _settings WHERE key = $1`, key); err != nil {
			t.Errorf("очистка _settings: %v", err)
		}
	})

	iso := ephemeralDB(t, ctx, dsn)
	if got, ok, err := iso.GetSetting(ctx, key); err == nil && ok {
		t.Fatalf("эфемерная схема видит настройку рабочей базы: %q", got)
	}
	if err := iso.SaveSetting(ctx, key, "ephemeral"); err != nil {
		t.Fatalf("SaveSetting в эфемерной схеме: %v", err)
	}
	if got, ok, err := iso.GetSetting(ctx, key); err != nil || !ok || got != "ephemeral" {
		t.Errorf("в эфемерной схеме прочитано %q (ok=%v, err=%v), ожидалось \"ephemeral\"", got, ok, err)
	}
	if got, ok, err := pub.GetSetting(ctx, key); err != nil || !ok || got != "public" {
		t.Errorf("запись из эфемерной схемы протекла в public: %q (ok=%v, err=%v)", got, ok, err)
	}
}

// Регрессия ровно на симптом issue: реструктуризация в эфемерной схеме обязана
// действительно выполняться. Пока карта полей читалась из public, планировщик
// выдавал ноль изменений и ретайп string→bool молча не происходил.
func TestSchemaIsolation_RestructureRunsInEphemeralSchema_PG(t *testing.T) {
	dsn := isolationDSN(t)
	ctx := context.Background()
	db := ephemeralDB(t, ctx, dsn)

	entity := func(flagType metadata.FieldType) *metadata.Entity {
		return &metadata.Entity{
			Name: "ИзоРетайп638",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
				{ID: "f_flag", Name: "Активен", Type: flagType},
			},
		}
	}

	strEntity := entity(metadata.FieldTypeString)
	if err := db.Migrate(ctx, []*metadata.Entity{strEntity}); err != nil {
		t.Fatalf("Migrate(string): %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, strEntity.Name, id, map[string]any{
		"Наименование": "Проба",
		"Активен":      "yes",
	}, strEntity); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	boolEntity := entity(metadata.FieldTypeBool)
	if err := db.Migrate(ctx, []*metadata.Entity{boolEntity}); err != nil {
		t.Fatalf("Migrate(bool): %v", err)
	}
	row, err := db.GetByID(ctx, boolEntity.Name, id, boolEntity)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	got, isBool := row["Активен"].(bool)
	if !isBool {
		t.Fatalf("ретайп в эфемерной схеме не выполнен: значение %#v (%T), а не bool", row["Активен"], row["Активен"])
	}
	if !got {
		t.Errorf("значение \"yes\" после ретайпа = false, ожидалось true")
	}
}
