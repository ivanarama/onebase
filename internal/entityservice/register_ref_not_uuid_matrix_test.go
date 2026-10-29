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

// Поле-ссылка из результата запроса приходит представлением — номером
// документа или наименованием. Модуль проведения, который клал такое значение
// в измерение регистра, получал на SQLite молча сохранённый текст «ПОС-00001»
// вместо ссылки: партия поступления, списанная таким движением, не уменьшалась
// никогда, и контроль остатков пропускал продажу сверх наличия. На PostgreSQL
// та же запись падала ошибкой драйвера о синтаксисе uuid, не называя ни регистр,
// ни поле.
//
// Запись регистра обязана отвергать такое значение на обеих СУБД понятной
// ошибкой — так же, как реквизит объекта. Проверка идёт через проведение
// документа (Service.Save), то есть путём формы и REST, для регистра
// накопления и регистра сведений.
func TestPostingRejectsPresentationInRegisterReferenceMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		batch := &metadata.Entity{
			Name: "Партия" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		refType := metadata.FieldType("reference:" + batch.Name)
		doc := &metadata.Entity{
			Name: "Продажа" + suffix, Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{
				{Name: "Дата", Type: metadata.FieldTypeDate},
				{Name: "Значение", Type: metadata.FieldTypeString},
			},
		}
		reg := &metadata.Register{
			Name:       "Остатки" + suffix,
			Dimensions: []metadata.Field{{Name: "Партия", Type: refType, RefEntity: batch.Name}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		ir := &metadata.InfoRegister{
			Name: "Цены" + suffix, Periodic: true, Recorder: true,
			Dimensions: []metadata.Field{{Name: "Партия", Type: refType, RefEntity: batch.Name}},
			Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{batch, doc}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		existing := uuid.New()
		if err := db.Upsert(ctx, batch.Name, existing, map[string]any{"Наименование": "ПОС-00001"}, batch); err != nil {
			t.Fatal(err)
		}

		for _, target := range []struct{ name, body string }{
			{"регистр накопления", `Дв = Движения.` + reg.Name + `.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Партия = this.Значение;
    Дв.Количество = 1;`},
			{"регистр сведений", `Дв = Движения.` + ir.Name + `.Добавить();
    Дв.Период = this.Дата;
    Дв.Партия = this.Значение;
    Дв.Цена = 1;`},
		} {
			t.Run(target.name, func(t *testing.T) {
				onPost := mustParseProgramT(t, "Процедура OnPost()\n    "+target.body+"\nКонецПроцедуры")
				registry := runtime.NewRegistry()
				registry.Load(runtime.LoadOptions{
					Entities:  []*metadata.Entity{batch, doc},
					Registers: []*metadata.Register{reg},
					InfoRegs:  []*metadata.InfoRegister{ir},
					Programs:  map[string]*ast.Program{doc.Name: onPost},
				})
				interp := interpreter.New()
				interp.LookupProc = registry.GetModuleProc
				svc := &Service{
					Store: db, Reg: registry, Interp: interp,
					BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
						return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
					},
				}
				post := func(value string) (uuid.UUID, error) {
					id := uuid.New()
					res, err := svc.Save(ctx, SaveRequest{
						Entity: doc, ID: id, IsNew: true, Action: "post",
						Fields: map[string]any{"Дата": testDay(), "Значение": value},
					})
					if err == nil && res.DSLError != "" {
						err = fmt.Errorf("%s", res.DSLError)
					}
					return id, err
				}

				// Представление вместо ссылки: проведение отклонено понятной
				// ошибкой, а не сырой ошибкой драйвера и не молчаливой записью.
				rejected, err := post("ПОС-00001")
				if err == nil {
					t.Fatalf("документ проведён: в измерение-ссылку записан текст «ПОС-00001»")
				}
				for _, want := range []string{"ПОС-00001", "не является ссылкой", batch.Name} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("ошибка не содержит %q: %v", want, err)
					}
				}
				if strings.Contains(strings.ToLower(err.Error()), "uuid") {
					t.Errorf("ошибка драйвера вместо понятной: %v", err)
				}
				var stored int
				for _, table := range []string{metadata.RegisterTableName(reg.Name), metadata.InfoRegTableName(ir.Name)} {
					var n int
					if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+
						" WHERE CAST(recorder AS TEXT) = "+db.Dialect().Placeholder(1), rejected.String()).Scan(&n); err != nil {
						t.Fatal(err)
					}
					stored += n
				}
				if stored != 0 {
					t.Fatalf("отклонённое проведение оставило %d движений", stored)
				}

				// Настоящая ссылка UUID-строкой проводится, как и раньше.
				if _, err := post(existing.String()); err != nil {
					t.Fatalf("проведение со ссылкой: %v", err)
				}
			})
		}
	})
}

func testDay() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }

// Ошибка записи регистра типизирована так же, как у реквизита объекта:
// вызывающие (REST, формы) различают её через errors.Is.
func TestRegisterReferenceMismatchIsTyped(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		reg := &metadata.Register{
			Name:       "Партии" + suffix,
			Dimensions: []metadata.Field{{Name: "Документ", Type: "reference:Поступление", RefEntity: "Поступление"}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		period := testDay()
		err := db.WriteMovements(ctx, reg.Name, "Реализация", uuid.New(), []map[string]any{
			{"Документ": "ПОС-00005", "Количество": 1, "ВидДвижения": "Расход"},
		}, reg, &period)
		if !errors.Is(err, storage.ErrReferenceTypeMismatch) {
			t.Fatalf("ожидалась ErrReferenceTypeMismatch, получено: %v", err)
		}
	})
}
