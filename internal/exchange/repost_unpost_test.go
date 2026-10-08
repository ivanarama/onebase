package exchange_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Приёмник обмена с repost: true перепроводит пришедший проведённым документ —
// движения ложатся в его регистры. Когда источник потом отменяет проведение или
// помечает документ на удаление, пакет приносит непроведённую версию, и приёмник
// ставит posted = false (SetExchangeObjectState). Движения, записанные прежним
// перепроведением, при этом оставались: документ «не проведён», а его количество
// по-прежнему в остатках. Отмена проведения на источнике до приёмника не доходила.
//
// Тест идёт тем же путём, что HTTP-приёмник: ApplyPackage с перепроведением через
// entityservice.Service.Repost (ui.Server.exchangeApplyOptions).
func TestApplyPackage_UnpostedVersionClearsReceiverMovements(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deletion bool
		// repostFails: источник прислал новую ПРОВЕДЁННУЮ версию, а её
		// перепроведение на приёмнике сорвалось.
		repostFails bool
		// Старый обмен оставлял posted=false при сохранённых движениях.
		legacyUnposted bool
	}{
		{"отмена проведения на источнике", false, false, false},
		{"пометка удаления на источнике", true, false, false},
		{"перепроведение новой версии сорвалось", false, true, false},
		{"старые движения после отмены проведения", false, false, true},
		{"старые движения после пометки удаления", true, false, true},
		{"старые движения после ошибки перепроведения", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, b *storage.DB) {
				doc := &metadata.Entity{
					Name: "Продажа", Kind: metadata.KindDocument, Posting: true,
					Fields: []metadata.Field{
						{Name: "Товар", Type: metadata.FieldTypeString},
						{Name: "Количество", Type: metadata.FieldTypeNumber},
					},
				}
				reg := &metadata.Register{
					Name:       "Остатки",
					Totals:     metadata.RegisterTotals{Enabled: true},
					Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
					Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
				}
				plan := repostPlan(true)

				// Источник: документ проведён и зарегистрирован для fil01.
				a, ctxA := newBase(t, doc)
				if err := a.SaveExchangeThisNode(ctxA, plan.Name, "center"); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				fields := map[string]any{"Товар": "Гвоздь", "Количество": float64(10)}
				if err := a.Upsert(ctxA, doc.Name, id, fields, doc); err != nil {
					t.Fatal(err)
				}
				if err := a.SetPosted(ctxA, doc.Name, id, true); err != nil {
					t.Fatal(err)
				}
				register := func(changedAt int64) []byte {
					t.Helper()
					v, err := a.EntityVersion(ctxA, doc.Name, id)
					if err != nil {
						t.Fatal(err)
					}
					if err := a.RegisterExchangeChange(ctxA, storage.ExchangeChange{
						Plan: plan.Name, ObjectType: doc.Name, ObjectID: id.String(), NodeCode: "fil01",
						Version: v, Deletion: tc.deletion && changedAt > 1000, ChangedAt: changedAt,
					}); err != nil {
						t.Fatal(err)
					}
					data, err := exchange.BuildPackage(ctxA, a, fakeResolver{doc.Name: doc}, plan, "fil01")
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				posted := register(1000)

				// Приёмник: настоящий реестр и перепроведение модулем проведения.
				ctxB := context.Background()
				if err := b.EnsureExchangeSchema(ctxB); err != nil {
					t.Fatal(err)
				}
				if err := b.Migrate(ctxB, []*metadata.Entity{doc}); err != nil {
					t.Fatal(err)
				}
				if err := b.MigrateRegisters(ctxB, []*metadata.Register{reg}); err != nil {
					t.Fatal(err)
				}
				if err := b.SaveExchangeThisNode(ctxB, plan.Name, "fil01"); err != nil {
					t.Fatal(err)
				}
				registry := runtime.NewRegistry()
				registry.Load(runtime.LoadOptions{
					Entities:  []*metadata.Entity{doc},
					Registers: []*metadata.Register{reg},
					Programs: map[string]*ast.Program{doc.Name: parseProgram(t, `Процедура OnPost()
  Дв = Движения.Остатки.Добавить();
  Дв.Товар = this.Товар;
  Дв.Количество = this.Количество;
КонецПроцедуры`)},
				})
				interp := interpreter.New()
				interp.LookupProc = registry.GetModuleProc
				svc := &entityservice.Service{
					Store: b, Reg: registry, Interp: interp,
					BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
						return dslvars.Common{Ctx: c, Reg: registry, Store: b, Movements: mc}.Build(), nil
					},
				}
				opts := exchange.ApplyOptions{Repost: svc.Repost}
				movements := func() int {
					t.Helper()
					var n int
					if err := b.QueryRow(ctxB, "SELECT COUNT(*) FROM "+metadata.RegisterTableName(reg.Name)+
						" WHERE recorder = "+b.Dialect().Placeholder(1), id.String()).Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n
				}

				if _, err := exchange.ApplyPackage(ctxB, b, registry, plan, posted, opts); err != nil {
					t.Fatalf("загрузка проведённой версии: %v", err)
				}
				if n := movements(); n != 1 {
					t.Fatalf("после перепроведения на приёмнике %d движений, ожидалось 1", n)
				}

				if tc.legacyUnposted {
					// Воспроизводим состояние базы после прежней загрузки:
					// документ уже непроведён, его настоящие движения ещё лежат.
					if err := b.SetPosted(ctxB, doc.Name, id, false); err != nil {
						t.Fatal(err)
					}
				}

				// Источник отменяет проведение, помечает на удаление или меняет
				// проведённый документ — новая ревизия.
				if tc.repostFails {
					fields = map[string]any{"Товар": "Гвоздь", "Количество": float64(7)}
				}
				if err := a.Upsert(ctxA, doc.Name, id, fields, doc); err != nil {
					t.Fatal(err)
				}
				if tc.deletion {
					if err := a.MarkForDeletion(ctxA, doc.Name, id, true); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.SetPosted(ctxA, doc.Name, id, tc.repostFails); err != nil {
					t.Fatal(err)
				}
				next := register(2000)

				if tc.repostFails {
					failing := exchange.ApplyOptions{Repost: func(context.Context, string, uuid.UUID) error {
						return errors.New("перепроведение сорвалось")
					}}
					if _, err := exchange.ApplyPackage(ctxB, b, registry, plan, next, failing); err == nil {
						t.Fatal("ошибка перепроведения должна вернуться вызывающему")
					}
				} else if _, err := exchange.ApplyPackage(ctxB, b, registry, plan, next, opts); err != nil {
					t.Fatalf("загрузка новой версии: %v", err)
				}
				row, err := b.GetByID(ctxB, doc.Name, id, doc)
				if err != nil {
					t.Fatal(err)
				}
				if toBoolT(row["posted"]) {
					t.Fatalf("документ на приёмнике остался проведённым")
				}
				if n := movements(); n != 0 {
					t.Fatalf("документ на приёмнике не проведён, а его движений в регистре %d — остатки считают непроведённый документ", n)
				}
				var quantity float64
				if err := b.QueryRow(ctxB, "SELECT COALESCE(SUM(CAST(количество AS NUMERIC)), 0) FROM "+metadata.RegisterTotalsTableName(reg.Name)).Scan(&quantity); err != nil {
					t.Fatal(err)
				}
				if quantity != 0 {
					t.Fatalf("итоги после очистки = %v, ожидался нулевой остаток", quantity)
				}
			})
		})
	}
}

func parseProgram(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog
}
