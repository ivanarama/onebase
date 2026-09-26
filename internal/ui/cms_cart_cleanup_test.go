package ui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestCMSCartCleanupProcessor runs the production processor declaration through
// the same Server.RunProcessor entry point used by procrun and scheduled jobs.
// A frozen clock makes the exact 45-day boundary deterministic.
func TestCMSCartCleanupProcessor(t *testing.T) {
	proj, err := project.Load("../../examples/cms")
	if err != nil {
		t.Fatalf("загрузка examples/cms: %v", err)
	}
	defer proj.Close()
	jobFound := false
	for _, job := range proj.ScheduledJobs {
		if job.Name != "ОчисткаКорзин" {
			continue
		}
		jobFound = true
		if job.Processor != "ОчисткаКорзин" || job.Schedule != "30 3 * * *" || !job.Enabled {
			t.Fatalf("регламентное задание ОчисткаКорзин загружено неверно: %#v", job)
		}
	}
	if !jobFound {
		t.Fatal("регламентное задание ОчисткаКорзин не найдено")
	}

	carts := cmsCleanupEntity(t, proj, "Корзины")
	orders := cmsCleanupEntity(t, proj, "ЗаказПокупателя")
	var cartRows *metadata.TablePart
	for i := range carts.TableParts {
		if carts.TableParts[i].Name == "Строки" {
			cartRows = &carts.TableParts[i]
			break
		}
	}
	if cartRows == nil {
		t.Fatal("табличная часть Корзины.Строки не найдена")
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatalf("миграция CMS: %v", err)
		}

		fixedNow := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
		cutoff := fixedNow.AddDate(0, 0, -45)
		raceCandidate := uuid.New()
		if err := db.Upsert(ctx, carts.Name, raceCandidate, map[string]any{
			"Наименование": "Корзина, изменённая во время очистки",
			"Дата":         cutoff.Add(-time.Hour),
			"Оформлена":    false,
		}, carts); err != nil {
			t.Fatalf("запись корзины для гонки: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, carts.Name, cartRows.Name, raceCandidate, []map[string]any{{
			"Количество": 1,
		}}, *cartRows); err != nil {
			t.Fatalf("запись строки корзины для гонки: %v", err)
		}
		expired := make([]uuid.UUID, 101)
		for i := range expired {
			expired[i] = uuid.New()
			if err := db.Upsert(ctx, carts.Name, expired[i], map[string]any{
				"Наименование": fmt.Sprintf("Просроченная корзина %03d", i+1),
				"Дата":         cutoff.Add(-time.Second),
				"Оформлена":    false,
			}, carts); err != nil {
				t.Fatalf("запись просроченной корзины %d: %v", i+1, err)
			}
		}
		if err := db.UpsertTablePartRows(ctx, carts.Name, cartRows.Name, expired[0], []map[string]any{{
			"Количество": 1,
		}}, *cartRows); err != nil {
			t.Fatalf("запись строки просроченной корзины: %v", err)
		}

		atBoundary := uuid.New()
		if err := db.Upsert(ctx, carts.Name, atBoundary, map[string]any{
			"Наименование": "Корзина ровно на границе",
			"Дата":         cutoff,
			"Оформлена":    false,
		}, carts); err != nil {
			t.Fatalf("запись корзины на границе: %v", err)
		}
		fresh := uuid.New()
		if err := db.Upsert(ctx, carts.Name, fresh, map[string]any{
			"Наименование": "Свежая корзина",
			"Дата":         cutoff.Add(time.Second),
			"Оформлена":    false,
		}, carts); err != nil {
			t.Fatalf("запись свежей корзины: %v", err)
		}

		orderID := uuid.New()
		if err := db.Upsert(ctx, orders.Name, orderID, map[string]any{
			"Номер": "ЗС-ТЕСТ",
			"Дата":  fixedNow,
		}, orders); err != nil {
			t.Fatalf("запись заказа: %v", err)
		}
		completed := uuid.New()
		if err := db.Upsert(ctx, carts.Name, completed, map[string]any{
			"Наименование": "Старая оформленная корзина",
			"Дата":         cutoff.Add(-time.Hour),
			"Оформлена":    true,
			"Заказ":        orderID.String(),
		}, carts); err != nil {
			t.Fatalf("запись оформленной корзины: %v", err)
		}

		server, reg, err := NewOfflineServer(proj, db)
		if err != nil {
			t.Fatalf("offline runtime: %v", err)
		}
		profile := interpreter.NewTestProfile()
		vars := profile.Vars()
		clock, ok := vars["Часы"].(*interpreter.ClockRoot)
		if !ok {
			t.Fatal("тестовые часы не зарегистрированы")
		}
		clock.CallMethod("Установить", []any{fixedNow})

		// Подменяем только корень Справочники: production-процессор и его
		// entityservice delete-path остаются настоящими. Фабрика обновляет одну
		// корзину после того, как ПолучитьОбъект() уже сохранил её _version, но
		// до вызова УдалитьЕслиНеИзменен(). Это детерминированная интерливинг-
		// регрессия той же гонки, которая в production возникает между двумя
		// соседними строками DSL.
		ctxSrc := interpreter.NewStaticCtx(ctx)
		raceInjected := false
		raceFactory := &cmsCleanupRaceFactory{
			base: server.catObjectFactory(ctxSrc),
			afterLoad: func(entity *metadata.Entity, uuidStr string) error {
				if raceInjected || entity.Name != carts.Name || uuidStr != raceCandidate.String() {
					return nil
				}
				raceInjected = true
				return db.Upsert(ctx, carts.Name, raceCandidate, map[string]any{
					"Дата":      fixedNow,
					"Оформлена": true,
				}, carts)
			},
		}
		vars["Справочники"] = interpreter.NewCatalogsRoot(ctxSrc, db, reg).
			WithObjectFactory(raceFactory).
			WithDeleter(dslCatalogDeleter{s: server})

		run := func() []string {
			t.Helper()
			messages, runErr, setupErr := server.RunProcessor(ctx, reg, "ОчисткаКорзин", nil, nil, vars)
			if setupErr != nil || runErr != nil {
				t.Fatalf("запуск ОчисткаКорзин: setup=%v run=%v", setupErr, runErr)
			}
			return messages
		}

		if got := run(); len(got) != 1 || got[0] != "Очистка корзин: удалено 101." {
			t.Fatalf("сообщения первого запуска = %#v", got)
		}
		if !raceInjected {
			t.Fatal("обновление между проверкой корзины и удалением не было внедрено")
		}
		assertCMSCleanupTableCount(t, ctx, db, metadata.TableName(carts.Name), 4)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", raceCandidate, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", expired[0], 0)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", atBoundary, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", fresh, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", completed, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TablePartTableName(carts.Name, cartRows.Name), "parent_id", raceCandidate, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TablePartTableName(carts.Name, cartRows.Name), "parent_id", expired[0], 0)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(orders.Name), "id", orderID, 1)
		racedRow, err := db.GetByID(ctx, carts.Name, raceCandidate, carts)
		if err != nil {
			t.Fatalf("чтение изменённой корзины: %v", err)
		}
		if !cmsCleanupTrue(racedRow["Оформлена"]) || racedRow["_version"] != int64(2) {
			t.Fatalf("изменённая корзина удалена или перезаписана: %#v", racedRow)
		}

		if got := run(); len(got) != 1 || got[0] != "Очистка корзин: удалено 0." {
			t.Fatalf("сообщения повторного запуска = %#v", got)
		}
		assertCMSCleanupTableCount(t, ctx, db, metadata.TableName(carts.Name), 4)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", raceCandidate, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", atBoundary, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", fresh, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", completed, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TablePartTableName(carts.Name, cartRows.Name), "parent_id", raceCandidate, 1)
		assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(orders.Name), "id", orderID, 1)
	})
}

type cmsCleanupRaceFactory struct {
	base      interpreter.CatalogObjectFactory
	afterLoad func(entity *metadata.Entity, uuidStr string) error
}

func (f *cmsCleanupRaceFactory) NewCatalogObject(entity *metadata.Entity) any {
	return f.base.NewCatalogObject(entity)
}

func (f *cmsCleanupRaceFactory) LoadCatalogObject(entity *metadata.Entity, uuidStr string) (any, error) {
	obj, err := f.base.LoadCatalogObject(entity, uuidStr)
	if err != nil {
		return nil, err
	}
	if f.afterLoad != nil {
		if err := f.afterLoad(entity, uuidStr); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

func cmsCleanupEntity(t *testing.T, proj *project.Project, name string) *metadata.Entity {
	t.Helper()
	for _, entity := range proj.Entities {
		if entity.Name == name {
			return entity
		}
	}
	t.Fatalf("объект %s не найден", name)
	return nil
}

func cmsCleanupTrue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int64:
		return v == 1
	case int:
		return v == 1
	case string:
		return v == "1" || v == "true"
	default:
		return false
	}
}

func assertCMSCleanupTableCount(t *testing.T, ctx context.Context, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("подсчёт %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("строк %s = %d, ожидалось %d", table, got, want)
	}
}

func assertCMSCleanupRowCount(t *testing.T, ctx context.Context, db *storage.DB, table, column string, id uuid.UUID, want int) {
	t.Helper()
	var got int
	placeholder := db.Dialect().Placeholder(1)
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+column+" = "+placeholder, id.String()).Scan(&got); err != nil {
		t.Fatalf("подсчёт %s для %s: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("строк %s для %s = %d, ожидалось %d", table, id, got, want)
	}
}
