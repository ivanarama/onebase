package storage

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ensureTestChart заводит _accounts и план счетов plan с перечисленными кодами.
// Запись проводок сверяет коды с планом счетов регистра, поэтому тест, который
// пишет проводки напрямую, заводит план так же, как это делает migrate.
func ensureTestChart(t *testing.T, db *DB, plan string, codes ...string) {
	t.Helper()
	ctx := context.Background()
	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	chart := &metadata.ChartOfAccounts{Name: plan}
	for _, code := range codes {
		chart.Accounts = append(chart.Accounts, metadata.Account{Code: code, Name: code, Kind: "active_passive"})
	}
	if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{chart}); err != nil {
		t.Fatal(err)
	}
}
