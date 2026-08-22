package ui

// Этапы (план 121) на DSL-пути «Документы.X» — он идёт мимо entityservice и
// имеет собственные вызовы storage. Проверка через публичную точку входа
// (Создать/Записать/ПолучитьОбъект/Провести), а не вызовом гейта напрямую.

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func stagesDSLDoc(posting bool) *metadata.Entity {
	return &metadata.Entity{
		Name:    "Заявка",
		Kind:    metadata.KindDocument,
		Posting: posting,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
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

func stagesDSLServer(t *testing.T, db *storage.DB, e *metadata.Entity, programs map[string]*ast.Program) *Server {
	t.Helper()
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{e}, Programs: programs})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	srv := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	// Создать() ходит в entityservice (дефолты и ПриСозданииНового, план 153).
	srv.entitySvc = srv.newEntityService(nil)
	return srv
}

// stagesDSLCall исполняет вызов DSL-объекта и превращает RaiseUserError
// (интерпретатор сообщает об ошибках паникой) в обычную ошибку.
func stagesDSLCall(t *testing.T, fn func()) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			err = errFromPanic(r)
		}
	}()
	fn()
	return nil
}

// TestStagesGateOnDSLWritePathMatrix — гейт закрывает и создание документа из
// DSL, и правку существующего объекта (ПолучитьОбъект выставляет версию, и
// запись идёт через UpsertVersioned — вторую точку записи storage).
func TestStagesGateOnDSLWritePathMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesDSLDoc(false)
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		s := stagesDSLServer(t, db, e, nil)
		dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(e.Name).(*docProxy)

		// Создание сразу на «Утверждена» — отказ.
		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", "З-001")
		w.Set("Состояние", "Утверждена")
		if err := stagesDSLCall(t, func() { w.CallMethod("записать", nil) }); err == nil {
			t.Fatal("создание документа из DSL сразу на «Утверждена» прошло мимо гейта")
		}

		// Создание на начальном этапе проходит.
		w2 := dp.CallMethod("создать", nil).(*docWriter)
		w2.Set("Номер", "З-002")
		w2.Set("Состояние", "Черновик")
		if err := stagesDSLCall(t, func() { w2.CallMethod("записать", nil) }); err != nil {
			t.Fatalf("создание на начальном этапе: %v", err)
		}
		id := w2.obj.ID

		ref, ok := dp.CallMethod("найтипономеру", []any{"З-002"}).(*interpreter.Ref)
		if !ok {
			t.Fatal("НайтиПоНомеру не вернул ссылку")
		}

		// Правка существующего объекта: перескок этапа отвергнут.
		w3 := ref.CallMethod("получитьобъект", nil).(*docWriter)
		w3.Set("Состояние", "Утверждена")
		if err := stagesDSLCall(t, func() { w3.CallMethod("записать", nil) }); err == nil {
			t.Fatal("перескок этапа при правке из DSL прошёл мимо гейта — вторая точка записи не закрыта")
		}

		// Разрешённый переход проходит.
		w4 := ref.CallMethod("получитьобъект", nil).(*docWriter)
		w4.Set("Состояние", "НаСогласовании")
		if err := stagesDSLCall(t, func() { w4.CallMethod("записать", nil) }); err != nil {
			t.Fatalf("разрешённый переход из DSL: %v", err)
		}

		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("история DSL-записи: %d, ожидалось 2 (создание + переход): %+v", len(hist), hist)
		}
		if hist[0].FromStage != "Черновик" || hist[0].ToStage != "НаСогласовании" {
			t.Fatalf("последний переход %q → %q", hist[0].FromStage, hist[0].ToStage)
		}
	})
}

// TestStagesDSLPostingKeepsStageMatrix — Провести() из DSL дописывает реквизиты
// после ОбработкаПроведения отдельной записью (UpsertPreserveVersion). Этап она
// не меняет, поэтому гейт обязан молчать, а история — не пополняться.
//
// Ровно здесь ломается «наивный» вариант, в котором финальная запись созданного
// объекта считается созданием: документ, доехавший до «НаСогласовании»,
// переставал проводиться — «создать сразу на этом этапе нельзя».
func TestStagesDSLPostingKeepsStageMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := stagesDSLDoc(true)
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		prog := mustParse(t, `Процедура ОбработкаПроведения()
  ЭтотОбъект.Номер = ЭтотОбъект.Номер;
КонецПроцедуры`)
		s := stagesDSLServer(t, db, e, map[string]*ast.Program{e.Name: prog})
		dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(e.Name).(*docProxy)

		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", "З-010")
		w.Set("Состояние", "Черновик")
		if err := stagesDSLCall(t, func() { w.CallMethod("записать", nil) }); err != nil {
			t.Fatalf("создание: %v", err)
		}
		id := w.obj.ID

		w.Set("Состояние", "НаСогласовании")
		if err := stagesDSLCall(t, func() { w.CallMethod("записать", nil) }); err != nil {
			t.Fatalf("переход перед проведением: %v", err)
		}
		if err := stagesDSLCall(t, func() { w.CallMethod("провести", nil) }); err != nil {
			t.Fatalf("проведение документа на этапе «НаСогласовании»: %v", err)
		}

		var posted bool
		if err := db.QueryRow(ctx, "SELECT posted FROM заявка WHERE id = "+db.Dialect().Placeholder(1), id.String()).Scan(&posted); err != nil {
			t.Fatal(err)
		}
		if !posted {
			t.Fatal("документ не проведён")
		}
		hist, err := db.StageHistory(ctx, e.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 {
			t.Fatalf("после проведения записей истории %d, ожидалось 2 (создание + переход): %+v", len(hist), hist)
		}
	})
}
