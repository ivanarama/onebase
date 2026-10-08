package entityservice

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Проводка бухрегистра обязана ссылаться на счета плана счетов своего регистра.
//
// Раньше код счёта не проверялся нигде: проводка «Дт 41х — Кт 60» записывалась,
// а остатки (соединение с _accounts по коду) её дебетовую половину теряли —
// Дт 41 = 100 при Кт 60 = 600, баланс не сходился, и никто об этом не узнавал.
// Свёртка базы после этого удаляла такие проводки навсегда: опорные остатки
// строятся из того же соединения. Ещё тише терялось `Дв.СчетДт` — написание
// свойства из 1С, без «ё»: ключ «счетдт» storage не читал, и в колонку уходила
// строка «<nil>».
//
// Тест идёт через entityservice.Save с проведением — тем же путём, что форма и
// REST: модуль проведения → коллектор движений → запись проводок.
func TestPostAccountMovements_AccountCodesCheckedAgainstChart(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name: "Операция", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Дт", Type: metadata.FieldTypeString},
			{Name: "Кт", Type: metadata.FieldTypeString},
			{Name: "ЧерезЕ", Type: metadata.FieldTypeBool},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
	}
	ar := &metadata.AccountRegister{
		Name:      "Бух",
		Accounts:  "Основной",
		Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
	}
	charts := []*metadata.ChartOfAccounts{
		{Name: "Основной", Accounts: []metadata.Account{
			{Code: "41", Name: "Товары", Kind: "active"},
			{Code: "60", Name: "Поставщики", Kind: "passive"},
		}},
		// Счёт есть в базе, но в чужом плане: проводка регистра «Бух» на него —
		// та же ошибка, что и на несуществующий код.
		{Name: "Налоговый", Accounts: []metadata.Account{
			{Code: "99", Name: "Прибыли и убытки", Kind: "passive"},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncAccounts(ctx, charts); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatal(err)
	}

	onPost := mustParseProgramT(t, `Процедура OnPost()
  Дв = Движения.Бух.Добавить();
  Если this.ЧерезЕ Тогда
    Дв.СчетДт = this.Дт;
    Дв.СчетКт = this.Кт;
  Иначе
    Дв.СчётДт = this.Дт;
    Дв.СчётКт = this.Кт;
  КонецЕсли;
  Дв.Сумма = this.Сумма;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: onPost},
	})
	registry.LoadAccountRegisters([]*metadata.AccountRegister{ar}, charts)
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{
		Store: db, Reg: registry, Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
			return dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build(), nil
		},
	}

	post := func(dt, kt string, viaE bool) (uuid.UUID, error) {
		id := uuid.New()
		res, err := svc.Save(ctx, SaveRequest{
			Entity: doc, ID: id, IsNew: true, Action: "post",
			Fields: map[string]any{"Дт": dt, "Кт": kt, "ЧерезЕ": viaE, "Сумма": float64(100)},
		})
		if err == nil && res.DSLError != "" {
			err = fmt.Errorf("%s", res.DSLError)
		}
		return id, err
	}
	entries := func(id uuid.UUID) []string {
		rows, err := db.Query(ctx, "SELECT счётдт, счёткт FROM "+metadata.AccountRegTableName(ar.Name)+
			" WHERE регистратор = ?", id.String())
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var dt, kt string
			if err := rows.Scan(&dt, &kt); err != nil {
				t.Fatal(err)
			}
			out = append(out, dt+"/"+kt)
		}
		return out
	}
	posted := func(id uuid.UUID) bool {
		row, err := db.GetByID(ctx, doc.Name, id, doc)
		if err != nil {
			return false
		}
		p, _ := row["posted"].(bool)
		return p
	}

	t.Run("счета плана проводятся", func(t *testing.T) {
		id, err := post("41", "60", false)
		if err != nil {
			t.Fatalf("проведение: %v", err)
		}
		if got := entries(id); len(got) != 1 || got[0] != "41/60" {
			t.Fatalf("проводки: %v, ожидалась 41/60", got)
		}
	})

	t.Run("СчетДт без ё — то же свойство", func(t *testing.T) {
		id, err := post("41", "60", true)
		if err != nil {
			t.Fatalf("проведение с СчетДт/СчетКт: %v", err)
		}
		if got := entries(id); len(got) != 1 || got[0] != "41/60" {
			t.Fatalf("проводки: %v, ожидалась 41/60 (СчетДт/СчетКт терялись и писались «<nil>»)", got)
		}
	})

	for _, c := range []struct {
		name, dt, kt string
		want         []string
	}{
		{"код не из плана", "41х", "60", []string{"41х", "Основной"}},
		{"счёт чужого плана", "41", "99", []string{"99", "Основной"}},
		{"пустой счёт Кт", "41", "", []string{"Кт"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, err := post(c.dt, c.kt, false)
			if err == nil {
				t.Fatalf("проведение с Дт %q, Кт %q прошло: проводки %v", c.dt, c.kt, entries(id))
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("ошибка %q не называет %q", err.Error(), w)
				}
			}
			if got := entries(id); len(got) != 0 {
				t.Errorf("отказ проведения оставил проводки: %v", got)
			}
			if posted(id) {
				t.Errorf("документ помечен проведённым после отказа")
			}
		})
	}
}
