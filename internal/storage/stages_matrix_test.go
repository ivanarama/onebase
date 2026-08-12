// Матричные тесты этапов (план 121): гейт переходов, история и отчёт «где
// застряло» обязаны вести себя одинаково на SQLite и PostgreSQL — трогаем
// семантику SQL, а раздельные тесты расхождений диалектов не показывают
// (CLAUDE.md, план 115).
//
// Пакет внешний (storage_test), потому что internal/dbtest импортирует storage:
// из package storage такой импорт был бы циклом.
package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// stagesEntity — справочник «Заявка» с маршрутом Черновик → НаСогласовании →
// Утверждена | Отклонена → Черновик.
func stagesEntity(enforce string) *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Состояние", Type: "enum:СостояниеЗаявки", EnumName: "СостояниеЗаявки"},
		},
		Stages: &metadata.Stages{
			Field: "Состояние",
			Order: []string{"Черновик", "НаСогласовании", "Утверждена", "Отклонена"},
			Transitions: []metadata.StageTransition{
				{From: "Черновик", To: []string{"НаСогласовании"}},
				{From: "НаСогласовании", To: []string{"Утверждена", "Отклонена"}},
				{From: "Отклонена", To: []string{"Черновик"}},
			},
			DeadlineDays: map[string]int{"НаСогласовании": 2},
			Enforce:      enforce,
		},
	}
}

func stageFields(name, stage string) map[string]any {
	return map[string]any{"Наименование": name, "Состояние": stage}
}

func migrateStages(t *testing.T, ctx context.Context, db *storage.DB, entities ...*metadata.Entity) {
	t.Helper()
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

// TestStagesGateBlocksSkipOnBothWritePaths — центральная проверка плана: гейт
// стоит в ОБЕИХ точках записи storage. Первая (upsert) отвечает за создание,
// вторая (UpsertVersioned) — за все правки существующего объекта из формы, REST
// и DSL. Тест только на создание пропуск гейта во второй не заметил бы.
func TestStagesGateBlocksSkipOnBothWritePaths(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		// Точка записи №1 — upsert: создание сразу на «Утверждена» отвергнуто.
		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка 1", "Утверждена"), e); err == nil {
			t.Fatal("создание сразу на «Утверждена» прошло — гейт в upsert не сработал")
		}

		// Нормальное создание на начальном этапе.
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка 1", "Черновик"), e); err != nil {
			t.Fatalf("создание на начальном этапе: %v", err)
		}

		// Точка записи №2 — UpsertVersioned: перескок через этап отвергнут.
		var version int64 = 1
		err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка 1", "Утверждена"), e, &version)
		if err == nil {
			t.Fatal("перескок «Черновик» → «Утверждена» прошёл — гейт в UpsertVersioned не сработал")
		}
		if errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("ожидался отказ по этапу, а не конфликт версий: %v", err)
		}

		// Значение в базе не изменилось: отказ произошёл ДО записи.
		row, err := db.GetByID(ctx, e.Name, id, e)
		if err != nil {
			t.Fatal(err)
		}
		if got := row["Состояние"]; got != "Черновик" {
			t.Fatalf("после отказа этап = %v, ожидался «Черновик»", got)
		}

		// Разрешённый переход проходит через ту же точку записи.
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка 1", "НаСогласовании"), e, &version); err != nil {
			t.Fatalf("разрешённый переход: %v", err)
		}
	})
}

// TestStagesEmptyStageMeansStartOfRoute — объект с незаполненным этапом стоит в
// начале маршрута: шаг из пустого значения разрешён ровно там же, где он
// разрешён из начального этапа.
//
// Так устроено большинство существующих конфигураций: реквизит «Состояние» у
// заявки заполняется не при создании, а командой «Отправить на согласование».
// Считать пустое значение отдельным состоянием значило бы объявить этот путь
// нарушением, а разрешить из пустого что угодно — оставить дыру, ради закрытия
// которой гейт и написан.
func TestStagesEmptyStageMeansStartOfRoute(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		// Создание с незаполненным этапом — не переход, гейт молчит.
		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Без этапа", ""), e); err != nil {
			t.Fatalf("создание с пустым этапом: %v", err)
		}
		if hist, err := db.StageHistory(ctx, e.Name, id); err != nil {
			t.Fatal(err)
		} else if len(hist) != 0 {
			t.Fatalf("пустой этап записан в историю: %+v", hist)
		}

		// Шаг из пустого значения по объявленному переходу «Черновик → …».
		var version int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Без этапа", "НаСогласовании"), e, &version); err != nil {
			t.Fatalf("«пусто» → «НаСогласовании» отвергнуто, хотя переход из начального этапа объявлен: %v", err)
		}

		// А перескок из пустого значения по-прежнему отвергается.
		id2 := uuid.New()
		if err := db.Upsert(ctx, e.Name, id2, stageFields("Без этапа 2", ""), e); err != nil {
			t.Fatal(err)
		}
		var v2 int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id2, stageFields("Без этапа 2", "Утверждена"), e, &v2); err == nil {
			t.Fatal("«пусто» → «Утверждена» прошло: маршрут можно перескочить, не заполняя этап")
		}
	})
}

// TestStagesHistoryRecordsWhoAndWhen — история переходов пишется на обеих точках
// записи и хранит «из какого в какой», актора и момент.
func TestStagesHistoryRecordsWhoAndWhen(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := storage.WithAuditUser(context.Background(), uuid.NewString(), "ivanov")
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var version int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "НаСогласовании"), e, &version); err != nil {
			t.Fatal(err)
		}

		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatalf("StageHistory: %v", err)
		}
		if len(hist) != 2 {
			t.Fatalf("записей истории %d, ожидалось 2: %+v", len(hist), hist)
		}
		// Свежие сверху.
		if hist[0].FromStage != "Черновик" || hist[0].ToStage != "НаСогласовании" {
			t.Fatalf("последний переход %q → %q, ожидался «Черновик» → «НаСогласовании»", hist[0].FromStage, hist[0].ToStage)
		}
		if hist[1].FromStage != "" || hist[1].ToStage != "Черновик" {
			t.Fatalf("создание записано как %q → %q, ожидалось «» → «Черновик»", hist[1].FromStage, hist[1].ToStage)
		}
		for _, h := range hist {
			if h.UserLogin != "ivanov" {
				t.Fatalf("актор перехода %q, ожидался ivanov", h.UserLogin)
			}
			if h.At.IsZero() {
				t.Fatal("момент перехода не записан")
			}
			if h.Field != "Состояние" {
				t.Fatalf("поле перехода %q, ожидалось «Состояние»", h.Field)
			}
			if h.Source != "" {
				t.Fatalf("источник %q, ожидалась обычная запись", h.Source)
			}
		}
	})
}

// TestStagesHistoryWrittenWithAuditDisabled — история не зависит от журнала
// регистрации. Именно поэтому она живёт в своей таблице: аудит выключается
// настройкой и тогда молча ничего не пишет, а «где застряло» обязано работать.
func TestStagesHistoryWrittenWithAuditDisabled(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceWarn)
		migrateStages(t, ctx, db, e)
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveAuditSettings(ctx, storage.AuditSettings{}); err != nil {
			t.Fatalf("SaveAuditSettings: %v", err)
		}

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var version int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "НаСогласовании"), e, &version); err != nil {
			t.Fatal(err)
		}

		var auditRows int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _audit`).Scan(&auditRows); err != nil {
			t.Fatal(err)
		}
		if auditRows != 0 {
			t.Fatalf("журнал регистрации выключен, а записей %d", auditRows)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("история переходов при выключенном аудите: %d записей, ожидалось 2", len(hist))
		}
	})
}

// TestStagesWarnAllowsAndStrictRejects — умолчание warn пропускает нарушение
// (и всё равно записывает его в историю: там то, что произошло, а не то, что
// разрешено), strict отвергает.
func TestStagesWarnAllowsAndStrictRejects(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		warn := stagesEntity(metadata.StageEnforceWarn)
		migrateStages(t, ctx, db, warn)

		id := uuid.New()
		if err := db.Upsert(ctx, warn.Name, id, stageFields("Заявка", "Черновик"), warn); err != nil {
			t.Fatal(err)
		}
		var version int64 = 1
		if err := db.UpsertVersioned(ctx, warn.Name, id, stageFields("Заявка", "Утверждена"), warn, &version); err != nil {
			t.Fatalf("warn обязан пропустить нарушение: %v", err)
		}
		hist, err := db.StageHistory(ctx, warn.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 || hist[0].ToStage != "Утверждена" {
			t.Fatalf("warn не записал состоявшийся переход в историю: %+v", hist)
		}

		// Та же конфигурация с enforce: strict — отказ.
		strict := stagesEntity(metadata.StageEnforceStrict)
		id2 := uuid.New()
		if err := db.Upsert(ctx, strict.Name, id2, stageFields("Заявка 2", "Черновик"), strict); err != nil {
			t.Fatal(err)
		}
		var v2 int64 = 1
		if err := db.UpsertVersioned(ctx, strict.Name, id2, stageFields("Заявка 2", "Утверждена"), strict, &v2); err == nil {
			t.Fatal("strict обязан отвергнуть перескок этапа")
		}
	})
}

// TestStagesHistoryRollsBackWithTransaction — история в той же транзакции, что
// и запись: откат записи откатывает и историю, иначе отчёт показывал бы
// переходы объектов, которых нет.
func TestStagesHistoryRollsBackWithTransaction(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		boom := errors.New("откат")
		err := db.WithTx(ctx, func(txCtx context.Context) error {
			if err := db.Upsert(txCtx, e.Name, id, stageFields("Заявка", "Черновик"), e); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("ожидался откат по %v, получено %v", boom, err)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 0 {
			t.Fatalf("после отката осталось %d записей истории", len(hist))
		}
	})
}

// TestStagesUntouchedEntityBehavesAsBefore — конфигурация без блока `stages`
// ведёт себя ровно как раньше: ни гейта, ни истории.
func TestStagesUntouchedEntityBehavesAsBefore(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		e.Stages = nil
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if err := db.Upsert(ctx, e.Name, id, stageFields("Заявка", "Утверждена"), e); err != nil {
			t.Fatalf("без stages запись обязана проходить как прежде: %v", err)
		}
		var version int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, id, stageFields("Заявка", "Черновик"), e, &version); err != nil {
			t.Fatalf("без stages правка обязана проходить как прежде: %v", err)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 0 {
			t.Fatalf("без stages история не пишется, а записей %d", len(hist))
		}
	})
}

// TestStagesCatalogWritePathGoesThroughGate — четвёртый путь записи: запись
// справочника из DSL (Справочники.X.Записать) идёт через WriteCatalogRecord.
// Проверяется публичной точкой входа, а не вызовом приватной проверки.
func TestStagesCatalogWritePathGoesThroughGate(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		id := uuid.New()
		if _, err := db.WriteCatalogRecord(ctx, e, id.String(), stageFields("Заявка", "Утверждена")); err == nil {
			t.Fatal("создание справочника сразу на «Утверждена» прошло мимо гейта")
		}
		if _, err := db.WriteCatalogRecord(ctx, e, id.String(), stageFields("Заявка", "Черновик")); err != nil {
			t.Fatalf("создание на начальном этапе: %v", err)
		}
		if _, err := db.WriteCatalogRecord(ctx, e, id.String(), stageFields("Заявка", "Утверждена")); err == nil {
			t.Fatal("перескок этапа при правке справочника прошёл мимо гейта")
		}
		if _, err := db.WriteCatalogRecord(ctx, e, id.String(), stageFields("Заявка", "НаСогласовании")); err != nil {
			t.Fatalf("разрешённый переход при правке справочника: %v", err)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("история записи справочника: %d, ожидалось 2", len(hist))
		}
	})
}

// TestStagesExternalWriteBypassesGate — обмен данными (план 86) пишет объект,
// прошедший маршрут в чужой базе. Гейт его пропускает, но история фиксирует
// источник, иначе разбирать «откуда это взялось» было бы нечем.
func TestStagesExternalWriteBypassesGate(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceStrict)
		migrateStages(t, ctx, db, e)

		extCtx := storage.WithExternalStageWrite(ctx, storage.StageSourceExchange)
		id := uuid.New()
		if err := db.Upsert(extCtx, e.Name, id, stageFields("Заявка", "Утверждена"), e); err != nil {
			t.Fatalf("внешняя запись обязана проходить гейт: %v", err)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 1 || hist[0].Source != storage.StageSourceExchange {
			t.Fatalf("история внешней записи: %+v", hist)
		}
	})
}

// TestStageSummaryCountsStuckObjects — отчёт «где застряло»: сколько объектов на
// этапе, сколько просрочено и сколько таких, по которым история неизвестна.
func TestStageSummaryCountsStuckObjects(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesEntity(metadata.StageEnforceWarn)
		migrateStages(t, ctx, db, e)

		// Объект дошёл до «НаСогласовании» штатным путём только что — не просрочен.
		fresh := uuid.New()
		if err := db.Upsert(ctx, e.Name, fresh, stageFields("Заявка", "Черновик"), e); err != nil {
			t.Fatal(err)
		}
		var v int64 = 1
		if err := db.UpsertVersioned(ctx, e.Name, fresh, stageFields("Заявка", "НаСогласовании"), e, &v); err != nil {
			t.Fatal(err)
		}

		// Запись объектов без гейта и без истории — так в базе выглядят данные,
		// накопленные до объявления блока `stages`.
		noStages := stagesEntity(metadata.StageEnforceWarn)
		noStages.Stages = nil

		// Висит на этапе 10 дней при сроке 2 — просрочка.
		stale := uuid.New()
		if err := db.Upsert(ctx, noStages.Name, stale, stageFields("Долгая заявка", "НаСогласовании"), noStages); err != nil {
			t.Fatal(err)
		}
		if err := db.LogStageChange(ctx, &storage.StageChange{
			EntityName: e.Name,
			RecordID:   stale.String(),
			Field:      "Состояние",
			FromStage:  "Черновик",
			ToStage:    "НаСогласовании",
			At:         time.Now().UTC().AddDate(0, 0, -10),
		}); err != nil {
			t.Fatal(err)
		}

		// Объект «старше этапов»: значение есть, истории нет — время на этапе
		// неизвестно, и просрочкой его считать нельзя.
		legacy := uuid.New()
		if err := db.Upsert(ctx, noStages.Name, legacy, stageFields("Старая заявка", "НаСогласовании"), noStages); err != nil {
			t.Fatal(err)
		}

		buckets, err := db.StageSummary(ctx, e, nil)
		if err != nil {
			t.Fatalf("StageSummary: %v", err)
		}
		if len(buckets) != len(e.Stages.Order) {
			t.Fatalf("строк отчёта %d, ожидалось %d (по строке на этап)", len(buckets), len(e.Stages.Order))
		}
		var b storage.StageBucket
		for _, x := range buckets {
			if x.Stage == "НаСогласовании" {
				b = x
			}
		}
		if b.Count != 3 {
			t.Fatalf("на этапе «НаСогласовании» %d объектов, ожидалось 3", b.Count)
		}
		if b.Unknown != 1 {
			t.Fatalf("объектов без истории %d, ожидался 1 (созданный до объявления этапов)", b.Unknown)
		}
		if b.DeadlineDays != 2 {
			t.Fatalf("срок этапа %d, ожидалось 2", b.DeadlineDays)
		}
		if b.Overdue != 1 {
			t.Fatalf("просрочено %d, ожидался 1 (висит 10 дней при сроке 2)", b.Overdue)
		}
		if b.Since.IsZero() {
			t.Fatal("момент самого давнего перехода не определён")
		}
		// Пустой этап присутствует в отчёте нулевой строкой, а не пропадает.
		for _, x := range buckets {
			if x.Stage == "Отклонена" && x.Count != 0 {
				t.Fatalf("на «Отклонена» %d объектов, ожидался 0", x.Count)
			}
		}
	})
}
