package entityservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// У регистра сведений одна запись на ключ (период + измерения). Запись движений
// шла через INSERT … ON CONFLICT DO UPDATE SET …, recorder = EXCLUDED.recorder:
// второй документ с тем же ключом молча забирал запись первого. Отмена проведения
// второго удаляла запись целиком — первый документ оставался проведённым, а его
// значения в регистре уже не было. В 1С это ошибка уникальности.
//
// Тест идёт через entityservice.Save с проведением и Service.Unpost — пути формы
// и REST.
func TestPostInfoRegister_KeyOwnedByAnotherDocumentIsNotTakenOver(t *testing.T) {
	dbtest.ForEachDialect(t, testPostInfoRegisterKeyOwnership)
}

func testPostInfoRegisterKeyOwnership(t *testing.T, db *storage.DB) {
	ctx := context.Background()

	doc := &metadata.Entity{
		Name: "УстановкаЦены", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Товар", Type: metadata.FieldTypeString},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
			{Name: "Строк", Type: metadata.FieldTypeNumber},
		},
	}
	ir := &metadata.InfoRegister{
		Name: "Цены", Periodic: true, Recorder: true,
		Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	onPost := mustParseProgramT(t, `Процедура OnPost()
  Для Н = 1 По this.Строк Цикл
    Дв = Движения.Цены.Добавить();
    Дв.Период = this.Дата;
    Дв.Товар = this.Товар;
    Дв.Цена = this.Цена + Н - 1;
  КонецЦикла;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		InfoRegs: []*metadata.InfoRegister{ir},
		Programs: map[string]*ast.Program{doc.Name: onPost},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{
		Store: db, Reg: registry, Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
		},
	}

	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	post := func(item string, price, rows int) (uuid.UUID, error) {
		id := uuid.New()
		res, err := svc.Save(ctx, SaveRequest{
			Entity: doc, ID: id, IsNew: true, Action: "post",
			Fields: map[string]any{"Дата": day, "Товар": item, "Цена": float64(price), "Строк": float64(rows)},
		})
		if err == nil && res.DSLError != "" {
			err = fmt.Errorf("%s", res.DSLError)
		}
		return id, err
	}
	price := func() (string, string) {
		t.Helper()
		var p, rec string
		err := db.QueryRow(ctx, "SELECT CAST(цена AS TEXT), recorder FROM "+metadata.InfoRegTableName(ir.Name)+
			" WHERE товар = 'Гвоздь'").Scan(&p, &rec)
		if err != nil {
			return "", ""
		}
		return p, rec
	}

	first, err := post("Гвоздь", 10, 1)
	if err != nil {
		t.Fatalf("первый документ: %v", err)
	}

	// Второй документ сначала записан, потом проводится — как из формы.
	second := uuid.New()
	fields := map[string]any{"Дата": day, "Товар": "Гвоздь", "Цена": float64(12), "Строк": float64(1)}
	if _, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: second, IsNew: true, Fields: fields}); err != nil {
		t.Fatalf("запись второго документа: %v", err)
	}
	res, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: second, Fields: fields, Action: "post"})
	if err == nil && res.DSLError != "" {
		err = fmt.Errorf("%s", res.DSLError)
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", err), storage.ErrInfoRegOwnershipConflict) {
		t.Errorf("ownership error is not recognized through wrappers: %v", err)
	}
	if err == nil {
		t.Fatalf("второй документ с тем же ключом (дата, товар) проведён: запись первого перехвачена")
	}
	if !strings.Contains(err.Error(), first.String()) {
		t.Errorf("ошибка не называет документ, которому принадлежит ключ: %v", err)
	}
	if p, rec := price(); p != "10" || rec != first.String() {
		t.Fatalf("запись первого документа изменена: цена %q, регистратор %q", p, rec)
	}
	// Отказ проведения не оставил второй документ проведённым. На main здесь
	// отмена его проведения удаляла запись первого документа целиком.
	if row, err := db.GetByID(ctx, doc.Name, second, doc); err != nil {
		t.Fatal(err)
	} else if p, _ := row["posted"].(bool); p {
		t.Fatalf("второй документ помечен проведённым после отказа")
	}
	if _, err := svc.Unpost(ctx, doc, second); err != nil {
		t.Fatalf("Unpost: %v", err)
	}
	if p, rec := price(); p != "10" || rec != first.String() {
		t.Fatalf("после отмены второго запись первого пропала: цена %q, регистратор %q", p, rec)
	}

	// Внутри одного проведения последняя строка с ключом по-прежнему побеждает
	// (TestWriteInfoMovementsConvergesEquivalentNumberKeysSQLite): это запись
	// одного документа, перехвата нет.
	if _, err := post("Шуруп", 20, 2); err != nil {
		t.Fatalf("две строки одного документа с одним ключом: %v", err)
	}
	var lastPrice string
	if err := db.QueryRow(ctx, "SELECT CAST(цена AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)+" WHERE товар = 'Шуруп'").Scan(&lastPrice); err != nil {
		t.Fatal(err)
	}
	if lastPrice != "21" {
		t.Fatalf("последняя строка собственного ключа не обновлена: %s", lastPrice)
	}

	// A manual value has no owner, so posting can still replace it.
	if err := db.InfoRegSet(ctx, ir, map[string]any{"Товар": "Болт"}, map[string]any{"Цена": float64(1)}, &day); err != nil {
		t.Fatal(err)
	}
	if _, err := post("Болт", 30, 1); err != nil {
		t.Fatalf("запись без регистратора: %v", err)
	}
}
