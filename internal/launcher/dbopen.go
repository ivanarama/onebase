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
	lease, b, err := acquireBaseReadLease(ctx, b)
	if err != nil {
		return nil, err
	}
	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	db.AddCloseHook(lease.Close)
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
	err := db.CheckSchemaRevision(ctx)
	if err == nil {
		return nil
	}
	var newer *storage.NewerSchemaError
	if errors.As(err, &newer) && os.Getenv(storage.AllowNewerSchemaEnv) != "" {
		oblog.Component("launcher").Warn("база обслуживалась платформой новее этого бинаря — открыта по явному разрешению",
			"ревизия_базы", newer.Base,
			"ревизия_бинаря", newer.Known,
			"обслужил", newer.UpdatedBy)
		return nil
	}
	return fmt.Errorf("launcher: %w", err)
}

func acquireBaseReadLease(ctx context.Context, b *Base) (dblock.Lease, *Base, error) {
	if b == nil {
		return nil, nil, fmt.Errorf("launcher: base is nil")
	}
	copyBase := *b
	switch copyBase.DBType {
	case "sqlite":
		if copyBase.DBPath == "" {
			return nil, nil, fmt.Errorf("launcher: sqlite base %q has empty db_path", copyBase.Name)
		}
		lease, canonical, err := dblock.AcquireSQLiteSharedTarget(copyBase.DBPath)
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
			lease, canonical, err := dblock.AcquireSQLiteSharedTarget(path)
			if err != nil {
				return nil, nil, fmt.Errorf("launcher: database lifetime lock: %w", err)
			}
			copyBase.DBPath = canonical
			return lease, &copyBase, nil
		}
		lease, err := dblock.AcquirePostgresShared(ctx, copyBase.DB)
		if err != nil {
			return nil, nil, fmt.Errorf("launcher: database lifetime lock: %w", err)
		}
		return lease, &copyBase, nil
	default:
		return nil, nil, fmt.Errorf("launcher: unknown db_type %q", copyBase.DBType)
	}
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
func openDBForRestore(ctx context.Context, b *Base) (*storage.DB, error) {
	if b == nil || !cfgDBExclusiveLeaseHeld(ctx, b.ID) {
		return nil, fmt.Errorf("launcher: restore database open requires the exclusive configurator lease")
	}
	return openDBUnchecked(ctx, b)
}
