package backup

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/storage"
)

// authTables — таблицы, пропускаемые при демо-сбросе.
// Сессии не импортируем — пользователю всё равно нужно логиниться заново.
// Историю запусков регл.заданий оставляем.
// Пользователи, роли и связи импортируются из бэкапа — демо-сайт должен
// показывать тех же пользователей, что и в исходной конфигурации.
// Системные таблицы (_users, _roles, _user_roles) импортируются в явном
// порядке зависимостей, а не в алфавитном — чтобы DELETE FROM _users не
// уничтожил только что импортированные _user_roles через ON DELETE CASCADE.
var authTables = map[string]bool{
	"_sessions":       true,
	"_scheduled_runs": true,
}

// DemoReset восстанавливает все данные из .obz бэкапа (бизнес-данные,
// конфигурацию, пользователей и роли), пропуская сессии и историю
// регламентных заданий.  Системные таблицы импортируются в порядке
// зависимостей (_users → _roles → _user_roles), чтобы FK CASCADE
// не уничтожил только что импортированные связи.
// Если backupPath пуст — ничего не делает.
func DemoReset(ctx context.Context, db *storage.DB, backupPath string) (report *ImportReport, resultErr error) {
	report = &ImportReport{Tables: make(map[string]int)}

	if backupPath == "" {
		return report, nil
	}
	restoreOperationMu.Lock()
	defer restoreOperationMu.Unlock()
	if err := rejectSQLiteInsideRestoreTree(db, db.FilesDir(), "attachment"); err != nil {
		return report, fmt.Errorf("demo reset: %w", err)
	}

	f, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("demo reset: open backup %q: %w", backupPath, err)
	}
	defer closeRead("файл резервной копии", f)

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("demo reset: stat backup: %w", err)
	}

	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("demo reset: open zip: %w", err)
	}
	if err := validateUniversalArchive(zr); err != nil {
		return nil, fmt.Errorf("demo reset: %w", err)
	}

	meta, err := readMeta(zr)
	if err != nil {
		return nil, err
	}
	if meta["format"] != "universal" {
		return nil, ErrLegacyFormat
	}

	tmpDir, err := os.MkdirTemp("", "onebase-demo-reset-*")
	if err != nil {
		return nil, err
	}
	defer removeTemp(tmpDir)

	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		outPath := filepath.Join(tmpDir, filepath.FromSlash(zf.Name))
		// Zip-slip guard (как в universal.go): путь распаковки не должен выходить
		// за пределы tmpDir. Источник .obz здесь — локальный файл, но защита от
		// «../» в именах записей архива нужна и тут — для консистентности.
		if rel, err := filepath.Rel(tmpDir, outPath); err != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("недопустимый путь в архиве: %s", zf.Name)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), fsmode.Dir); err != nil {
			return nil, err
		}
		if err := extractFile(zf, outPath); err != nil {
			return nil, err
		}
	}
	if err := validateUniversalManifest(tmpDir); err != nil {
		return report, fmt.Errorf("demo reset: %w", err)
	}
	if err := rejectUniversalArchiveS3References(tmpDir); err != nil {
		return report, fmt.Errorf("demo reset: %w", err)
	}
	manifest, err := readUniversalManifest(filepath.Join(tmpDir, "manifest.json"))
	if err != nil {
		return report, fmt.Errorf("demo reset manifest: %w", err)
	}
	configDir := filepath.Join(tmpDir, "config")
	if err := validateExtractedConfig(configDir); err != nil {
		return report, fmt.Errorf("demo reset config: %w", err)
	}
	opCtx, cancelOperation := detachedRestoreContext(ctx)
	defer cancelOperation()
	durableSession, err := db.BeginDurableSession(opCtx)
	if err != nil {
		return report, fmt.Errorf("demo reset: begin durable database session: %w", err)
	}
	opCtx = durableSession.Context()
	defer func() {
		if err := durableSession.Close(); err != nil {
			backupLog().Warn("demo reset: failed to restore database durability mode", "err", err)
		}
	}()
	// Recovery must run before inspecting or staging the current destination:
	// a previous pending operation may have temporarily changed whether it exists.
	if err := recoverPendingRestoreLocked(opCtx, db, db.FilesDir()); err != nil {
		return report, fmt.Errorf("demo reset: recover previous restore: %w", err)
	}

	attachmentsSrc := filepath.Join(tmpDir, "attachments")
	if _, statErr := os.Stat(attachmentsSrc); os.IsNotExist(statErr) {
		attachmentsSrc = ""
	} else if statErr != nil {
		return report, fmt.Errorf("demo reset: inspect attachments: %w", statErr)
	}
	fileSwap, err := prepareDirectorySwap(ctx, attachmentsSrc, db.FilesDir(), fsmode.SecretFile, nil)
	if err != nil {
		return report, fmt.Errorf("demo reset: prepare attachment snapshot: %w", err)
	}
	unownedSwap := true
	defer func() {
		if unownedSwap {
			resultErr = errors.Join(resultErr, fileSwap.Rollback())
		}
	}()
	intent, err := newRestoreIntent(db, []*directorySwap{fileSwap})
	if err != nil {
		return report, err
	}
	if err := intent.Begin(opCtx); err != nil {
		return report, err
	}
	unownedSwap = false
	intentCleanupNeeded := true
	rollbackFiles := func() error {
		err := intent.Rollback(opCtx, []*directorySwap{fileSwap})
		if err == nil {
			intentCleanupNeeded = false
		}
		return err
	}
	defer func() {
		if intentCleanupNeeded {
			resultErr = errors.Join(resultErr, rollbackFiles())
		}
	}()

	fkTransactional := !db.IsSQLite()
	var fkCleanup func() error
	fkDisabled := false
	defer func() {
		if fkDisabled {
			resultErr = errors.Join(resultErr, fkCleanup())
		}
	}()
	if !fkTransactional {
		fkCleanup, err = db.DisableFKForImport(opCtx)
		if err != nil {
			return report, errors.Join(fmt.Errorf("demo reset: disable FK: %w", err), rollbackFiles())
		}
		fkDisabled = true
	}
	// Keep the database outcome at least as durable as the filesystem swap. The
	// SQLite durable session has been pinned since before recovery/intent.Begin.
	tx, txCtx, err := db.BeginTx(opCtx)
	if err != nil {
		return report, errors.Join(fmt.Errorf("demo reset: begin transaction: %w", err), rollbackFiles())
	}
	txOpen := true
	defer func() {
		if txOpen {
			resultErr = errors.Join(resultErr, tx.Rollback(txCtx))
		}
	}()
	ctx = txCtx
	if fkTransactional {
		fkCleanup, err = db.DisableFKForImport(ctx)
		if err != nil {
			txOpen = false
			return report, errors.Join(fmt.Errorf("demo reset: disable FK: %w", err), tx.Rollback(ctx), rollbackFiles())
		}
		fkDisabled = true
	}

	// Импортируем конфигурацию из config/ (каталоги, формы, отчёты и т.д.).
	// Для --config-source database конфиг запишется в _onebase_config.
	if err := importConfig(ctx, db, "database", "", configDir); err != nil {
		return report, fmt.Errorf("demo reset config: %w", err)
	}
	if err := restorePreMigrationSystemTables(ctx, db, filepath.Join(tmpDir, "system"), report); err != nil {
		return report, fmt.Errorf("demo reset pre-migration system state: %w", err)
	}
	if err := migrateSchema(ctx, db, "database", ""); err != nil {
		return report, fmt.Errorf("demo reset schema: %w", err)
	}
	if err := clearDemoSnapshotTables(ctx, db); err != nil {
		return report, err
	}

	// Импортируем data/, пропуская таблицы авторизации
	dataDir := filepath.Join(tmpDir, "data")
	if _, err := os.Stat(dataDir); err == nil {
		if err := importDir(ctx, db, dataDir, report, authTables); err != nil {
			return report, fmt.Errorf("demo reset data: %w", err)
		}
	}

	// Импортируем system/ в порядке зависимостей: сначала _users, потом _roles,
	// последним _user_roles.  filepath.WalkDir даёт алфавитный порядок, при
	// котором _users идёт ПОСЛЕ _user_roles — и DELETE FROM _users через
	// ON DELETE CASCADE уничтожает только что импортированные связи.
	// Явный порядок гарантирует, что _user_roles всегда импортируется последним.
	sysDir := filepath.Join(tmpDir, "system")
	if _, err := os.Stat(sysDir); err == nil {
		sysOrder := append([]string(nil), systemTables...)
		for _, tbl := range sysOrder {
			if authTables[tbl] || preMigrationSystemTables[tbl] {
				continue
			}
			fp := filepath.Join(sysDir, tbl+".jsonl")
			if _, err := os.Stat(fp); err != nil {
				continue // файла нет — пропускаем
			}
			n, err := importTableJSONL(ctx, db, tbl, fp)
			if err != nil {
				return report, fmt.Errorf("demo reset system %s: %w", tbl, err)
			}
			report.Tables[tbl] = n
		}
		// Подбираем оставшиеся системные таблицы, не вошедшие в sysOrder
		// (например, _scheduled_runs если он не в authTables, или новые таблицы).
		if err := importDir(ctx, db, sysDir, report, authTables, sysOrder); err != nil {
			return report, fmt.Errorf("demo reset system rest: %w", err)
		}
	}

	if err := clearPortableSettings(ctx, db); err != nil {
		return report, fmt.Errorf("demo reset settings: clear old values: %w", err)
	}
	settingsFile := filepath.Join(tmpDir, "settings", "safe.jsonl")
	if _, err := os.Stat(settingsFile); err == nil {
		n, err := importSafeSettings(ctx, db, settingsFile, false)
		if err != nil {
			return report, fmt.Errorf("demo reset settings: %w", err)
		}
		if n > 0 {
			report.Tables["_settings"] = n
		}
	}
	if err := verifyDemoImportedCounts(manifest, report); err != nil {
		return report, err
	}
	if err := resetExchangeSecretsAndCloneState(ctx, db, ExchangeRestoreClone); err != nil {
		return report, fmt.Errorf("demo reset: isolate exchange state: %w", err)
	}
	if err := db.SaveNetworkEnabled(ctx, false); err != nil {
		return report, fmt.Errorf("demo reset: disable network access: %w", err)
	}
	if err := db.SaveExecEnabled(ctx, false); err != nil {
		return report, fmt.Errorf("demo reset: disable OS commands: %w", err)
	}
	reset, err := disableUnreadableTOTP(ctx, db)
	if err != nil {
		return report, fmt.Errorf("demo reset: reset unreadable second factor: %w", err)
	}
	report.TOTPReset = reset
	if err := rebuildSearchIndex(ctx, db, "database", ""); err != nil {
		return report, fmt.Errorf("demo reset: rebuild search index: %w", err)
	}
	if fkTransactional {
		if err := fkCleanup(); err != nil {
			return report, fmt.Errorf("demo reset: restore and validate FK constraints: %w", err)
		}
		fkDisabled = false
	}
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		return report, fmt.Errorf("demo reset: publish schema revision: %w", err)
	}
	if err := intent.MarkCommitted(ctx); err != nil {
		txOpen = false
		rollbackErr := tx.Rollback(ctx)
		if fkTransactional {
			fkDisabled = false
		}
		return report, errors.Join(err, rollbackErr, rollbackFiles())
	}
	if err := fileSwap.Publish(); err != nil {
		txOpen = false
		rollbackErr := tx.Rollback(ctx)
		if fkTransactional {
			fkDisabled = false
		}
		return report, errors.Join(fmt.Errorf("demo reset: publish attachment snapshot: %w", err), rollbackErr, rollbackFiles())
	}
	if err := tx.Commit(ctx); err != nil {
		txOpen = false
		if fkTransactional {
			fkDisabled = false
		}
		// From this point the transaction outcome is unknown. Only the fresh
		// transaction barrier in ResolveCommitError may decide file direction.
		intentCleanupNeeded = false
		return report, intent.ResolveCommitError(opCtx, []*directorySwap{fileSwap},
			fmt.Errorf("demo reset: commit database snapshot: %w", err))
	}
	txOpen = false
	if n, ok := manifest["attachments/"]; ok {
		report.Files = n
	}

	var fkErr error
	if fkDisabled {
		fkErr = fkCleanup()
	}
	fkDisabled = false
	intentCleanupNeeded = false
	fileErr := intent.Finalize(opCtx, []*directorySwap{fileSwap})
	return report, errors.Join(wrapDemoResetError("restore FK", fkErr), wrapDemoResetError("cleanup previous attachment snapshot", fileErr))
}

func clearDemoSnapshotTables(ctx context.Context, db *storage.DB) error {
	appTables, err := listAppTables(ctx, db)
	if err != nil {
		return fmt.Errorf("demo reset: list application tables: %w", err)
	}
	for _, tableName := range appTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+quotedIdent(db, tableName)); err != nil {
			return fmt.Errorf("demo reset: clear application table %s: %w", tableName, err)
		}
	}
	for _, tableName := range append(append([]string(nil), systemTables...), "_sessions", "_api_tokens", "_auth_bind_tickets", "_webhook_log") {
		if tableName == "_scheduled_runs" || preMigrationSystemTables[tableName] {
			continue
		}
		exists, err := tableExistsChecked(ctx, db, tableName)
		if err != nil {
			return fmt.Errorf("demo reset: inspect table %s: %w", tableName, err)
		}
		if exists {
			if _, err := db.Exec(ctx, "DELETE FROM "+quotedIdent(db, tableName)); err != nil {
				return fmt.Errorf("demo reset: clear table %s: %w", tableName, err)
			}
		}
	}
	return nil
}

func verifyDemoImportedCounts(manifest map[string]int, report *ImportReport) error {
	for key, expected := range manifest {
		if key == "settings/safe.jsonl" || key == "attachments/" || strings.HasPrefix(key, "exchange/") {
			continue
		}
		tableName := strings.TrimSuffix(filepath.Base(key), ".jsonl")
		if authTables[tableName] {
			continue
		}
		actual, ok := report.Tables[tableName]
		if !ok || actual != expected {
			return fmt.Errorf("demo reset: table %s: imported %d rows, manifest requires %d", tableName, actual, expected)
		}
	}
	return nil
}

func wrapDemoResetError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("demo reset: %s: %w", action, err)
}
