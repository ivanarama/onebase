package backup

import (
	"archive/zip"
	"context"
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
func DemoReset(ctx context.Context, db *storage.DB, backupPath string) (*ImportReport, error) {
	report := &ImportReport{Tables: make(map[string]int)}

	if backupPath == "" {
		return report, nil
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

	fkCleanup, err := db.DisableFKForImport(ctx)
	if err != nil {
		return report, fmt.Errorf("demo reset: disable FK: %w", err)
	}
	fkDisabled := true
	defer func() {
		if fkDisabled {
			_ = fkCleanup()
		}
	}()

	// Импортируем конфигурацию из config/ (каталоги, формы, отчёты и т.д.).
	// Для --config-source database конфиг запишется в _onebase_config.
	configDir := filepath.Join(tmpDir, "config")
	if _, err := os.Stat(configDir); err == nil {
		if err := importConfig(ctx, db, "database", "", configDir); err != nil {
			return report, fmt.Errorf("demo reset config: %w", err)
		}
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
		sysOrder := []string{
			"_attachments",
			"_audit",
			"_constants",
			"_numerators",
			"_users",      // до _user_roles
			"_roles",      // до _user_roles
			"_user_roles", // последним — зависит от _users и _roles
		}
		for _, tbl := range sysOrder {
			if authTables[tbl] {
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

	if err := fkCleanup(); err != nil {
		return report, fmt.Errorf("demo reset: restore FK: %w", err)
	}
	fkDisabled = false
	return report, nil
}
