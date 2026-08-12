package entityservice

// Этапы (план 121) на пути entityservice.Save — том, которым идут форма и REST.
// Проверка через публичную точку входа, а не вызовом гейта напрямую: повод —
// issue #611, где проверка была написана, покрыта тестом и зелена, а в продовом
// пути не вызывалась вовсе.
//
// Правка существующего объекта здесь обязательна отдельным случаем: она идёт
// через UpsertVersioned (вторая точка записи storage), и тест только на
// создание пропуск гейта в ней не заметил бы.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func stagesDoc() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Комментарий", Type: metadata.FieldTypeString},
			{Name: "Состояние", Type: "enum:СостояниеЗаявки", EnumName: "СостояниеЗаявки"},
		},
		Stages: &metadata.Stages{
			Field: "Состояние",
			Order: []string{"Черновик", "НаСогласовании", "Утверждена"},
			Transitions: []metadata.StageTransition{
				{From: "Черновик", To: []string{"НаСогласовании"}},
				{From: "НаСогласовании", To: []string{"Утверждена"}},
			},
			Enforce: metadata.StageEnforceStrict,
		},
	}
}

func stagesService(t *testing.T, db *storage.DB, e *metadata.Entity, programs map[string]*ast.Program) *Service {
	t.Helper()
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{e}, Programs: programs})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	return &Service{Store: db, Reg: registry, Interp: interp}
}

func TestStagesGateOnEntityServiceSaveMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := storage.WithAuditUser(context.Background(), uuid.NewString(), "petrov")
		e := stagesDoc()
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		svc := stagesService(t, db, e, nil)

		// Создание сразу на «Утверждена» — отказ.
		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, IsNew: true,
			Fields: map[string]any{"Состояние": "Утверждена"}}); err == nil {
			t.Fatal("создание документа сразу на «Утверждена» прошло мимо гейта")
		}

		// Создание на начальном этапе.
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, IsNew: true,
			Fields: map[string]any{"Состояние": "Черновик"}}); err != nil {
			t.Fatalf("создание на начальном этапе: %v", err)
		}

		// Правка существующего объекта БЕЗ проверки версии — путь Upsert.
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id,
			Fields: map[string]any{"Состояние": "Утверждена"}}); err == nil {
			t.Fatal("перескок этапа при правке (без версии) прошёл мимо гейта")
		}

		// Правка существующего объекта С проверкой версии — путь UpsertVersioned.
		var version int64 = 1
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, ExpectedVersion: &version,
			Fields: map[string]any{"Состояние": "Утверждена"}}); err == nil {
			t.Fatal("перескок этапа при правке (с версией) прошёл мимо гейта — вторая точка записи не закрыта")
		}
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, ExpectedVersion: &version,
			Fields: map[string]any{"Состояние": "НаСогласовании"}}); err != nil {
			t.Fatalf("разрешённый переход через UpsertVersioned: %v", err)
		}

		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("история: %d записей, ожидалось 2 (создание + переход): %+v", len(hist), hist)
		}
		if hist[0].ToStage != "НаСогласовании" || hist[0].UserLogin != "petrov" {
			t.Fatalf("последний переход: %+v", hist[0])
		}
	})
}

// TestStagesHookCreatingObjectWritesSingleHistoryMatrix — создание объекта с
// хуком ПриЗаписи идёт двумя записями (провизорная вставка + финальная), и
// история обязана получить ровно одну строку создания, а не две и не ноль.
func TestStagesHookCreatingObjectWritesSingleHistoryMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesDoc()
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		program := mustParseProgramT(t, `
Процедура ПриЗаписи()
  ЭтотОбъект.Комментарий = "из хука";
КонецПроцедуры`)
		svc := stagesService(t, db, e, map[string]*ast.Program{e.Name: program})

		id := uuid.New()
		res, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, IsNew: true,
			Fields: map[string]any{"Состояние": "Черновик"}})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if res.DSLError != "" {
			t.Fatalf("DSLError: %s", res.DSLError)
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 1 {
			t.Fatalf("создание с хуком дало %d записей истории, ожидалась 1: %+v", len(hist), hist)
		}
		if hist[0].FromStage != "" || hist[0].ToStage != "Черновик" {
			t.Fatalf("запись создания: %q → %q", hist[0].FromStage, hist[0].ToStage)
		}
	})
}

// TestStagesPostingDoesNotTouchStageMatrix — проведение документа меняет только
// признак проведения, этап оно не трогает. Ни гейт не должен срабатывать, ни
// история пополняться: иначе документ с strict-маршрутом становится
// непроводимым, а в отчёте «где застряло» появляется переход из ниоткуда.
func TestStagesPostingDoesNotTouchStageMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesDoc()
		e.Posting = true
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		program := mustParseProgramT(t, `
Процедура ОбработкаПроведения()
  ЭтотОбъект.Комментарий = "проведено";
КонецПроцедуры`)
		svc := stagesService(t, db, e, map[string]*ast.Program{e.Name: program})

		id := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, IsNew: true,
			Fields: map[string]any{"Состояние": "Черновик"}}); err != nil {
			t.Fatalf("создание: %v", err)
		}
		var version int64 = 1
		if _, err := svc.Save(ctx, SaveRequest{Entity: e, ID: id, ExpectedVersion: &version,
			Action: "post", Fields: map[string]any{"Состояние": "Черновик"}}); err != nil {
			t.Fatalf("проведение документа с этапами: %v", err)
		}

		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 1 {
			t.Fatalf("после проведения записей истории %d, ожидалась 1 (только создание): %+v", len(hist), hist)
		}
	})
}
