package dbcheck

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// ensureChart заводит _accounts и план счетов plan с перечисленными кодами:
// запись проводок сверяет коды с планом счетов регистра.
func ensureChart(t *testing.T, db *storage.DB, plan string, codes ...string) {
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
