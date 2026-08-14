package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

type cleanupFixture struct {
	db         *DB
	reg        *metadata.Register
	accountReg *metadata.AccountRegister
}

func newCleanupFixture(t *testing.T) cleanupFixture {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	reg := &metadata.Register{
		Name:      "RollbackAccum",
		Resources: []metadata.Field{{Name: "Quantity", Type: metadata.FieldTypeNumber}},
		Totals:    metadata.RegisterTotals{Enabled: true},
	}
	accountReg := &metadata.AccountRegister{
		Name:      "RollbackAccount",
		Accounts:  "Main",
		Resources: []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber}},
		Totals:    metadata.RegisterTotals{Enabled: true},
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatalf("MigrateRegisters: %v", err)
	}
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{accountReg}); err != nil {
		t.Fatalf("MigrateAccountRegisters: %v", err)
	}

	period := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		recorderType     string
		quantity, amount float64
	}{{"RemovedDocument", 10, 100}, {"KeepDocument", 7, 70}} {
		if err := db.WriteMovements(ctx, reg.Name, row.recorderType, uuid.New(),
			[]map[string]any{{"Quantity": row.quantity}}, reg, &period); err != nil {
			t.Fatalf("WriteMovements(%s): %v", row.recorderType, err)
		}
		if err := db.WriteAccountMovements(ctx, accountReg.Name, row.recorderType, uuid.New(),
			[]map[string]any{{accountDebitKeyInternal: "41", accountCreditKeyInternal: "60", "Amount": row.amount}},
			accountReg, &period); err != nil {
			t.Fatalf("WriteAccountMovements(%s): %v", row.recorderType, err)
		}
	}
	return cleanupFixture{db: db, reg: reg, accountReg: accountReg}
}

const (
	accountDebitKeyInternal  = "\u0441\u0447\u0451\u0442\u0434\u0442"
	accountCreditKeyInternal = "\u0441\u0447\u0451\u0442\u043a\u0442"
)

type cleanupState struct {
	accumRows           int
	accumAmount         float64
	accountRows         int
	accountAmount       float64
	accumTotalsRows     int
	accumTotalsAmount   float64
	accountTotalsRows   int
	accountTotalsDebit  float64
	accountTotalsCredit float64
}

func snapshotCleanupState(t *testing.T, f cleanupFixture) cleanupState {
	t.Helper()
	ctx := context.Background()
	var state cleanupState
	queries := []struct {
		query string
		dest  []any
	}{
		{
			"SELECT COUNT(*), COALESCE(SUM(quantity), 0) FROM " + metadata.RegisterTableName(f.reg.Name),
			[]any{&state.accumRows, &state.accumAmount},
		},
		{
			"SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM " + metadata.AccountRegTableName(f.accountReg.Name),
			[]any{&state.accountRows, &state.accountAmount},
		},
		{
			"SELECT COUNT(*), COALESCE(SUM(quantity), 0) FROM " + metadata.RegisterTotalsTableName(f.reg.Name),
			[]any{&state.accumTotalsRows, &state.accumTotalsAmount},
		},
		{
			fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(%s), 0), COALESCE(SUM(%s), 0) FROM %s",
				accountTotalsDebitCol(f.accountReg.Resources[0]), accountTotalsCreditCol(f.accountReg.Resources[0]),
				metadata.AccountRegTotalsTableName(f.accountReg.Name)),
			[]any{&state.accountTotalsRows, &state.accountTotalsDebit, &state.accountTotalsCredit},
		},
	}
	for _, q := range queries {
		if err := f.db.QueryRow(ctx, q.query).Scan(q.dest...); err != nil {
			t.Fatalf("snapshot %q: %v", q.query, err)
		}
	}
	return state
}

// If the second family cannot be deleted, the first DELETE must not leak out
// of the transaction and the caller must not report a partial deleted count.
func TestDeleteUnknownRecorderTypeMovements_RollsBackAccountDeleteFailure(t *testing.T) {
	f := newCleanupFixture(t)
	before := snapshotCleanupState(t, f)
	accountSource := f.db.accountSources(context.Background(), []*metadata.AccountRegister{f.accountReg})[0]
	if _, err := f.db.Exec(context.Background(), fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO broken_type",
		accountSource.table, accountSource.recorderTypeCol)); err != nil {
		t.Fatalf("break account recorder type column: %v", err)
	}

	deleted, err := f.db.DeleteUnknownRecorderTypeMovementsAndRecalcTotals(context.Background(),
		[]*metadata.Register{f.reg}, []*metadata.AccountRegister{f.accountReg}, []string{"RemovedDocument"})
	if err == nil {
		t.Fatal("cleanup unexpectedly succeeded with a broken account table")
	}
	if deleted != 0 {
		t.Errorf("deleted = %d after rollback, want 0", deleted)
	}
	after := snapshotCleanupState(t, f)
	if after != before {
		t.Fatalf("state changed after account DELETE failure:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// A failure in the final (account) totals rebuild happens after both DELETEs
// and the accumulation totals rebuild. All of those earlier writes must roll
// back together with the failed rebuild.
func TestDeleteUnknownRecorderTypeMovements_RollsBackSecondTotalsFailure(t *testing.T) {
	f := newCleanupFixture(t)
	before := snapshotCleanupState(t, f)
	totalsTable := metadata.AccountRegTotalsTableName(f.accountReg.Name)
	trigger := "CREATE TRIGGER fail_account_totals_recalc BEFORE DELETE ON " + totalsTable +
		" BEGIN SELECT RAISE(ABORT, 'forced account totals recalc failure'); END"
	if _, err := f.db.Exec(context.Background(), trigger); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	deleted, err := f.db.DeleteUnknownRecorderTypeMovementsAndRecalcTotals(context.Background(),
		[]*metadata.Register{f.reg}, []*metadata.AccountRegister{f.accountReg}, []string{"RemovedDocument"})
	if err == nil {
		t.Fatal("cleanup unexpectedly succeeded despite the totals failure trigger")
	}
	if deleted != 0 {
		t.Errorf("deleted = %d after rollback, want 0", deleted)
	}
	after := snapshotCleanupState(t, f)
	if after != before {
		t.Fatalf("state changed after second totals rebuild failed:\nbefore=%+v\nafter=%+v", before, after)
	}
}
