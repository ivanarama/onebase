//go:build integration

package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
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

	schema := storage.NewEphemeralSchemaName()
	db, err := storage.ConnectWithSchema(ctx, baseDSN, schema)
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

	probe := &metadata.Entity{
		Name:   "Schema877UniversalBackupProbe",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "ProbeValue", Type: metadata.FieldTypeString}},
	}
	probeTable := metadata.TableName(probe.Name)
	// The old implementation enumerated public tables and then read them through
	// search_path. A shared entity name could therefore make this test pass by
	// accident even though the probe table in the active schema was never listed.
	var existsInPublic bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = $1
	)`, probeTable).Scan(&existsInPublic); err != nil {
		t.Fatalf("check public probe precondition: %v", err)
	}
	if existsInPublic {
		t.Fatalf("test precondition violated: probe table %q already exists in public", probeTable)
	}

	if err := db.Migrate(ctx, []*metadata.Entity{probe}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, probe.Name, id, map[string]any{"ProbeValue": "schema-877-roundtrip"}, probe); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	cfgDir := t.TempDir()
	configDir := filepath.Join(cfgDir, "config")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "app.yaml"), []byte("name: Тест\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if err := ExportUniversal(ctx, db, "file", cfgDir, t.TempDir(), "schema-test", &archive); err != nil {
		t.Fatalf("ExportUniversal: %v", err)
	}
	if archive.Len() == 0 {
		t.Fatal("архив пуст")
	}
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("open exported archive: %v", err)
	}
	// Together with the public-schema precondition above, this is the direct
	// regression assertion: the old public-only table listing omits this entry.
	wantEntry := "data/" + probeTable + ".jsonl"
	foundProbe := false
	for _, entry := range zr.File {
		if entry.Name == wantEntry {
			foundProbe = true
			break
		}
	}
	if !foundProbe {
		t.Fatalf("archive omitted non-public probe table %q (entry %q)", probeTable, wantEntry)
	}

	// «Авария»: данные снесены. Таблица останется — её восстановит импорт.
	if _, err := db.Exec(ctx, `DELETE FROM `+probeTable); err != nil {
		t.Fatalf("симуляция аварии: %v", err)
	}
	if _, err := db.GetByID(ctx, probe.Name, id, probe); !storage.IsNotFound(err) {
		if err != nil {
			t.Fatalf("проверка симуляции аварии: %v", err)
		}
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

	row, err := db.GetByID(ctx, probe.Name, id, probe)
	if err != nil {
		t.Fatalf("после импорта записи нет — бэкап прошёл мимо таблиц своей схемы: %v", err)
	}
	if got, _ := row["ProbeValue"].(string); got != "schema-877-roundtrip" {
		t.Fatalf("после импорта ProbeValue = %q, ожидалось %q", got, "schema-877-roundtrip")
	}
}
