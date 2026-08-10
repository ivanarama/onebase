package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	var ownerAware int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM _blobs WHERE owner_kind=? AND owner_entity=? AND dsl_managed=0",
		string(nomenclature.Kind), nomenclature.Name).Scan(&ownerAware); err != nil {
		t.Fatalf("проверка владельцев блобов: %v", err)
	}
	if ownerAware != len(items) {
		t.Fatalf("owner-aware блобов = %d, ожидалось %d", ownerAware, len(items))
	}
}

// Ошибка записи объекта происходит уже после СохранитьКартинку. Блоб и файл
// на диске должны откатиться той же транзакцией, а после устранения ошибки тот
// же процесс обязан успешно повториться.
func TestTradeImageDemo_ObjectSaveFailureRollsBackBlob(t *testing.T) {
	ctx := context.Background()
	proj, err := project.Load("../../examples/trade")
	if err != nil {
		t.Fatalf("загрузка examples/trade: %v", err)
	}
	defer proj.Close()
	nomenclature := tradeNomenclature(t, proj)

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "image-failure.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	filesDir := filepath.Join(t.TempDir(), "blob-files")
	db.SetFilesDir(filesDir)
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("blob schema: %v", err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err != nil {
		t.Fatalf("file storage mode: %v", err)
	}

	id := uuid.New()
	if err := db.Upsert(ctx, nomenclature.Name, id, map[string]any{
		"Наименование": "Доска с ошибкой записи",
		"Фото":         "",
	}, nomenclature); err != nil {
		t.Fatalf("запись номенклатуры: %v", err)
	}
	photoColumn := ""
	for _, field := range nomenclature.Fields {
		if field.Name == "Фото" {
			photoColumn = metadata.ColumnName(field)
			break
		}
	}
	if photoColumn == "" {
		t.Fatal("колонка Фото не найдена")
	}
	table := metadata.TableName(nomenclature.Name)
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER fail_image_save BEFORE UPDATE OF %s ON %s
		BEGIN SELECT RAISE(ABORT, 'forced image save failure'); END`, photoColumn, table)
	if _, err := db.Exec(ctx, triggerSQL); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, runErr, err := RunProcessorOffline(ctx, proj, db, "ДемоФотоНоменклатуры", nil, nil)
	if err != nil {
		t.Fatalf("запуск обработки: %v", err)
	}
	if runErr == nil {
		t.Fatal("ошибка Объект.Записать() не дошла до вызывающего")
	}
	var blobs int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _blobs").Scan(&blobs); err != nil {
		t.Fatalf("подсчёт блобов после rollback: %v", err)
	}
	if blobs != 0 {
		t.Fatalf("ошибка записи оставила %d строк _blobs", blobs)
	}
	entries, err := os.ReadDir(filepath.Join(filesDir, "_blobs"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("чтение каталога блобов: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ошибка записи оставила %d файлов-блобов", len(entries))
	}

	if _, err := db.Exec(ctx, "DROP TRIGGER fail_image_save"); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	_, runErr, err = RunProcessorOffline(ctx, proj, db, "ДемоФотоНоменклатуры", nil, nil)
	if err != nil || runErr != nil {
		t.Fatalf("повторный запуск после устранения ошибки: setup=%v run=%v", err, runErr)
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _blobs").Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if blobs != 1 {
		t.Fatalf("после успешного повтора блобов = %d, ожидался 1", blobs)
	}
}

// Параллельные запуски на одном сервере проходят реальный Query → lock →
// повторное чтение → транзакционную запись. Только один из них создаёт blob;
// остальные после ожидания видят уже заполненное Фото и ничего не сохраняют.
func TestTradeImageDemo_ConcurrentRunsCreateSingleBlob(t *testing.T) {
	ctx := context.Background()
	proj, err := project.Load("../../examples/trade")
	if err != nil {
		t.Fatalf("загрузка examples/trade: %v", err)
	}
	defer proj.Close()
	nomenclature := tradeNomenclature(t, proj)

	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "image-concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, nomenclature.Name, id, map[string]any{
		"Наименование": "Гвоздь конкурентный",
		"Фото":         "",
	}, nomenclature); err != nil {
		t.Fatal(err)
	}

	s, reg, err := NewOfflineServer(proj, db)
	if err != nil {
		t.Fatal(err)
	}
	const runners = 8
	start := make(chan struct{})
	errCh := make(chan error, runners)
	var wg sync.WaitGroup
	for i := 0; i < runners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, runErr, setupErr := s.RunProcessor(ctx, reg, "ДемоФотоНоменклатуры", nil, nil, nil)
			if setupErr != nil {
				errCh <- setupErr
				return
			}
			errCh <- runErr
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for runErr := range errCh {
		if runErr != nil {
			t.Fatalf("параллельный запуск завершился ошибкой: %v", runErr)
		}
	}

	var blobs int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM _blobs").Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if blobs != 1 {
		t.Fatalf("параллельные запуски создали %d блобов, ожидался 1", blobs)
	}
	row, err := db.GetByID(ctx, nomenclature.Name, id, nomenclature)
	if err != nil {
		t.Fatal(err)
	}
	if photo, _ := row["Фото"].(string); photo == "" {
		t.Fatal("параллельные запуски не записали Фото")
	}
}

func tradeNomenclature(t *testing.T, proj *project.Project) *metadata.Entity {
	t.Helper()
	for _, entity := range proj.Entities {
		if entity.Name == "Номенклатура" {
			return entity
		}
	}
	t.Fatal("справочник Номенклатура не найден")
	return nil
}
