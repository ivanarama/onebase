package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/dblock"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

// OpenDB opens a storage.DB for the given base, routing by DBType.
// Defaults to SQLite when db_type is empty and db is empty (backward compat).
func OpenDB(ctx context.Context, b *Base) (*storage.DB, error) {
	lease, b, err := acquireBaseLease(ctx, b, true)
	if err != nil {
		return nil, err
	}
	ownedLease := true
	defer func() {
		if ownedLease {
			_ = lease.Close()
		}
	}()
	if err := checkNoPendingRestoreBeforeOpen(ctx, b); err != nil {
		return nil, err
	}
	state, err := probeBaseSchemaRevision(ctx, b)
	if err != nil {
		return nil, err
	}
	if state.Known && state.Revision < 0 {
		return nil, checkSchemaRevisionState(state)
	}
	prepareAfterOpen := baseUsesInMemorySQLite(b) && state.NeedsUpgrade()
	for state.NeedsUpgrade() && !prepareAfterOpen {
		if state.Known && state.Revision < 0 {
			return nil, checkSchemaRevisionState(state)
		}
		if err := lease.Close(); err != nil {
			return nil, fmt.Errorf("launcher: release shared database lifetime lock for schema upgrade: %w", err)
		}
		ownedLease = false
		lease, b, err = acquireBaseLease(ctx, b, false)
		if err != nil {
			return nil, fmt.Errorf("launcher: schema revision upgrade requires exclusive database access: %w", err)
		}
		ownedLease = true
		if err := checkNoPendingRestoreBeforeOpen(ctx, b); err != nil {
			return nil, err
		}
		state, err = probeBaseSchemaRevision(ctx, b)
		if err != nil {
			return nil, err
		}
		if state.Known {
			if err := checkSchemaRevisionState(state); err != nil {
				return nil, err
			}
		}
		if state.NeedsUpgrade() {
			state, err = prepareBaseSchemaRevision(ctx, b)
			if err != nil {
				return nil, err
			}
		}
		if err := checkSchemaRevisionState(state); err != nil {
			return nil, err
		}
		if err := lease.Downgrade(ctx); err != nil {
			return nil, fmt.Errorf("launcher: downgrade schema-upgrade database lease: %w", err)
		}
		// Downgrade may briefly release the primitive. Re-read both durable gates
		// under the resulting shared lease before normal Connect can mutate the DB;
		// a restore that replaced the generation in the gap repeats the transition.
		if err := checkNoPendingRestoreBeforeOpen(ctx, b); err != nil {
			return nil, err
		}
		state, err = probeBaseSchemaRevision(ctx, b)
		if err != nil {
			return nil, err
		}
	}
	if err := checkSchemaRevisionState(state); err != nil {
		return nil, err
	}

	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		return nil, err
	}
	if prepareAfterOpen {
		if _, err := db.RaiseSchemaRevision(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("launcher: publish schema revision: %w", err)
		}
	}
	db.AddCloseHook(lease.Close)
	ownedLease = false
	if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("launcher: database has an interrupted restore: %w", err)
	}
	// Ревизия схемы (#1057). Флага командной строки у лаунчера нет — осознанный
	// обход задаётся только переменной окружения, и ровно она названа в тексте
	// отказа.
	if err := checkSchemaRevision(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// checkSchemaRevision — гейт ревизии для лаунчера: база, обслуженная платформой
// новее, не открывается, пока обход не разрешён явно.
func checkSchemaRevision(ctx context.Context, db *storage.DB) error {
	return handleSchemaRevisionError(db.CheckSchemaRevision(ctx))
}

func checkSchemaRevisionState(state storage.SchemaRevisionState) error {
	return handleSchemaRevisionError(state.Check())
}

func handleSchemaRevisionError(err error) error {
	if err == nil {
		return nil
	}
	var newer *storage.NewerSchemaError
	if errors.As(err, &newer) && storage.AllowNewerSchemaByEnv() {
		oblog.Component("launcher").Warn("база обслуживалась платформой новее этого бинаря — открыта по явному разрешению",
			"ревизия_базы", newer.Base,
			"ревизия_бинаря", newer.Known,
			"обслужил", newer.UpdatedBy)
		return nil
	}
	return fmt.Errorf("launcher: %w", err)
}

func acquireBaseLease(ctx context.Context, b *Base, shared bool) (dblock.Lease, *Base, error) {
	if b == nil {
		return nil, nil, fmt.Errorf("launcher: base is nil")
	}
	copyBase := *b
	switch copyBase.DBType {
	case "sqlite":
		if copyBase.DBPath == "" {
			return nil, nil, fmt.Errorf("launcher: sqlite base %q has empty db_path", copyBase.Name)
		}
		var lease dblock.Lease
		var canonical string
		var err error
		if shared {
			lease, canonical, err = dblock.AcquireSQLiteSharedTarget(copyBase.DBPath)
		} else {
			lease, canonical, err = dblock.AcquireSQLiteTarget(copyBase.DBPath)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("launcher: database lifetime lock: %w", err)
		}
		copyBase.DBPath = canonical
		return lease, &copyBase, nil
	case "", "postgres":
		if copyBase.DB == "" {
			path := copyBase.DBPath
			if path == "" {
				path = os.TempDir() + string(os.PathSeparator) + "onebase_" + copyBase.ID + ".db"
			}
			var lease dblock.Lease
			var canonical string
			var err error
			if shared {
				lease, canonical, err = dblock.AcquireSQLiteSharedTarget(path)
			} else {
				lease, canonical, err = dblock.AcquireSQLiteTarget(path)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("launcher: database lifetime lock: %w", err)
			}
			copyBase.DBPath = canonical
			return lease, &copyBase, nil
		}
		var lease dblock.Lease
		var err error
		if shared {
			lease, err = dblock.AcquirePostgresShared(ctx, copyBase.DB)
		} else {
			lease, err = dblock.AcquirePostgres(ctx, copyBase.DB)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("launcher: database lifetime lock: %w", err)
		}
		return lease, &copyBase, nil
	default:
		return nil, nil, fmt.Errorf("launcher: unknown db_type %q", copyBase.DBType)
	}
}

func baseUsesSQLite(b *Base) bool {
	return b.DBType == "sqlite" || ((b.DBType == "" || b.DBType == "postgres") && b.DB == "")
}

func baseUsesInMemorySQLite(b *Base) bool {
	return baseUsesSQLite(b) && storage.IsInMemorySQLitePath(b.DBPath)
}

func checkNoPendingRestoreBeforeOpen(ctx context.Context, b *Base) error {
	pending, err := hasPendingRestoreBeforeOpen(ctx, b)
	if err != nil {
		return fmt.Errorf("launcher: inspect restore marker before database open: %w", err)
	}
	if pending {
		return fmt.Errorf("launcher: database has an interrupted restore: %w: interrupted restore marker exists", backup.ErrRestoreRecoveryRequired)
	}
	return nil
}

func hasPendingRestoreBeforeOpen(ctx context.Context, b *Base) (bool, error) {
	if baseUsesSQLite(b) {
		return backup.HasPendingRestoreSQLite(ctx, b.DBPath)
	}
	return backup.HasPendingRestorePostgres(ctx, b.DB)
}

func probeBaseSchemaRevision(ctx context.Context, b *Base) (storage.SchemaRevisionState, error) {
	if baseUsesSQLite(b) {
		return storage.ProbeSQLiteSchemaRevision(ctx, b.DBPath)
	}
	return storage.ProbePostgresSchemaRevision(ctx, b.DB)
}

func prepareBaseSchemaRevision(ctx context.Context, b *Base) (storage.SchemaRevisionState, error) {
	if baseUsesSQLite(b) {
		return storage.PrepareSQLiteSchemaRevision(ctx, b.DBPath)
	}
	return storage.PreparePostgresSchemaRevision(ctx, b.DB)
}

// publishBaseSchemaRevisionExclusive checks or atomically publishes the
// minimum-reader barrier while the caller owns the exclusive database lifetime
// lease. It runs before openDBUnchecked so future databases are never touched by
// normal SQLite pragmas or PostgreSQL compatibility DDL before the refusal.
func publishBaseSchemaRevisionExclusive(ctx context.Context, b *Base) error {
	state, err := probeBaseSchemaRevision(ctx, b)
	if err != nil {
		return err
	}
	if state.Known {
		if err := checkSchemaRevisionState(state); err != nil {
			return err
		}
	}
	if state.NeedsUpgrade() {
		state, err = prepareBaseSchemaRevision(ctx, b)
		if err != nil {
			return err
		}
	}
	return checkSchemaRevisionState(state)
}

// openDBWithExclusiveSchemaGate is for launcher operations that already own
// both the configurator-exclusive gate and the cross-process exclusive database
// lease. It must not be used by ordinary consumers, which go through OpenDB.
func openDBWithExclusiveSchemaGate(ctx context.Context, b *Base) (*storage.DB, error) {
	if b == nil || !cfgDBExclusiveLeaseHeld(ctx, b.ID) {
		return nil, fmt.Errorf("launcher: exclusive database open requires the configurator lease")
	}
	if err := checkNoPendingRestoreBeforeOpen(ctx, b); err != nil {
		return nil, err
	}
	if err := publishBaseSchemaRevisionExclusive(ctx, b); err != nil {
		return nil, err
	}
	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		return nil, err
	}
	if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("launcher: database has an interrupted restore: %w", err)
	}
	if err := checkSchemaRevision(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// openDBUnchecked only opens the physical database. Every ordinary launcher
// consumer must use OpenDB so a durable restore marker fails closed until
// startup recovery resolves it.
func openDBUnchecked(ctx context.Context, b *Base) (*storage.DB, error) {
	switch b.DBType {
	case "sqlite":
		if b.DBPath == "" {
			return nil, fmt.Errorf("launcher: sqlite base %q has empty db_path", b.Name)
		}
		return storage.ConnectSQLite(ctx, b.DBPath)
	case "", "postgres":
		// backward-compat: пустой db_type и пустой db → SQLite
		if b.DB == "" {
			dbPath := b.DBPath
			if dbPath == "" {
				dbPath = os.TempDir() + string(os.PathSeparator) + "onebase_" + b.ID + ".db"
			}
			return storage.ConnectSQLite(ctx, dbPath)
		}
		return storage.Connect(ctx, b.DB)
	default:
		return nil, fmt.Errorf("launcher: unknown db_type %q", b.DBType)
	}
}

// openDBForRestore is the narrow escape hatch used by universal import, which
// must open the database in order to resolve a durable marker. Requiring the
// cfg-exclusive lease in the context makes accidental use by ordinary
// configurator handlers fail closed instead of relying on caller convention.
func openDBForRestore(ctx context.Context, b *Base, allowedDestinations ...string) (*storage.DB, error) {
	if b == nil || !cfgDBExclusiveLeaseHeld(ctx, b.ID) {
		return nil, fmt.Errorf("launcher: restore database open requires the exclusive configurator lease")
	}
	pending, err := hasPendingRestoreBeforeOpen(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("launcher: inspect restore marker before recovery open: %w", err)
	}
	state, err := probeBaseSchemaRevision(ctx, b)
	if err != nil {
		return nil, err
	}
	// A pending journal does not authorize an older binary to interpret a
	// generation explicitly marked as future. A missing marker or an incomplete
	// marker paired with that trusted journal is recovered first and published
	// atomically with intent deletion.
	if state.Known {
		if err := checkSchemaRevisionState(state); err != nil {
			return nil, err
		}
	}
	if !pending {
		if err := publishBaseSchemaRevisionExclusive(ctx, b); err != nil {
			return nil, err
		}
	}
	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		return nil, err
	}
	if pending {
		destinations := append([]string(nil), allowedDestinations...)
		if db.FilesDir() != "" {
			destinations = append(destinations, db.FilesDir())
		}
		if err := backup.RecoverPendingRestore(ctx, db, destinations...); err != nil {
			db.Close()
			return nil, fmt.Errorf("launcher: recover interrupted restore before schema gate: %w", err)
		}
	}
	state, err = db.SchemaRevisionStateOf(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	if state.Known {
		if err := checkSchemaRevisionState(state); err != nil {
			db.Close()
			return nil, err
		}
	}
	if state.NeedsUpgrade() {
		if _, err := db.RaiseSchemaRevision(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("launcher: publish schema revision after recovery: %w", err)
		}
	}
	if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("launcher: database has an interrupted restore after recovery: %w", err)
	}
	if err := checkSchemaRevision(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
