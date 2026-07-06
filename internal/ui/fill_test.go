package ui

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

// «Ввод на основании»: документ-приёмник с based_on и хуком
// ОбработкаЗаполнения должен после Fill получить поля шапки и строки ТЧ,
// скопированные из источника.
func TestFill_OnFillHook_CopiesFieldsAndTablePart(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := &metadata.Entity{
		Name: "РеализацияТоваров",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Покупатель", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	recv := &metadata.Entity{
		Name:    "ВозвратОтПокупателя",
		Kind:    metadata.KindDocument,
		BasedOn: []string{"РеализацияТоваров"},
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Покупатель", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{src, recv}); err != nil {
		t.Fatal(err)
	}

	onFillSrc := `Процедура ОбработкаЗаполнения(ДанныеЗаполнения)
  this.Покупатель = ДанныеЗаполнения.Покупатель;
  Для Каждого Стр Из ДанныеЗаполнения.Товары Цикл
    Нов = this.Товары.Добавить();
    Нов.Номенклатура = Стр.Номенклатура;
    Нов.Количество = Стр.Количество;
    Нов.Цена = Стр.Цена;
  КонецЦикла;
КонецПроцедуры`
	prog := mustParse(t, onFillSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{src, recv},
		Programs: map[string]*ast.Program{"ВозвратОтПокупателя": prog},
	})

	// Записываем источник напрямую в БД (без UI submit).
	srcID := uuid.New()
	srcFields := map[string]any{
		"Номер":      "РТ-001",
		"Покупатель": "ООО Ромашка",
	}
	if err := db.Upsert(ctx, src.Name, srcID, srcFields, src); err != nil {
		t.Fatalf("Upsert источника: %v", err)
	}
	tpRows := []map[string]any{
		{"Номенклатура": "Стул", "Количество": float64(2), "Цена": float64(1500)},
		{"Номенклатура": "Стол", "Количество": float64(1), "Цена": float64(8000)},
	}
	if err := db.UpsertTablePartRows(ctx, src.Name, "Товары", srcID, tpRows, src.TableParts[0]); err != nil {
		t.Fatalf("UpsertTablePartRows источника: %v", err)
	}

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &entityservice.Service{
		Store: db, Reg: registry, Interp: interp,
		MakeThis: func(ctx context.Context, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return &formObjectThis{obj: obj, entity: e}
		},
	}

	result, err := svc.Fill(ctx, entityservice.FillRequest{
		Receiver:   recv,
		SourceType: "РеализацияТоваров",
		SourceID:   srcID,
	})
	if err != nil {
		t.Fatalf("Fill вернул ошибку: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("DSLError: %s", result.DSLError)
	}

	// Шапка скопирована.
	if got := result.Fields["покупатель"]; got != "ООО Ромашка" {
		t.Errorf("Покупатель = %v, want \"ООО Ромашка\"", got)
	}
	// ТЧ скопирована — 2 строки.
	rows := result.TablePartRows["Товары"]
	if len(rows) != 2 {
		t.Fatalf("ТЧ.количество = %d, want 2", len(rows))
	}
	// MapThis.Set нормализует ключи в lower-case при заполнении из DSL —
	// Service.Fill приводит их обратно в PascalCase metadata, чтобы шаблон
	// формы (строгое {{index $row $fn}}) находил значения.
	if got := rows[0]["Номенклатура"]; got != "Стул" {
		t.Errorf("ТЧ[0].Номенклатура = %v, want \"Стул\" (ключ должен быть PascalCase)", got)
	}
	if qty := toFloat(rows[1]["Количество"]); qty != 1 {
		t.Errorf("ТЧ[1].Количество = %v, want 1 (ключ должен быть PascalCase)", rows[1]["Количество"])
	}
	// И никаких lowercase-дублей не должно остаться.
	if _, has := rows[0]["номенклатура"]; has {
		t.Errorf("дублирующий lowercase-ключ остался в ТЧ: %v", rows[0])
	}
}

// mapValue — регистронезависимое чтение ключа из row (DSL пишет в lower-case,
// читать удобнее в PascalCase для совпадения с YAML-метаданными).
func mapValue(row map[string]any, key string) any {
	for k, v := range row {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

// Fill отклоняет источник, который не разрешён в based_on приёмника.
func TestFill_RejectsUnauthorizedSource(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := &metadata.Entity{Name: "Поступление", Kind: metadata.KindDocument}
	recv := &metadata.Entity{Name: "Возврат", Kind: metadata.KindDocument, BasedOn: []string{"РеализацияТоваров"}}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{src, recv}})
	svc := &entityservice.Service{Store: db, Reg: registry, Interp: interpreter.New()}

	_, err = svc.Fill(ctx, entityservice.FillRequest{
		Receiver:   recv,
		SourceType: "Поступление", // не в based_on
		SourceID:   uuid.New(),
	})
	if err == nil {
		t.Fatal("Fill должен был отклонить запрос с неразрешённым источником")
	}
	if !entityservice.IsBadRequest(err) {
		t.Errorf("ожидалась клиентская ошибка (IsBadRequest), получили %v", err)
	}
}

// Fill без хука ОбработкаЗаполнения у приёмника — не ошибка, просто пустой
// результат (пользователь заполнит вручную).
func TestFill_NoHook_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src := &metadata.Entity{Name: "РеализацияТоваров", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}}
	recv := &metadata.Entity{Name: "Возврат", Kind: metadata.KindDocument, BasedOn: []string{"РеализацияТоваров"}}

	if err := db.Migrate(ctx, []*metadata.Entity{src, recv}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{src, recv}})
	svc := &entityservice.Service{Store: db, Reg: registry, Interp: interpreter.New()}

	srcID := uuid.New()
	if err := db.Upsert(ctx, src.Name, srcID, map[string]any{"Номер": "РТ-001"}, src); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Fill(ctx, entityservice.FillRequest{Receiver: recv, SourceType: src.Name, SourceID: srcID})
	if err != nil {
		t.Fatalf("Fill вернул ошибку: %v", err)
	}
	if len(result.Fields) != 0 {
		t.Errorf("ожидалось пустое Fields, получили %v", result.Fields)
	}
	if result.DSLError != "" {
		t.Errorf("без хука DSLError должен быть пустым, получили %q", result.DSLError)
	}
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case decimal.Decimal:
		return x.InexactFloat64()
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		// SQLite numeric → text; парсим как float64.
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
