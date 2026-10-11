package ui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

// Exercise the public reread/delete sequence. In the former implementation the
// third context lookup occurred after reading fields but before EntityVersion,
// so a concurrent checkout lent its new token to the stale cart fields.
func TestCMSCartReadThenDeleteKeepsConcurrentCheckout(t *testing.T) {
	proj, err := project.Load("../../examples/cms")
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	carts := cmsCleanupEntity(t, proj, "Корзины")

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := db.Upsert(ctx, carts.Name, id, map[string]any{
			"Наименование": "Корзина с конкурентным оформлением",
			"Дата":         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			"Оформлена":    false,
		}, carts); err != nil {
			t.Fatal(err)
		}
		server, _, err := NewOfflineServer(proj, db)
		if err != nil {
			t.Fatal(err)
		}
		checkoutDone := false
		checkout := func() {
			if checkoutDone {
				return
			}
			checkoutDone = true
			if err := db.Upsert(ctx, carts.Name, id, map[string]any{"Оформлена": true}, carts); err != nil {
				t.Fatal(err)
			}
		}
		source := &cmsCleanupReadRaceContext{ctx: ctx}
		loaded, err := server.catObjectFactory(source).LoadCatalogObject(carts, id.String())
		if err != nil {
			t.Fatal(err)
		}
		obj := loaded.(interface {
			CallMethod(string, []any) any
			Get(string) any
		})
		source.calls = 0
		source.beforeThird = checkout
		obj.CallMethod("Прочитать", nil)
		// With one header read there is no second version query to intercept.
		// Commit immediately after reread instead, before the public CAS delete.
		checkout()
		source.beforeThird = nil
		if cmsCleanupTrue(obj.Get("Оформлена")) {
			t.Fatal("проверка гонки должна использовать поля до оформления")
		}
		if got := obj.CallMethod("УдалитьЕслиНеИзменен", nil); got != false {
			t.Fatalf("удаление конкурентно оформленной корзины = %v, ожидалось false", got)
		}
		row, err := db.GetByID(ctx, carts.Name, id, carts)
		if err != nil || row == nil || !cmsCleanupTrue(row["Оформлена"]) {
			t.Fatalf("оформленная корзина потеряна: row=%#v err=%v", row, err)
		}
		// A new public read must accept the current token and allow deletion
		// while that exact snapshot remains unchanged.
		obj.CallMethod("Прочитать", nil)
		if got := obj.CallMethod("УдалитьЕслиНеИзменен", nil); got != true {
			t.Fatalf("удаление неизменённого снимка = %v, ожидалось true", got)
		}
	})
}

type cmsCleanupReadRaceContext struct {
	ctx         context.Context
	calls       int
	beforeThird func()
}

func (s *cmsCleanupReadRaceContext) Ctx() context.Context {
	s.calls++
	if s.calls == 3 && s.beforeThird != nil {
		s.beforeThird()
	}
	return s.ctx
}

// The storage port injects a write immediately before the real versioned SQL
// DELETE. The public DSL method, service transaction and table-part deletion
// remain intact; no private delete helper is invoked by the test.
func TestCMSCartLateDeleteConflict(t *testing.T) {
	proj, err := project.Load("../../examples/cms")
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	carts := cmsCleanupEntity(t, proj, "Корзины")
	if len(carts.TableParts) == 0 {
		t.Fatal("корзина должна иметь табличные части для проверки отката")
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		for _, borrowed := range []bool{false, true} {
			name := "owned-transaction"
			if borrowed {
				name = "borrowed-savepoint"
			}
			t.Run(name, func(t *testing.T) {
				id := uuid.New()
				if err := db.Upsert(ctx, carts.Name, id, map[string]any{
					"Наименование": "Корзина с поздним конфликтом",
					"Дата":         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
					"Оформлена":    false,
				}, carts); err != nil {
					t.Fatal(err)
				}
				original, err := db.GetByID(ctx, carts.Name, id, carts)
				if err != nil || original == nil {
					t.Fatalf("исходная корзина: row=%#v err=%v", original, err)
				}
				originalParts := make(map[string][]map[string]any)
				for _, tp := range carts.TableParts {
					if err := db.UpsertTablePartRows(ctx, carts.Name, tp.Name, id,
						[]map[string]any{{"Количество": 2}, {"Количество": 7}}, tp); err != nil {
						t.Fatal(err)
					}
					rows, err := db.GetTablePartRows(ctx, carts.Name, tp.Name, id, tp)
					if err != nil || len(rows) != 2 {
						t.Fatalf("исходная ТЧ %s: rows=%#v err=%v", tp.Name, rows, err)
					}
					originalParts[tp.Name] = rows
				}
				assertRestored := func(readCtx context.Context) {
					t.Helper()
					row, err := db.GetByID(readCtx, carts.Name, id, carts)
					if err != nil || !reflect.DeepEqual(row, original) {
						t.Fatalf("корзина не восстановлена: row=%#v want=%#v err=%v", row, original, err)
					}
					for _, tp := range carts.TableParts {
						rows, err := db.GetTablePartRows(readCtx, carts.Name, tp.Name, id, tp)
						if err != nil || !reflect.DeepEqual(rows, originalParts[tp.Name]) {
							t.Fatalf("ТЧ %s не восстановлена: rows=%#v want=%#v err=%v", tp.Name, rows, originalParts[tp.Name], err)
						}
					}
				}

				runCtx := ctx
				unrelatedID, afterConflictID := uuid.New(), uuid.New()
				var outer storage.Tx
				if borrowed {
					outer, runCtx, err = db.BeginTx(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer outer.Rollback(ctx)
					// Written after BeginTx: a whole-transaction rollback would
					// erase this row, unlike the required savepoint rollback.
					if err := db.Upsert(runCtx, carts.Name, unrelatedID,
						map[string]any{"Наименование": "До конфликта"}, carts); err != nil {
						t.Fatal(err)
					}
				}
				server, _, err := NewOfflineServer(proj, db)
				if err != nil {
					t.Fatal(err)
				}
				injected := false
				port := &cmsCleanupLateDeleteStore{DB: db}
				port.beforeDelete = func(deleteCtx context.Context, entity string, target uuid.UUID, expected int64) {
					if entity != carts.Name || target != id {
						t.Fatalf("неожиданный объект DELETE: %s %s", entity, target)
					}
					if injected {
						return
					}
					if !storage.HasTx(deleteCtx) {
						t.Fatal("инъекция должна выполняться в транзакции удаления")
					}
					// Prove the early version check has passed and all table
					// parts have already been removed, while the header still
					// has the loaded token. This rejects an early injection.
					row, err := db.GetByID(deleteCtx, carts.Name, id, carts)
					if err != nil || row == nil || row["_version"] != expected || !reflect.DeepEqual(row, original) {
						t.Fatalf("инъекция не перед финальным DELETE: row=%#v expected=%d err=%v", row, expected, err)
					}
					for _, tp := range carts.TableParts {
						assertCMSCleanupRowCount(t, deleteCtx, db,
							metadata.TablePartTableName(carts.Name, tp.Name), "parent_id", id, 0)
					}
					if err := db.Upsert(deleteCtx, carts.Name, id,
						map[string]any{"Оформлена": true}, carts); err != nil {
						t.Fatal(err)
					}
					row, err = db.GetByID(deleteCtx, carts.Name, id, carts)
					if err != nil || row["_version"] != expected+1 || !cmsCleanupTrue(row["Оформлена"]) {
						t.Fatalf("позднее обновление не выполнено: row=%#v err=%v", row, err)
					}
					injected = true
				}
				server.EntitySvc().Store = port
				loaded, err := server.catObjectFactory(interpreter.NewStaticCtx(runCtx)).LoadCatalogObject(carts, id.String())
				if err != nil {
					t.Fatal(err)
				}
				obj := loaded.(interface{ CallMethod(string, []any) any })
				if got := obj.CallMethod("УдалитьЕслиНеИзменен", nil); got != false {
					t.Fatalf("поздний конфликт: got=%v, ожидалось false", got)
				}
				if !injected || port.calls != 1 || !errors.Is(port.lastErr, storage.ErrVersionConflict) {
					t.Fatalf("финальный SQL-конфликт не подтверждён: injected=%v calls=%d err=%v", injected, port.calls, port.lastErr)
				}
				assertRestored(runCtx)
				if borrowed {
					assertCMSCleanupRowCount(t, runCtx, db, metadata.TableName(carts.Name), "id", unrelatedID, 1)
					// A new write proves the borrowed transaction is still usable.
					if err := db.Upsert(runCtx, carts.Name, afterConflictID,
						map[string]any{"Наименование": "После конфликта"}, carts); err != nil {
						t.Fatal(err)
					}
					if err := outer.Commit(ctx); err != nil {
						t.Fatalf("commit внешней транзакции: %v", err)
					}
					assertRestored(ctx)
					assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", unrelatedID, 1)
					assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", afterConflictID, 1)
				}
				// Retry through a fresh public read after the rollback (and,
				// for the borrowed case, after committing the outer transaction).
				loaded, err = server.catObjectFactory(interpreter.NewStaticCtx(ctx)).LoadCatalogObject(carts, id.String())
				if err != nil {
					t.Fatal(err)
				}
				obj = loaded.(interface{ CallMethod(string, []any) any })
				obj.CallMethod("Прочитать", nil)
				if got := obj.CallMethod("УдалитьЕслиНеИзменен", nil); got != true {
					t.Fatalf("повтор после нового чтения: got=%v, ожидалось true", got)
				}
				if port.calls != 2 || port.lastErr != nil {
					t.Fatalf("повтор не прошёл финальный SQL: calls=%d err=%v", port.calls, port.lastErr)
				}
				assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", id, 0)
				for _, tp := range carts.TableParts {
					assertCMSCleanupRowCount(t, ctx, db, metadata.TablePartTableName(carts.Name, tp.Name), "parent_id", id, 0)
				}
				if borrowed {
					assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", unrelatedID, 1)
					assertCMSCleanupRowCount(t, ctx, db, metadata.TableName(carts.Name), "id", afterConflictID, 1)
				}
			})
		}
	})
}

type cmsCleanupLateDeleteStore struct {
	*storage.DB
	beforeDelete func(context.Context, string, uuid.UUID, int64)
	calls        int
	lastErr      error
}

func (s *cmsCleanupLateDeleteStore) DeleteVersioned(ctx context.Context, entity string, id uuid.UUID, expected int64) error {
	s.calls++
	s.beforeDelete(ctx, entity, id, expected)
	s.lastErr = s.DB.DeleteVersioned(ctx, entity, id, expected)
	return s.lastErr
}
