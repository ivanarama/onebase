package ui

// Методы табличной части в обработчике проведения (issue #842).
//
// `this.Товары.Количество()` падал: табличная часть приезжала сырым срезом, у
// которого методов нет вовсе. При этом `Для Каждого … Из this.Товары` работал,
// поэтому поломка выглядела выборочной, а жила долго — до #718 вызов
// несуществующего метода молча возвращал «Неопределено», и типовая проверка
// «если строк нет — исключение» просто ничего не делала.
//
// Ключевое: путей к хуку ДВА, и `this` у них разный —
//
//	entityservice.Save          → обёртка формы (formTpProxy, методы были);
//	Документы.X.Провести()      → сырой runtime.Object (методов не было).
//
// Поэтому каждый тест гоняет ОБА пути: расхождение между ними и есть дефект,
// и проверка одного пути его бы не увидела.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// tpMethodsServer поднимает документ с ТЧ и модулем проведения из src.
func tpMethodsServer(t *testing.T, src string) (context.Context, *Server, *metadata.Entity, *metadata.Register) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "tp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		}}},
	}
	reg := &metadata.Register{
		Name:       "Продажи",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{"Реализация": mustParse(t, src)},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store: db, Reg: registry, Interp: interp,
		PrepareHook: s.enrichHeaderRefs, EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars: s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	return ctx, s, doc, reg
}

// postViaDSL проводит документ путём `Документы.X.Создать().Провести()` —
// здесь this — сырой runtime.Object.
func postViaDSL(t *testing.T, ctx context.Context, s *Server, rows []map[string]any) error {
	t.Helper()
	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("Реализация").(*docProxy)
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = dslPanicToError(r)
			}
		}()
		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", "РТ-DSL")
		tp := w.Get("Товары").(*tpProxy)
		for _, r := range rows {
			row := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
			for k, v := range r {
				row.Set(k, v)
			}
		}
		w.CallMethod("провести", nil)
	}()
	return runErr
}

// postViaService проводит документ через entityservice.Save — здесь this
// оборачивается формовой обёрткой.
// Ошибка ДЕЛОВАЯ (исключение из модуля) приезжает в SaveResult.DSLError, а не
// в err: err — это техническая ошибка. Тест обязан смотреть оба.
func postViaService(t *testing.T, ctx context.Context, s *Server, doc *metadata.Entity, rows []map[string]any) error {
	t.Helper()
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: uuid.New(), IsNew: true,
		Fields:        map[string]any{"Номер": "РТ-SVC"},
		TablePartRows: map[string][]map[string]any{"Товары": rows},
		Action:        "post",
	})
	if err != nil {
		return err
	}
	if res.DSLError != "" {
		return errors.New(res.DSLError)
	}
	return nil
}

// dslPanicToError разворачивает панику интерпретатора в ошибку с ИСХОДНЫМ
// текстом: тип userError не экспортирован, но его строковое представление
// содержит сообщение — а именно оно и проверяется.
func dslPanicToError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

// movementSums читает движения прямо из таблицы регистра: number на SQLite
// хранится TEXT, поэтому суммируем через CAST (грабли из AGENTS.md).
func movementSums(t *testing.T, ctx context.Context, s *Server) (count, sum float64) {
	t.Helper()
	if err := s.store.QueryRow(ctx,
		`SELECT COALESCE(SUM(CAST(количество AS NUMERIC)), 0), COALESCE(SUM(CAST(сумма AS NUMERIC)), 0) FROM рег_продажи`).
		Scan(&count, &sum); err != nil {
		t.Fatalf("чтение движений: %v", err)
	}
	return count, sum
}

func asFloatValue(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f); err == nil {
			return f
		}
	}
	if str, ok := v.(interface{ String() string }); ok {
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(str.String()), "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

// Количество() и Итог() работают на ОБОИХ путях проведения и дают одно и то же.
func TestTablePartMethods_CountAndTotalOnBothPostPaths(t *testing.T) {
	src := `Процедура ОбработкаПроведения()
  Если ЭтотОбъект.Товары.Количество() = 0 Тогда
    ВызватьИсключение("Табличная часть не заполнена");
  КонецЕсли;
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Сумма = Стр.Количество * Стр.Цена;
  КонецЦикла;
  Дв = Движения.Продажи.Добавить();
  Дв.ВидДвижения = "Приход";
  Дв.Номенклатура = "итого";
  Дв.Количество = ЭтотОбъект.Товары.Количество();
  Дв.Сумма = ЭтотОбъект.Товары.Итог("Сумма");
КонецПроцедуры`

	rows := func() []map[string]any {
		return []map[string]any{
			{"Номенклатура": "Доска", "Количество": float64(2), "Цена": float64(100)},
			{"Номенклатура": "Брус", "Количество": float64(3), "Цена": float64(200)},
		}
	}

	t.Run("Документы.X.Провести", func(t *testing.T) {
		ctx, s, _, _ := tpMethodsServer(t, src)
		if err := postViaDSL(t, ctx, s, rows()); err != nil {
			t.Fatalf("проведение через DSL: %v", err)
		}
		count, sum := movementSums(t, ctx, s)
		if count != 2 || sum != 800 {
			t.Errorf("движения: количество=%v сумма=%v, ожидались 2 и 800", count, sum)
		}
	})

	t.Run("entityservice.Save", func(t *testing.T) {
		ctx, s, doc, _ := tpMethodsServer(t, src)
		if err := postViaService(t, ctx, s, doc, rows()); err != nil {
			t.Fatalf("проведение через сервис: %v", err)
		}
		count, sum := movementSums(t, ctx, s)
		if count != 2 || sum != 800 {
			t.Errorf("движения: количество=%v сумма=%v, ожидались 2 и 800", count, sum)
		}
	})
}

// Пустая табличная часть: проверка «строк нет» обязана срабатывать. Раньше
// Количество() возвращало Неопределено, сравнение с нулём давало Ложь, и
// документ проводился с пустой ТЧ, будто проверки нет.
func TestTablePartMethods_EmptyIsCaughtOnBothPostPaths(t *testing.T) {
	src := `Процедура ОбработкаПроведения()
  Если ЭтотОбъект.Товары.Количество() = 0 Тогда
    ВызватьИсключение("Табличная часть «Товары» не заполнена — проводить нечего.");
  КонецЕсли;
КонецПроцедуры`

	t.Run("Документы.X.Провести", func(t *testing.T) {
		ctx, s, _, _ := tpMethodsServer(t, src)
		err := postViaDSL(t, ctx, s, nil)
		if err == nil || !strings.Contains(err.Error(), "не заполнена") {
			t.Fatalf("проведение с пустой ТЧ не отклонено: %v", err)
		}
	})

	t.Run("entityservice.Save", func(t *testing.T) {
		ctx, s, doc, _ := tpMethodsServer(t, src)
		err := postViaService(t, ctx, s, doc, nil)
		if err == nil || !strings.Contains(err.Error(), "не заполнена") {
			t.Fatalf("проведение с пустой ТЧ не отклонено: %v", err)
		}
	})
}

// Правки строк в цикле обязаны доезжать до записи: обёртка адресует те же map,
// а не копию. Копия молча теряла бы расчётные реквизиты.
func TestTablePartMethods_RowEditsPersist(t *testing.T) {
	src := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Сумма = Стр.Количество * Стр.Цена;
  КонецЦикла;
КонецПроцедуры`
	ctx, s, doc, _ := tpMethodsServer(t, src)
	id := uuid.New()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: id, IsNew: true,
		Fields: map[string]any{"Номер": "РТ-1"},
		TablePartRows: map[string][]map[string]any{"Товары": {
			{"Номенклатура": "Доска", "Количество": float64(2), "Цена": float64(100)},
		}},
		Action: "post",
	}); err != nil {
		t.Fatalf("проведение: %v", err)
	}
	rows, err := s.store.GetTablePartRows(ctx, doc.Name, "Товары", id, doc.TableParts[0])
	if err != nil {
		t.Fatalf("чтение ТЧ: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("строк ТЧ = %d, ожидалась 1", len(rows))
	}
	if got := asFloatValue(rows[0]["Сумма"]); got != 200 {
		t.Errorf("Сумма после проведения = %v, ожидалось 200 — правка строки не доехала", got)
	}
}

// Опечатка в имени метода — ошибка со списком доступных, а не тихое
// «Неопределено». Именно тишина позволила #842 прожить незамеченным.
func TestTablePartMethods_UnknownMethodExplains(t *testing.T) {
	src := `Процедура ОбработкаПроведения()
  Ч = ЭтотОбъект.Товары.КоличествоСтрок();
КонецПроцедуры`
	ctx, s, doc, _ := tpMethodsServer(t, src)

	err := postViaDSL(t, ctx, s, []map[string]any{{"Номенклатура": "Доска", "Количество": float64(1)}})
	if err == nil {
		t.Fatal("опечатка в имени метода прошла молча")
	}
	for _, want := range []string{"КоличествоСтрок", "Количество"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %s", want, err)
		}
	}

	ctx2, s2, doc2, _ := tpMethodsServer(t, src)
	_ = doc
	err2 := postViaService(t, ctx2, s2, doc2, []map[string]any{{"Номенклатура": "Доска", "Количество": float64(1)}})
	if err2 == nil || !strings.Contains(err2.Error(), "КоличествоСтрок") {
		t.Errorf("на пути сервиса опечатка не отклонена: %v", err2)
	}
}
