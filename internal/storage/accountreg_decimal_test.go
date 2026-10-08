package storage

// Регрессия (план 47 п. 4.2): AccountBalances/AccountTurnovers сканировали
// денежные ресурсы в float64, хотя план 42 перевёл деньги на decimal. Это било
// не только по виду отчёта: accountOpeningRows строит из этих остатков опорные
// проводки свёртки, то есть округление осело бы в базе навсегда.
//
// Тесты фиксируют ТИП (decimal.Decimal во всех денежных ключах), точное значение
// и сквозную согласованность остатка до/после свёртки. Точность агрегата на
// SQLite обеспечивает перекрытый sum() (sqlite_sum.go): 0.1×3 даёт ровно 0.3, а
// не 0.30000000000000004, как встроенный sum() над TEXT-колонкой. На PostgreSQL
// NUMERIC суммируется точно сам по себе.

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

func decimalAcctReg() *metadata.AccountRegister {
	return &metadata.AccountRegister{
		Name:      "БухТочность",
		Accounts:  "Основной",
		Resources: []metadata.Field{{Name: "Сумма", Type: "number"}},
	}
}

// Денежные ключи остатков и оборотов приходят как decimal.Decimal, а не float64.
func TestAccountBalances_KeepsDecimalPrecision(t *testing.T) {
	ar := decimalAcctReg()
	db, ctx := newAccountTestDB(t, ar)
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	chart := &metadata.ChartOfAccounts{Name: "Основной", Accounts: []metadata.Account{
		{Code: "41", Name: "Товары", Kind: "active"},
		{Code: "60", Name: "Поставщики", Kind: "passive"},
	}}
	if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{chart}); err != nil {
		t.Fatal(err)
	}

	period := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "Сумма": "0.1"}}
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), rows, ar, &period); err != nil {
			t.Fatalf("WriteAccountMovements: %v", err)
		}
	}

	asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	balances, err := db.AccountBalances(ctx, ar.Name, "Основной", asOf, ar.Resources, nil)
	if err != nil {
		t.Fatalf("AccountBalances: %v", err)
	}

	var got41 decimal.Decimal
	var found bool
	for _, b := range balances {
		if code, _ := b["code"].(string); code == "41" {
			d, ok := b["сумма"].(decimal.Decimal)
			if !ok {
				t.Fatalf("сальдо счёта 41: тип %T, ожидался decimal.Decimal", b["сумма"])
			}
			got41, found = d, true
		}
	}
	if !found {
		t.Fatal("счёт 41 не найден в остатках")
	}
	if !got41.Equal(decimal.RequireFromString("2")) {
		t.Errorf("сальдо счёта 41 = %s, ожидалось ровно 2", got41)
	}

	// Обороты — тем же типом и той же точностью.
	turn, err := db.AccountTurnovers(ctx, ar.Name, "Основной", period.AddDate(0, 0, -1), asOf, ar.Resources)
	if err != nil {
		t.Fatalf("AccountTurnovers: %v", err)
	}
	for _, r := range turn {
		if code, _ := r["code"].(string); code != "41" {
			continue
		}
		d, ok := r["сумма_дт"].(decimal.Decimal)
		if !ok {
			t.Fatalf("оборот Дт: тип %T, ожидался decimal.Decimal", r["сумма_дт"])
		}
		if !d.Equal(decimal.RequireFromString("2")) {
			t.Errorf("оборот Дт счёта 41 = %s, ожидалось ровно 2", d)
		}
	}
}

// Опорные проводки свёртки наследуют точность остатков: сумма, которую нельзя
// представить в float64, должна лечь в базу без искажения.
func TestRollup_OpeningRowsKeepDecimalPrecision(t *testing.T) {
	ar := decimalAcctReg()
	db, ctx := newAccountTestDB(t, ar)
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	chart := &metadata.ChartOfAccounts{Name: "Основной", Accounts: []metadata.Account{
		{Code: "000", Name: "Вспомогательный", Kind: "active_passive"},
		{Code: "41", Name: "Товары", Kind: "active"},
		{Code: "60", Name: "Поставщики", Kind: "passive"},
	}}
	if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{chart}); err != nil {
		t.Fatal(err)
	}

	period := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "Сумма": "0.1"}}
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), rows, ar, &period); err != nil {
			t.Fatalf("WriteAccountMovements: %v", err)
		}
	}

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	aux, _ := db.resolveAuxAccount(ctx, ar.Accounts)
	rows, err := db.accountOpeningRows(ctx, ar, aux, cutoff)
	if err != nil {
		t.Fatalf("accountOpeningRows: %v", err)
	}
	var seen bool
	for _, r := range rows {
		if code, _ := r["счётдт"].(string); code != "41" {
			continue
		}
		seen = true
		d, ok := r["Сумма"].(decimal.Decimal)
		if !ok {
			t.Fatalf("опорная проводка: тип суммы %T, ожидался decimal.Decimal", r["Сумма"])
		}
		// Ровно 0.3: и агрегат SQL, и перенос в Go точные.
		if !d.Equal(decimal.RequireFromString("0.3")) {
			t.Errorf("опорная сумма = %s, ожидалось ровно 0.3", d)
		}
	}
	if !seen {
		t.Fatal("опорная проводка по счёту 41 не построена")
	}
}

// Свёртка целиком проходит на «неудобных» суммах и сохраняет остаток.
func TestRollup_AccountRegisterDecimalRoundTrip(t *testing.T) {
	ar := decimalAcctReg()
	db, ctx := newAccountTestDB(t, ar)
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	chart := &metadata.ChartOfAccounts{Name: "Основной", Accounts: []metadata.Account{
		{Code: "000", Name: "Вспомогательный", Kind: "active_passive"},
		{Code: "41", Name: "Товары", Kind: "active"},
		{Code: "60", Name: "Поставщики", Kind: "passive"},
	}}
	if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{chart}); err != nil {
		t.Fatal(err)
	}

	before := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	for _, p := range []time.Time{before, before, before, after} {
		period := p
		rows := []map[string]any{{"счётдт": "41", "счёткт": "60", "Сумма": "0.1"}}
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), rows, ar, &period); err != nil {
			t.Fatalf("WriteAccountMovements: %v", err)
		}
	}

	asOf := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	bal41 := func() decimal.Decimal {
		rows, err := db.AccountBalances(ctx, ar.Name, "Основной", asOf, ar.Resources, nil)
		if err != nil {
			t.Fatalf("AccountBalances: %v", err)
		}
		for _, b := range rows {
			if code, _ := b["code"].(string); code == "41" {
				d, _ := b["сумма"].(decimal.Decimal)
				return d
			}
		}
		return decimal.Zero
	}

	want := decimal.RequireFromString("0.4")
	if got := bal41(); !got.Equal(want) {
		t.Fatalf("до свёртки сальдо 41 = %s, ожидалось ровно %s", got, want)
	}
	if _, err := db.Rollup(ctx, nil, nil, []*metadata.AccountRegister{ar}, nil,
		RollupOptions{Date: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			AccountRegisters: []string{ar.Name}}); err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if got := bal41(); !got.Equal(want) {
		t.Errorf("после свёртки сальдо 41 = %s, ожидалось ровно %s", got, want)
	}
}
