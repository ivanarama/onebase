package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Виртуальные таблицы регистра бухгалтерии обязаны брать счета только из плана
// счетов своего регистра. Раньше _accounts соединялся без условия на план: во
// втором плане счетов конфигурации счёт с тем же кодом («41») давал вторую строку
// остатков и оборотов с теми же суммами — в отчёте счёт задваивался.
func TestAccountVirtualTables_OnlyRegisterChart_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.EnsureAccountsTable(ctx); err != nil {
			t.Fatal(err)
		}
		charts := []*metadata.ChartOfAccounts{
			{Name: "Основной", Accounts: []metadata.Account{
				{Code: "41", Name: "Товары", Kind: "active"},
				{Code: "60", Name: "Поставщики", Kind: "passive"},
			}},
			{Name: "Налоговый", Accounts: []metadata.Account{
				{Code: "41", Name: "Товары (налоговый учёт)", Kind: "active"},
				{Code: "99", Name: "Прибыли и убытки", Kind: "passive"},
			}},
		}
		if err := db.SyncAccounts(ctx, charts); err != nil {
			t.Fatal(err)
		}

		june := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
		july := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)

		for _, totals := range []bool{false, true} {
			t.Run(fmt.Sprintf("totals=%v", totals), func(t *testing.T) {
				ar := &metadata.AccountRegister{
					Name:      fmt.Sprintf("БухПлан%v", totals),
					Accounts:  "Основной",
					Resources: []metadata.Field{{Name: "Сумма", Type: "number"}},
					Totals:    metadata.RegisterTotals{Enabled: totals},
				}
				if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
					t.Fatal(err)
				}
				for _, mv := range []struct {
					at  time.Time
					sum int64
				}{{june, 1000}, {july, 300}} {
					at := mv.at
					if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), []map[string]any{
						{"счётдт": "41", "счёткт": "60", "сумма": decimal.NewFromInt(mv.sum)},
					}, ar, &at); err != nil {
						t.Fatal(err)
					}
				}

				cases := []struct {
					name, src string
					params    map[string]any
					col       string
					want      map[string]string
				}{
					{"остатки", "ВЫБРАТЬ Счёт, СуммаОстаток ИЗ РегистрБухгалтерии." + ar.Name + ".Остатки()",
						nil, "СуммаОстаток", map[string]string{"41": "1300", "60": "-1300"}},
					{"остатки на дату", "ВЫБРАТЬ Счёт, СуммаОстаток ИЗ РегистрБухгалтерии." + ar.Name + ".Остатки(&НаДату)",
						map[string]any{"НаДату": time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)},
						"СуммаОстаток", map[string]string{"41": "1300", "60": "-1300"}},
					{"обороты", "ВЫБРАТЬ Счёт, Сумма_Дт ИЗ РегистрБухгалтерии." + ar.Name + ".Обороты()",
						nil, "Сумма_Дт", map[string]string{"41": "1300", "60": "0"}},
					{"обороты за период", "ВЫБРАТЬ Счёт, Сумма_Дт ИЗ РегистрБухгалтерии." + ar.Name + ".Обороты(&Начало, &Конец)",
						map[string]any{
							"Начало": time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
							"Конец":  time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
						},
						"Сумма_Дт", map[string]string{"41": "1300", "60": "0"}},
				}
				for _, c := range cases {
					t.Run(c.name, func(t *testing.T) {
						res, err := query.Compile(c.src, query.CompileOpts{
							Dialect:     db.Dialect(),
							Params:      c.params,
							AccountRegs: []*metadata.AccountRegister{ar},
						})
						if err != nil {
							t.Fatalf("compile: %v", err)
						}
						rows, _, err := db.RunQuery(ctx, res.SQL, res.Args)
						if err != nil {
							t.Fatalf("exec: %v\nSQL: %s", err, res.SQL)
						}
						got := map[string]string{}
						for _, r := range rows {
							acc := fmt.Sprint(acctRowValue(r, "Счёт"))
							if _, dup := got[acc]; dup {
								t.Fatalf("счёт %s вернулся дважды: %v", acc, rows)
							}
							got[acc] = acctDecimalString(t, acctRowValue(r, c.col))
						}
						if _, foreign := got["99"]; foreign {
							t.Errorf("в выборку попал счёт 99 чужого плана счетов: %v", got)
						}
						for acc, want := range c.want {
							if got[acc] != want {
								t.Errorf("счёт %s: %s = %q, ожидалось %q (вся выборка %v)", acc, c.col, got[acc], want, got)
							}
						}
					})
				}
			})
		}
	})
}

// acctRowValue ищет колонку результата без учёта регистра: драйверы отдают имена
// колонок в разном регистре.
func acctRowValue(row map[string]any, col string) any {
	for k, v := range row {
		if strings.EqualFold(k, col) {
			return v
		}
	}
	return nil
}

func acctDecimalString(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case nil:
		return "0"
	case decimal.Decimal:
		return x.String()
	case int64:
		return decimal.NewFromInt(x).String()
	case float64:
		return decimal.NewFromFloat(x).String()
	case string:
		d, err := decimal.NewFromString(x)
		if err != nil {
			t.Fatalf("не число: %q", x)
		}
		return d.String()
	case []byte:
		d, err := decimal.NewFromString(string(x))
		if err != nil {
			t.Fatalf("не число: %q", x)
		}
		return d.String()
	}
	return fmt.Sprint(v)
}
