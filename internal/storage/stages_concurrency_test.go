// Гонка двух записей одного объекта с этапами (план 121).
//
// Проверка перехода читает старое значение и решает по прочитанному. Если между
// чтением и записью объект успел измениться, решение относится к состоянию,
// которого уже нет: два запроса читают один «Черновик», каждый видит свой
// разрешённый переход — и оба его выполняют. Маршрут раздваивается ровно там,
// где вся ценность в том, что он один.
//
// Тест держит ДВА независимых подключения к одной базе: один SQLite-handle сам
// ограничен SetMaxOpenConns(1) и сериализовал бы всё за нас, показав зелёное там,
// где на самом деле дыра.
package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// stagePair открывает две независимые связи с одной базой на обоих диалектах.
func stagePair(t *testing.T, body func(t *testing.T, a, b *storage.DB)) {
	t.Helper()
	ctx := context.Background()

	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "race.db")
		a, err := storage.ConnectSQLite(ctx, path)
		if err != nil {
			t.Fatalf("ConnectSQLite: %v", err)
		}
		t.Cleanup(a.Close)
		b, err := storage.ConnectSQLite(ctx, path)
		if err != nil {
			t.Fatalf("ConnectSQLite (второе подключение): %v", err)
		}
		t.Cleanup(b.Close)
		body(t, a, b)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("TEST_DATABASE_URL not set")
		}
		schema := storage.NewEphemeralSchemaName()
		a, err := storage.ConnectWithSchema(ctx, dsn, schema)
		if err != nil {
			t.Fatalf("ConnectWithSchema: %v", err)
		}
		if err := a.CreateSchema(ctx, schema); err != nil {
			a.Close()
			t.Fatalf("CreateSchema: %v", err)
		}
		b, err := storage.ConnectWithSchema(ctx, dsn, schema)
		if err != nil {
			a.Close()
			t.Fatalf("ConnectWithSchema (второе подключение): %v", err)
		}
		t.Cleanup(func() {
			b.Close()
			if err := a.DropSchemaCascade(context.Background(), schema); err != nil {
				t.Errorf("DropSchemaCascade(%s): %v", schema, err)
			}
			a.Close()
		})
		body(t, a, b)
	})
}

// TestStagesConcurrentTransitionsSerialize — из одного этапа два параллельных
// перехода не могут выполниться оба.
func TestStagesConcurrentTransitionsSerialize(t *testing.T) {
	stagePair(t, func(t *testing.T, a, b *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		if err := a.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := a.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		// Один шаг вперёд: теперь из «НаСогласовании» объявлены ДВА разных
		// перехода, и параллельно пойдут именно они.
		var v int64 = 1
		if err := a.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "НаСогласовании"), e, &v); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, target := range []string{"Утверждена", "Отклонена"} {
			wg.Add(1)
			go func(i int, target string, db *storage.DB) {
				defer wg.Done()
				<-start
				errs[i] = db.Upsert(ctx, e.Name, id, stageFields("Заявка", target), e)
			}(i, target, []*storage.DB{a, b}[i])
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			}
		}
		if ok != 1 {
			t.Fatalf("успешных записей %d, ожидалась ровно одна: %v", ok, errs)
		}

		// В истории — ровно один переход из «НаСогласовании», и он совпадает с
		// текущим значением объекта.
		hist, err := a.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		fromApproval := 0
		for _, h := range hist {
			if h.FromStage == "НаСогласовании" {
				fromApproval++
			}
		}
		if fromApproval != 1 {
			t.Fatalf("переходов из «НаСогласовании» в истории %d, ожидался 1: %+v", fromApproval, hist)
		}
		row, err := a.GetByID(ctx, e.Name, id, e)
		if err != nil {
			t.Fatal(err)
		}
		if got := row["Состояние"]; got != hist[0].ToStage {
			t.Fatalf("этап объекта %v, последнее событие истории %q — разъехались", got, hist[0].ToStage)
		}
	})
}

// TestStagesConcurrentCreateSerializes — два параллельных создания одного id не
// могут оба записать историю создания.
func TestStagesConcurrentCreateSerializes(t *testing.T) {
	stagePair(t, func(t *testing.T, a, b *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		if err := a.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()

		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, db := range []*storage.DB{a, b} {
			wg.Add(1)
			go func(i int, db *storage.DB) {
				defer wg.Done()
				<-start
				errs[i] = db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e)
			}(i, db)
		}
		close(start)
		wg.Wait()

		// Проверяется инвариант, а не механика. Вторая запись законно
		// заканчивается двумя способами: конфликтом (пришла между чтением и
		// записью первой) или успехом (пришла после её коммита — тогда это
		// обычная запись тех же значений, а не второе создание). Недопустимо
		// одно: чтобы объект получил ДВА события создания.
		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, storage.ErrStageConcurrentWrite), errors.Is(err, storage.ErrVersionConflict):
				// ожидаемый исход проигравшей записи
			default:
				t.Fatalf("неожиданная ошибка параллельного создания: %v", err)
			}
		}
		if ok == 0 {
			t.Fatalf("ни одно создание не прошло: %v", errs)
		}
		hist, err := a.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		creates := 0
		for _, h := range hist {
			if h.FromStage == "" {
				creates++
			}
		}
		if creates != 1 {
			t.Fatalf("событий создания %d, ожидалось 1: %+v", creates, hist)
		}
		if len(hist) != 1 {
			t.Fatalf("записей истории %d, ожидалась 1 (повторная запись тех же значений — не переход): %+v", len(hist), hist)
		}
		if hist[0].EventNo != 1 {
			t.Fatalf("номер первого события %d, ожидался 1", hist[0].EventNo)
		}
	})
}

// TestStagesEventNoIsMonotonic — номера событий идут подряд и задают порядок
// истории; на время опираться нельзя (на PostgreSQL now() зафиксирован на старте
// транзакции, у SQLite datetime('now') секундная точность).
func TestStagesEventNoIsMonotonic(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var v int64 = 1
		for _, stage := range []string{"НаСогласовании", "Отклонена", "Черновик", "НаСогласовании"} {
			if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", stage), e, &v); err != nil {
				t.Fatalf("переход в %s: %v", stage, err)
			}
			v++
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 5 {
			t.Fatalf("событий %d, ожидалось 5: %+v", len(hist), hist)
		}
		// Свежие сверху и без пропусков.
		for i, h := range hist {
			want := int64(len(hist) - i)
			if h.EventNo != want {
				t.Fatalf("событие %d имеет номер %d, ожидался %d", i, h.EventNo, want)
			}
		}
		if hist[0].ToStage != "НаСогласовании" || hist[0].FromStage != "Черновик" {
			t.Fatalf("последнее событие %q → %q", hist[0].FromStage, hist[0].ToStage)
		}
	})
}

// TestStagesStaleVersionIsVersionConflict — устаревшая ревизия остаётся
// конфликтом версий и не превращается в «недопустимый переход»: пользователю
// нужно сказать про ту проблему, которая на самом деле произошла, и ни история,
// ни предупреждение при этом не пишутся.
func TestStagesStaleVersionIsVersionConflict(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var v int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "НаСогласовании"), e, &v); err != nil {
			t.Fatal(err)
		}
		before, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}

		// Та же (уже устаревшая) ревизия и заведомо недопустимый переход.
		stale := int64(1)
		err = db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e, &stale)
		if !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("ожидался конфликт версий, получено: %v", err)
		}
		after, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(before) {
			t.Fatalf("конфликт версий дописал историю: было %d, стало %d", len(before), len(after))
		}
	})
}

// TestStagesWarnViolationIsRecorded — пропущенное режимом warn нарушение
// остаётся в истории помеченным. Молча пропустить его значит потерять
// единственный след: объект уже стоит не там, где должен, и по отчёту это надо
// увидеть.
func TestStagesWarnViolationIsRecorded(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceWarn)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var v int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "Утверждена"), e, &v); err != nil {
			t.Fatalf("warn обязан пропустить: %v", err)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("событий %d, ожидалось 2", len(hist))
		}
		if !hist[0].Violation {
			t.Fatal("нарушение, пропущенное режимом warn, не помечено в истории")
		}
		if hist[1].Violation {
			t.Fatal("обычное создание помечено нарушением")
		}
	})
}

// TestReadSnapshotIsolatesConcurrentWrite — выгрузка резервной копии читает
// много таблиц подряд, и без общего снимка объект успевал попасть в архив до
// перехода на следующий этап, а его история — уже после: восстановленная база
// показывала состояние, которого никогда не было.
//
// Проверяется сам механизм: запись, сделанная другим подключением во время
// снимка, внутри него не видна.
func TestReadSnapshotIsolatesConcurrentWrite(t *testing.T) {
	stagePair(t, func(t *testing.T, a, b *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		if err := a.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := a.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}

		err := a.WithReadSnapshot(ctx, func(snapCtx context.Context) error {
			// Первое чтение фиксирует снимок.
			before, err := a.GetByID(snapCtx, e.Name, id, e)
			if err != nil {
				return err
			}
			if before["Состояние"] != "Черновик" {
				t.Fatalf("до записи в снимке %v", before["Состояние"])
			}

			// Другое подключение двигает объект по маршруту и коммитит.
			var v int64 = 1
			if err := b.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "НаСогласовании"), e, &v); err != nil {
				t.Fatalf("параллельная запись: %v", err)
			}

			// Снимок обязан остаться прежним — и по объекту, и по истории.
			after, err := a.GetByID(snapCtx, e.Name, id, e)
			if err != nil {
				return err
			}
			if after["Состояние"] != "Черновик" {
				t.Fatalf("снимок увидел чужую запись: %v", after["Состояние"])
			}
			hist, err := a.StageHistory(snapCtx, e.Name, id)
			if err != nil {
				return err
			}
			if len(hist) != 1 {
				t.Fatalf("в снимке %d событий истории, ожидалось 1: %+v", len(hist), hist)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WithReadSnapshot: %v", err)
		}

		// После снимка видно уже новое состояние.
		row, err := a.GetByID(ctx, e.Name, id, e)
		if err != nil {
			t.Fatal(err)
		}
		if row["Состояние"] != "НаСогласовании" {
			t.Fatalf("после снимка этап %v", row["Состояние"])
		}
	})
}
