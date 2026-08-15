//go:build integration

package backup

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Универсальный бэкап обязан работать в базе, живущей НЕ в схеме public (#877).
//
// Правило пакета storage записано прямо в коде (ConnectWithSchema, #665): любая
// интроспекция каталога фильтруется по current_schema(), а не по литералу
// 'public'. Универсальный бэкап его нарушал в семи местах, поэтому база с
// собственным search_path выгружалась и восстанавливалась МИМО своих таблиц:
// список таблиц приходил чужим или пустым, а типы колонок брались не оттуда,
// где лежат данные.
//
// Тест идёт публичным путём ExportUniversal → ImportUniversal и сверяет
// СОДЕРЖИМОЕ, а не код возврата: пустая выгрузка «успешна» ровно так же, как
// полная.
func TestUniversalBackupRoundTripInNonPublicSchema(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	const schema = "ob_schema_877"
	admin, err := storage.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
		admin.Close()
		t.Fatalf("подготовка схемы: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("CREATE SCHEMA: %v", err)
	}
	admin.Close()
	t.Cleanup(func() {
		db, err := storage.Connect(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer db.Close()
		_, _ = db.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})

	client := &metadata.Entity{
		Name:   "Контрагент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}

	db, err := storage.ConnectWithSchema(ctx, baseDSN, schema)
	if err != nil {
		t.Fatalf("ConnectWithSchema: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, []*metadata.Entity{client}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, client.Name, id, map[string]any{"Наименование": "ООО Схема"}, client); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	cfgDir := t.TempDir()
	if err := os.MkdirAll(cfgDir+"/config", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgDir+"/config/app.yaml", []byte("name: Тест\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := ExportUniversal(ctx, db, "file", cfgDir, t.TempDir(), "schema-test", &archive); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	if archive.Len() == 0 {
		t.Fatal("архив пуст")
	}

	// «Авария»: данные снесены. Таблица останется — её восстановит импорт.
	if _, err := db.Exec(ctx, `DELETE FROM `+metadata.TableName(client.Name)); err != nil {
		t.Fatalf("симуляция аварии: %v", err)
	}
	if row, err := db.GetByID(ctx, client.Name, id, client); err == nil && row != nil {
		t.Fatal("подготовка: запись не удалилась")
	}

	data := archive.Bytes()
	report, err := ImportUniversal(ctx, db, "file", t.TempDir(), t.TempDir(),
		bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ImportUniversal: %v", err)
	}
	if report == nil {
		t.Fatal("отчёт импорта пуст")
	}

	row, err := db.GetByID(ctx, client.Name, id, client)
	if err != nil {
		t.Fatalf("после импорта записи нет — бэкап прошёл мимо таблиц своей схемы: %v", err)
	}
	if got, _ := row["Наименование"].(string); got != "ООО Схема" {
		t.Fatalf("после импорта Наименование = %q, ожидалось «ООО Схема»", got)
	}
}
