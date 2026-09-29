package entityservice

import (
	"context"
	"fmt"
	"strconv"
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

// Перепроведение снимает движения из регистров, которых модуль в этот раз не
// коснулся.
//
// Раньше запись движений шла только по регистрам, где хук вызвал Добавить()
// или Очистить(). Модуль, пишущий регистр по условию, после перепроведения со
// снятым условием оставлял документу прежние движения — остатки, итоги,
// регистр сведений и проводки расходились с данными документа молча.
//
// Сценарий через публичный путь проведения (Service.Save, Action "post") и все
// три вида регистров: накопления с итогами, сведений и бухгалтерии. Второй
// документ пишет те же регистры и обязан остаться нетронутым: снимаются только
// движения перепроводимого регистратора. Матрично — удаление по регистратору и
// пересчёт итогов диалектозависимы.
func TestRepost_ClearsMovementsOfUntouchedRegisters_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{
			Name: "Реализация", Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{
				{Name: "Дата", Type: metadata.FieldTypeDate},
				{Name: "Ключ", Type: metadata.FieldTypeString},
				{Name: "Флаг", Type: metadata.FieldTypeBool},
			},
		}
		key := []metadata.Field{{Name: "Т", Type: metadata.FieldTypeString}}
		always := &metadata.Register{Name: "РегА", Dimensions: key,
			Resources: []metadata.Field{{Name: "К", Type: metadata.FieldTypeNumber}}}
		withTotals := &metadata.Register{Name: "РегБ", Dimensions: key,
			Resources: []metadata.Field{{Name: "К", Type: metadata.FieldTypeNumber}},
			Totals:    metadata.RegisterTotals{Enabled: true}}
		info := &metadata.InfoRegister{Name: "ИнфоВ", Dimensions: key,
			Resources: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}}}
		acc := &metadata.AccountRegister{Name: "БухГ", Accounts: "Основной",
			Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}}}

		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{always, withTotals}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{acc}); err != nil {
			t.Fatal(err)
		}

		onPost := mustParseProgramT(t, `Процедура OnPost()
  Движения.РегА.Очистить();
  Дв = Движения.РегА.Добавить();
  Дв.Т = this.Ключ;
  Дв.К = 1;
  Если this.Флаг Тогда
    Дв = Движения.РегБ.Добавить();
    Дв.Т = this.Ключ;
    Дв.К = 100;
    Дв = Движения.ИнфоВ.Добавить();
    Дв.Т = this.Ключ;
    Дв.Цена = 5;
    Дв = Движения.БухГ.Добавить();
    Дв.СчётДт = "41";
    Дв.СчётКт = "60";
    Дв.Сумма = 100;
  КонецЕсли;
КонецПроцедуры`)
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{
			Entities:  []*metadata.Entity{doc},
			Registers: []*metadata.Register{always, withTotals},
			InfoRegs:  []*metadata.InfoRegister{info},
			Programs:  map[string]*ast.Program{doc.Name: onPost},
		})
		registry.LoadAccountRegisters([]*metadata.AccountRegister{acc}, nil)
		interp := interpreter.New()
		interp.LookupProc = registry.GetModuleProc
		svc := &Service{
			Store: db, Reg: registry, Interp: interp,
			BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
				return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
			},
		}

		date := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
		post := func(id uuid.UUID, isNew bool, key string, flag bool) {
			t.Helper()
			res, err := svc.Save(ctx, SaveRequest{
				Entity: doc, ID: id, IsNew: isNew, Action: "post",
				Fields: map[string]any{"Дата": date, "Ключ": key, "Флаг": flag},
			})
			if err != nil {
				t.Fatalf("проведение %s: %v", key, err)
			}
			if res.DSLError != "" {
				t.Fatalf("проведение %s: %s", key, res.DSLError)
			}
		}
		count := func(table, recorderCol string, id uuid.UUID) int {
			t.Helper()
			var n int
			q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = '%s'", table, recorderCol, id)
			if err := db.QueryRow(ctx, q).Scan(&n); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			return n
		}
		totals := func(key string) float64 {
			t.Helper()
			var raw string
			// Текстом: PostgreSQL отдаёт SUM(numeric) структурой драйвера.
			q := fmt.Sprintf("SELECT CAST(COALESCE(SUM(к), 0) AS TEXT) FROM %s WHERE т = '%s'",
				metadata.RegisterTotalsTableName(withTotals.Name), key)
			if err := db.QueryRow(ctx, q).Scan(&raw); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				t.Fatalf("итоги %s: %v (%q)", key, err, raw)
			}
			return f
		}
		regA := metadata.RegisterTableName(always.Name)
		regB := metadata.RegisterTableName(withTotals.Name)
		infoV := metadata.InfoRegTableName(info.Name)
		accG := metadata.AccountRegTableName(acc.Name)

		first, other := uuid.New(), uuid.New()
		post(first, true, "k1", true)
		post(other, true, "k2", true)
		for _, c := range []struct {
			table, col string
		}{{regA, "recorder"}, {regB, "recorder"}, {infoV, "recorder"}, {accG, "регистратор"}} {
			if n := count(c.table, c.col, first); n != 1 {
				t.Fatalf("после первого проведения в %s %d строк документа, ждали 1", c.table, n)
			}
		}
		if got := totals("k1"); got != 100 {
			t.Fatalf("итоги РегБ по k1 после проведения = %v, ждали 100", got)
		}

		// Условие снято — модуль больше не пишет РегБ, ИнфоВ и БухГ.
		post(first, false, "k1", false)

		if n := count(regA, "recorder", first); n != 1 {
			t.Errorf("РегА: %d строк документа, ждали 1 (модуль пишет его всегда)", n)
		}
		for _, c := range []struct {
			table, col string
		}{{regB, "recorder"}, {infoV, "recorder"}, {accG, "регистратор"}} {
			if n := count(c.table, c.col, first); n != 0 {
				t.Errorf("после перепроведения в %s осталось %d строк документа, которых модуль больше не формирует", c.table, n)
			}
		}
		if got := totals("k1"); got != 0 {
			t.Errorf("итоги РегБ по k1 после перепроведения = %v, ждали 0 — итоги разошлись с движениями", got)
		}
		// Чужой документ в тех же регистрах не задет.
		for _, c := range []struct {
			table, col string
		}{{regA, "recorder"}, {regB, "recorder"}, {infoV, "recorder"}, {accG, "регистратор"}} {
			if n := count(c.table, c.col, other); n != 1 {
				t.Errorf("движения другого документа в %s: %d строк, ждали 1", c.table, n)
			}
		}
		if got := totals("k2"); got != 100 {
			t.Errorf("итоги РегБ по k2 = %v, ждали 100 — задеты чужие движения", got)
		}

		// Отмена проведения идёт той же дорогой и снимает всё.
		res, err := svc.Unpost(ctx, doc, other)
		if err != nil {
			t.Fatalf("отмена проведения: %v", err)
		}
		if res.DSLError != "" {
			t.Fatalf("отмена проведения: %s", res.DSLError)
		}
		for _, c := range []struct {
			table, col string
		}{{regA, "recorder"}, {regB, "recorder"}, {infoV, "recorder"}, {accG, "регистратор"}} {
			if n := count(c.table, c.col, other); n != 0 {
				t.Errorf("после отмены проведения в %s осталось %d строк документа", c.table, n)
			}
		}
		if got := totals("k2"); got != 0 {
			t.Errorf("итоги РегБ по k2 после отмены проведения = %v, ждали 0", got)
		}
	})
}

// Больше 500 регистров: публичные проведение, перепроведение и отмена должны
// работать и при пустом наборе движений. Проверяем все виды регистров, в том
// числе движения по обе стороны границы SQL-пакета и в последнем регистре.
func TestPosting_ManyUntouchedRegisters_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "БольшойДокумент", Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}, {Name: "Флаг", Type: metadata.FieldTypeBool}, {Name: "Ключ", Type: metadata.FieldTypeString}}}
		var regs []*metadata.Register
		var infos []*metadata.InfoRegister
		for i := 0; i < 250; i++ {
			regs = append(regs, &metadata.Register{Name: fmt.Sprintf("Р%03d", i), Resources: []metadata.Field{{Name: "К", Type: metadata.FieldTypeNumber}}})
			infos = append(infos, &metadata.InfoRegister{Name: fmt.Sprintf("И%03d", i), Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "К", Type: metadata.FieldTypeNumber}}})
		}
		acc := &metadata.AccountRegister{Name: "Бух", Accounts: "Основной", Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, infos); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{acc}); err != nil {
			t.Fatal(err)
		}

		program := mustParseProgramT(t, `Процедура OnPost()
 Если this.Флаг Тогда
  Дв = Движения.Р000.Добавить(); Дв.К = 1;
  Дв = Движения.Р249.Добавить(); Дв.К = 1;
  Дв = Движения.И000.Добавить(); Дв.Ключ = this.Ключ; Дв.К = 1;
  Дв = Движения.И149.Добавить(); Дв.Ключ = this.Ключ; Дв.К = 1;
  Дв = Движения.И150.Добавить(); Дв.Ключ = this.Ключ; Дв.К = 1;
  Дв = Движения.И249.Добавить(); Дв.Ключ = this.Ключ; Дв.К = 1;
  Дв = Движения.Бух.Добавить(); Дв.СчётДт = "41"; Дв.СчётКт = "60"; Дв.Сумма = 1;
 КонецЕсли;
КонецПроцедуры`)
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, Registers: regs, InfoRegs: infos,
			Programs: map[string]*ast.Program{doc.Name: program}})
		registry.LoadAccountRegisters([]*metadata.AccountRegister{acc}, nil)
		interp := interpreter.New()
		interp.LookupProc = registry.GetModuleProc
		svc := &Service{Store: db, Reg: registry, Interp: interp,
			BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
				return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
			},
		}
		date := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
		post := func(id uuid.UUID, isNew, flag bool) {
			t.Helper()
			res, err := svc.Save(ctx, SaveRequest{Entity: doc, ID: id, IsNew: isNew, Action: "post",
				Fields: map[string]any{"Дата": date, "Флаг": flag, "Ключ": id.String()}})
			if err != nil {
				t.Fatalf("Save(post): %v", err)
			}
			if res.DSLError != "" {
				t.Fatalf("Save(post): %s", res.DSLError)
			}
		}
		tables := []struct{ table, col string }{
			{metadata.RegisterTableName(regs[0].Name), "recorder"},
			{metadata.RegisterTableName(regs[249].Name), "recorder"},
			{metadata.InfoRegTableName(infos[0].Name), "recorder"},
			{metadata.InfoRegTableName(infos[149].Name), "recorder"},
			{metadata.InfoRegTableName(infos[150].Name), "recorder"},
			{metadata.InfoRegTableName(infos[249].Name), "recorder"},
			{metadata.AccountRegTableName(acc.Name), "регистратор"},
		}
		check := func(id uuid.UUID, want int) {
			t.Helper()
			for _, table := range tables {
				var n int
				q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = '%s'", table.table, table.col, id)
				if err := db.QueryRow(ctx, q).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != want {
					t.Errorf("%s: movements = %d, want %d", table.table, n, want)
				}
			}
		}
		first, other := uuid.New(), uuid.New()
		post(first, true, false) // пустой mc — все 501 регистра участвуют в поиске
		check(first, 0)
		post(first, false, true)
		post(other, true, true)
		check(first, 1)
		if err := svc.Repost(ctx, doc.Name, first); err != nil {
			t.Fatalf("Repost: %v", err)
		}
		check(first, 1)
		post(first, false, false) // все прежние движения вне mc, снимаются оба пакета
		check(first, 0)
		check(other, 1)
		post(first, false, true)
		res, err := svc.Unpost(ctx, doc, first)
		if err != nil {
			t.Fatalf("Unpost: %v", err)
		}
		if res.DSLError != "" {
			t.Fatalf("Unpost: %s", res.DSLError)
		}
		check(first, 0)
		check(other, 1)
	})
}
