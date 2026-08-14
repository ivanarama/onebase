package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// CountMovementsAndAccountEntriesOfRecorderType returns a consistent dry-run
// count across accumulation and account registers.
func (db *DB) CountMovementsAndAccountEntriesOfRecorderType(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
	recorderTypes []string,
) (int64, error) {
	var total int64
	err := db.WithReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		movements, err := db.CountMovementsOfRecorderType(snapshotCtx, registers, recorderTypes)
		if err != nil {
			return fmt.Errorf("count accumulation movements: %w", err)
		}
		entries, err := db.CountAccountEntriesOfRecorderType(snapshotCtx, accountRegisters, recorderTypes)
		if err != nil {
			return fmt.Errorf("count account entries: %w", err)
		}
		total = movements + entries
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// DeleteUnknownRecorderTypeMovementsAndRecalcTotals atomically removes the
// explicitly named recorder types from both movement families and rebuilds
// every enabled total. A failure returns zero because the transaction restores
// all movements and totals.
func (db *DB) DeleteUnknownRecorderTypeMovementsAndRecalcTotals(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
	recorderTypes []string,
) (int64, error) {
	return db.deleteMovementFamiliesAndRecalcTotals(ctx, registers, accountRegisters,
		func(txCtx context.Context) (int64, error) {
			movements, err := db.DeleteMovementsOfUnknownRecorderType(txCtx, registers, recorderTypes)
			if err != nil {
				return 0, fmt.Errorf("delete accumulation movements: %w", err)
			}
			entries, err := db.DeleteAccountEntriesOfUnknownRecorderType(txCtx, accountRegisters, recorderTypes)
			if err != nil {
				return 0, fmt.Errorf("delete account entries: %w", err)
			}
			return movements + entries, nil
		})
}

// DeleteOrphanMovementsAndRecalcTotals atomically deletes real orphans from
// both movement families and rebuilds every enabled total. Unknown recorder
// types remain untouched by the lower-level orphan routines.
func (db *DB) DeleteOrphanMovementsAndRecalcTotals(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
	entities []*metadata.Entity,
) (int64, error) {
	return db.deleteMovementFamiliesAndRecalcTotals(ctx, registers, accountRegisters,
		func(txCtx context.Context) (int64, error) {
			movements, err := db.DeleteOrphanMovements(txCtx, registers, entities)
			if err != nil {
				return 0, fmt.Errorf("delete orphan accumulation movements: %w", err)
			}
			entries, err := db.DeleteOrphanAccountEntries(txCtx, accountRegisters, entities)
			if err != nil {
				return 0, fmt.Errorf("delete orphan account entries: %w", err)
			}
			return movements + entries, nil
		})
}

func (db *DB) deleteMovementFamiliesAndRecalcTotals(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
	deleteRows func(context.Context) (int64, error),
) (int64, error) {
	var deleted int64
	err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		// Posting obtains the same locks. Taking the complete sorted set before
		// the first DELETE prevents partial writes and cross-family lock-order
		// deadlocks.
		if err := db.lockMovementTotals(txCtx, registers, accountRegisters); err != nil {
			return err
		}
		var err error
		deleted, err = deleteRows(txCtx)
		if err != nil {
			return err
		}
		if err := db.recalcMovementTotalsInTx(txCtx, registers, accountRegisters); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (db *DB) lockMovementTotals(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
) error {
	keys := make([]string, 0, len(registers)+len(accountRegisters))
	for _, reg := range registers {
		if reg != nil && reg.TotalsUsable() {
			keys = append(keys, "register-totals|"+strings.ToLower(reg.Name))
		}
	}
	for _, reg := range accountRegisters {
		if reg != nil && reg.TotalsUsable() {
			keys = append(keys, "account-totals|"+strings.ToLower(reg.Name))
		}
	}
	if err := db.AdvisoryXactLock(ctx, keys); err != nil {
		return fmt.Errorf("lock movement totals: %w", err)
	}
	return nil
}

func (db *DB) recalcMovementTotalsInTx(
	ctx context.Context,
	registers []*metadata.Register,
	accountRegisters []*metadata.AccountRegister,
) error {
	for _, reg := range registers {
		if reg == nil || !reg.TotalsUsable() {
			continue
		}
		if err := db.recalcRegisterTotalsInTx(ctx, reg); err != nil {
			return fmt.Errorf("recalc accumulation totals %s: %w", reg.Name, err)
		}
	}
	for _, reg := range accountRegisters {
		if reg == nil || !reg.TotalsUsable() {
			continue
		}
		if err := db.recalcAccountTotalsInTx(ctx, reg); err != nil {
			return fmt.Errorf("recalc account totals %s: %w", reg.Name, err)
		}
	}
	return nil
}
