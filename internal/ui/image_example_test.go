package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
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

// Ошибка записи объекта происходит уже после СохранитьКартинку. Обычный
// обработанный rollback должен удалить строку blob и компенсировать disk-файл;
// после устранения ошибки тот же процесс обязан успешно повториться.
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

// Параллельные запуски через независимые Server проходят реальный Query →
// lock → повторное чтение → транзакционную запись. Их process-local lockMgr не
// пересекаются: на PostgreSQL сериализацию обязан обеспечить advisory lock, а
// не общий mutex. Только один запуск создаёт blob и записывает его UUID в строку.
func TestTradeImageDemo_ConcurrentRunsCreateSingleBlob(t *testing.T) {
	proj, err := project.Load("../../examples/trade")
	if err != nil {
		t.Fatalf("загрузка examples/trade: %v", err)
	}
	defer proj.Close()
	nomenclature := tradeNomenclature(t, proj)

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
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
		if db.IsPostgres() {
			// Widen the critical window deterministically. Without the advisory
			// lock, independent servers all load the blank row, create their own
			// blobs, then collide on optimistic UPDATE; with the lock, only the
			// first update reaches this delay and the rest re-read filled Фото.
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
			if _, err := db.Exec(ctx, `CREATE FUNCTION _test_delay_image_update()
				RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN PERFORM pg_sleep(0.25); RETURN NEW; END; $$`); err != nil {
				t.Fatalf("create delay function: %v", err)
			}
			triggerSQL := fmt.Sprintf(`CREATE TRIGGER _test_delay_image_update
				BEFORE UPDATE OF %s ON %s FOR EACH ROW
				EXECUTE FUNCTION _test_delay_image_update()`,
				photoColumn, metadata.TableName(nomenclature.Name))
			if _, err := db.Exec(ctx, triggerSQL); err != nil {
				t.Fatalf("create delay trigger: %v", err)
			}
		}

		type offlineRuntime struct {
			server *Server
			reg    *runtime.Registry
		}
		const runners = 8
		runtimes := make([]offlineRuntime, runners)
		for i := range runtimes {
			s, reg, err := NewOfflineServer(proj, db)
			if err != nil {
				t.Fatal(err)
			}
			runtimes[i] = offlineRuntime{server: s, reg: reg}
		}
		seenLockManagers := map[*runtime.LockManager]bool{}
		for _, rt := range runtimes {
			if seenLockManagers[rt.server.lockMgr] {
				t.Fatal("конкурентный тест неожиданно переиспользует process lockMgr")
			}
			seenLockManagers[rt.server.lockMgr] = true
		}

		start := make(chan struct{})
		errCh := make(chan error, runners)
		var wg sync.WaitGroup
		for i := 0; i < runners; i++ {
			rt := runtimes[i]
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, runErr, setupErr := rt.server.RunProcessor(ctx, rt.reg, "ДемоФотоНоменклатуры", nil, nil, nil)
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
		photo, _ := row["Фото"].(string)
		photoID, err := uuid.Parse(photo)
		if err != nil {
			t.Fatalf("строка получила некорректный UUID Фото %q: %v", photo, err)
		}
		blob, rc, err := db.OpenBlob(ctx, photoID)
		if err != nil {
			t.Fatalf("Фото строки не указывает на единственный blob: %v", err)
		}
		_ = rc.Close()
		if blob.OwnerKind != string(nomenclature.Kind) || blob.OwnerEntity != nomenclature.Name {
			t.Fatalf("владелец единственного blob = %q/%q", blob.OwnerKind, blob.OwnerEntity)
		}
	})
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
