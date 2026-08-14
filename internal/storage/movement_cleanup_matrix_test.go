package storage_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

const (
	accountDebitKey  = "\u0441\u0447\u0451\u0442\u0434\u0442"
	accountCreditKey = "\u0441\u0447\u0451\u0442\u043a\u0442"
)

// The destructive forget workflow must treat accumulation movements and
// account entries as one unit and rebuild both totals families afterwards.
func TestDeleteUnknownRecorderTypeMovementsAndRecalcTotals_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name:      "CleanupAccum",
			Resources: []metadata.Field{{Name: "Quantity", Type: metadata.FieldTypeNumber}},
			Totals:    metadata.RegisterTotals{Enabled: true},
		}
		accountReg := &metadata.AccountRegister{
			Name:      "CleanupAccount",
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
		write := func(recorderType string, quantity, amount float64) {
			t.Helper()
			if err := db.WriteMovements(ctx, reg.Name, recorderType, uuid.New(),
				[]map[string]any{{"Quantity": quantity}}, reg, &period); err != nil {
				t.Fatalf("WriteMovements(%s): %v", recorderType, err)
			}
			if err := db.WriteAccountMovements(ctx, accountReg.Name, recorderType, uuid.New(),
				[]map[string]any{{accountDebitKey: "41", accountCreditKey: "60", "Amount": amount}},
				accountReg, &period); err != nil {
				t.Fatalf("WriteAccountMovements(%s): %v", recorderType, err)
			}
		}
		write("RemovedDocument", 10, 100)
		write("KeepDocument", 7, 70)

		count, err := db.CountMovementsAndAccountEntriesOfRecorderType(ctx,
			[]*metadata.Register{reg}, []*metadata.AccountRegister{accountReg}, []string{"RemovedDocument"})
		if err != nil {
			t.Fatalf("CountMovementsAndAccountEntriesOfRecorderType: %v", err)
		}
		if count != 2 {
			t.Fatalf("dry-run count = %d, want 2", count)
		}

		deleted, err := db.DeleteUnknownRecorderTypeMovementsAndRecalcTotals(ctx,
			[]*metadata.Register{reg}, []*metadata.AccountRegister{accountReg}, []string{"RemovedDocument"})
		if err != nil {
			t.Fatalf("DeleteUnknownRecorderTypeMovementsAndRecalcTotals: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("deleted = %d, want 2", deleted)
		}

		assertTableCount(t, db, metadata.RegisterTableName(reg.Name), 1)
		assertTableCount(t, db, metadata.AccountRegTableName(accountReg.Name), 1)
		remaining, err := db.CountMovementsAndAccountEntriesOfRecorderType(ctx,
			[]*metadata.Register{reg}, []*metadata.AccountRegister{accountReg}, []string{"RemovedDocument"})
		if err != nil {
			t.Fatalf("count removed type after cleanup: %v", err)
		}
		if remaining != 0 {
			t.Errorf("removed recorder type still has %d movements", remaining)
		}

		assertNumericSum(t, db, metadata.RegisterTotalsTableName(reg.Name), "quantity", 7)
		assertNumericSum(t, db, metadata.AccountRegTotalsTableName(accountReg.Name), "amount_\u0434\u0442", 70)
		assertNumericSum(t, db, metadata.AccountRegTotalsTableName(accountReg.Name), "amount_\u043a\u0442", 70)
	})
}

func assertTableCount(t *testing.T, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Errorf("%s row count = %d, want %d", table, got, want)
	}
}

func assertNumericSum(t *testing.T, db *storage.DB, table, column string, want float64) {
	t.Helper()
	var got float64
	query := fmt.Sprintf("SELECT COALESCE(SUM(%s), 0) FROM %s", column, table)
	if err := db.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("sum %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Errorf("sum %s.%s = %v, want %v", table, column, got, want)
	}
}
