package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestTradeImageDemo_StoresOnlyUsedCategories проверяет пример тем же публичным
// путём, которым его запускают через procrun/UI. Добавление одной новой категории
// должно создавать ровно одну картинку: dsl-managed блобы GC не удаляет, поэтому
// предварительное сохранение всех четырёх плашек оставляло вечные сироты. Заодно
// последовательно декодируются и записываются все четыре встроенных PNG.
func TestTradeImageDemo_StoresOnlyUsedCategories(t *testing.T) {
	ctx := context.Background()
	proj, err := project.Load("../../examples/trade")
	if err != nil {
		t.Fatalf("загрузка examples/trade: %v", err)
	}
	defer proj.Close()

	var nomenclature *metadata.Entity
	for _, entity := range proj.Entities {
		if entity.Name == "Номенклатура" {
			nomenclature = entity
			break
		}
	}
	if nomenclature == nil {
		t.Fatal("справочник Номенклатура не найден")
	}
	if nomenclature.TileView == nil || nomenclature.TileView.Image != "Фото" {
		t.Fatalf("tile_view.image = %#v, ожидалось Фото", nomenclature.TileView)
	}
	photoFieldFound := false
	for _, field := range nomenclature.Fields {
		if field.Name == "Фото" && metadata.IsImage(field.Type) {
			photoFieldFound = true
			break
		}
	}
	if !photoFieldFound {
		t.Fatal("у Номенклатуры нет реквизита Фото типа image")
	}

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "image-example.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("blob schema: %v", err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatalf("file storage mode: %v", err)
	}

	run := func() {
		t.Helper()
		_, runErr, err := RunProcessorOffline(ctx, proj, db, "ДемоФотоНоменклатуры", nil, nil)
		if err != nil {
			t.Fatalf("запуск обработки: %v", err)
		}
		if runErr != nil {
			t.Fatalf("выполнение обработки: %v", runErr)
		}
	}
	countBlobs := func() int {
		t.Helper()
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _blobs").Scan(&count); err != nil {
			t.Fatalf("подсчёт блобов: %v", err)
		}
		return count
	}

	items := []string{
		"Доска для проверки image-демо",
		"Гвоздь для проверки image-демо",
		"Петля для проверки image-демо",
		"Молоток для проверки image-демо",
	}
	for index, name := range items {
		id := uuid.New()
		if err := db.Upsert(ctx, nomenclature.Name, id, map[string]any{
			"Наименование": name,
			"Фото":         "",
		}, nomenclature); err != nil {
			t.Fatalf("запись номенклатуры %q: %v", name, err)
		}

		run()
		if got, want := countBlobs(), index+1; got != want {
			t.Fatalf("после добавления категории %q создано %d блобов, ожидалось %d", name, got, want)
		}
		row, err := db.GetByID(ctx, nomenclature.Name, id, nomenclature)
		if err != nil {
			t.Fatalf("чтение номенклатуры %q: %v", name, err)
		}
		if photo, _ := row["Фото"].(string); photo == "" {
			t.Fatalf("обработка не записала UUID картинки для %q", name)
		}
	}

	run()
	if got := countBlobs(); got != len(items) {
		t.Fatalf("повторный запуск увеличил число блобов до %d, ожидалось %d", got, len(items))
	}
}
