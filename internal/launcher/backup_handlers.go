package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"gopkg.in/yaml.v3"
)

func (h *handler) backupDir(b *Base) string {
	custom := h.loadBackupDirSetting(b)
	if custom != "" {
		return custom
	}
	if b.Path != "" {
		return filepath.Join(b.Path, "backups")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".onebase", "backups", b.ID)
}

// safeBackupPath joins dir and file, guaranteeing the result stays inside dir.
// Protects against path traversal (../, absolute paths) in the {file} URL param.
func safeBackupPath(dir, file string) (string, error) {
	if file == "" || strings.ContainsRune(file, 0) {
		return "", i18nerr.New("недопустимое имя файла")
	}
	// reject any path separators / traversal — backup files are flat names.
	if strings.ContainsAny(file, `/\`) || file == ".." || strings.Contains(file, "..") {
		return "", i18nerr.Errorf("недопустимое имя файла: %s", file)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fp := filepath.Join(absDir, file)
	rel, err := filepath.Rel(absDir, fp)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", i18nerr.Errorf("недопустимое имя файла: %s", file)
	}
	return fp, nil
}

func (h *handler) loadBackupDirSetting(b *Base) string {
	if b.ConfigSource == "database" {
		db, err := OpenDB(context.Background(), b)
		if err != nil {
			return ""
		}
		defer db.Close()
		var content []byte
		if err := db.QueryRow(context.Background(),
			"SELECT content FROM _onebase_config WHERE path='config/app.yaml'").Scan(&content); err != nil {
			return ""
		}
		var tmp struct {
			Backup struct {
				Directory string `yaml:"directory"`
			} `yaml:"backup"`
		}
		yaml.Unmarshal(content, &tmp)
		return tmp.Backup.Directory
	}
	cfgPath := filepath.Join(b.Path, "config", "app.yaml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}
	var tmp struct {
		Backup struct {
			Directory string `yaml:"directory"`
		} `yaml:"backup"`
	}
	yaml.Unmarshal(raw, &tmp)
	return tmp.Backup.Directory
}

// dumpForBase chooses the right backup mechanism based on b.DBType.
func dumpForBase(ctx context.Context, b *Base, dir string) (string, error) {
	if b.DBType == "sqlite" {
		return backup.DumpSQLite(ctx, b.DBPath, dir)
	}
	return backup.Dump(ctx, b.DB, dir)
}

// restoreForBase chooses the right restore mechanism based on b.DBType.
func restoreForBase(ctx context.Context, b *Base, fp string) error {
	if b.DBType == "sqlite" {
		return backup.RestoreSQLite(ctx, b.DBPath, fp)
	}
	return backup.Restore(ctx, b.DB, fp)
}

// checkBackupFileMismatch returns an error when the backup file engine does not
// match the target base engine (e.g. restoring a .sql.gz PG dump into SQLite).
func checkBackupFileMismatch(b *Base, filename string) error {
	lower := strings.ToLower(filename)
	isPGDump := strings.HasSuffix(lower, ".sql.gz") || strings.HasSuffix(lower, ".sql")
	isSQLiteDump := strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite")
	targetSQLite := b.DBType == "sqlite"
	if isPGDump && targetSQLite {
		return i18nerr.Errorf("Нельзя восстановить PostgreSQL-бэкап в SQLite-базу (%s). Создайте базу с типом БД PostgreSQL.", filename)
	}
	if isSQLiteDump && !targetSQLite {
		return i18nerr.Errorf("Нельзя восстановить SQLite-бэкап в PostgreSQL-базу (%s). Создайте базу с типом БД SQLite.", filename)
	}
	return nil
}

func (h *handler) backupCreate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	dir := h.backupDir(b)
	outPath, dumpErr := dumpForBase(r.Context(), b, dir)
	data := h.loadCfgData(r.Context(), b, "backup")
	if dumpErr != nil {
		data.Error = tr(lang, "Ошибка бэкапа") + ": " + dumpErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		data.BackupMessage = tr(lang, "Бэкап создан") + ": " + filepath.Base(outPath)
	}
	renderCfg(w, r, data)
}

func (h *handler) backupDownload(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file := chi.URLParam(r, "file")
	dir := h.backupDir(b)
	fp, err := safeBackupPath(dir, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(fp); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+file)
	http.ServeFile(w, r, fp)
}

func (h *handler) backupDelete(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file := chi.URLParam(r, "file")
	if fp, err := safeBackupPath(h.backupDir(b), file); err == nil {
		os.Remove(fp)
	}
	data := h.loadCfgData(r.Context(), b, "backup")
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	renderCfg(w, r, data)
}

func (h *handler) backupSettings(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	type backupCfg struct {
		Enabled   bool   `yaml:"enabled"`
		Schedule  string `yaml:"schedule"`
		KeepLast  int    `yaml:"keep_last"`
		Directory string `yaml:"directory"`
	}
	type appCfgWithBackup struct {
		Name    string    `yaml:"name"`
		Version string    `yaml:"version,omitempty"`
		Backup  backupCfg `yaml:"backup,omitempty"`
	}
	keepLast, _ := strconv.Atoi(r.FormValue("backup_keep"))
	cfg := backupCfg{
		Enabled:   r.FormValue("backup_enabled") == "on",
		Schedule:  strings.TrimSpace(r.FormValue("backup_schedule")),
		KeepLast:  keepLast,
		Directory: strings.TrimSpace(r.FormValue("backup_dir")),
	}
	out, _ := yaml.Marshal(appCfgWithBackup{Name: b.Name, Backup: cfg})
	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, "config/app.yaml", out)
		}
	} else {
		dir := filepath.Join(b.Path, "config")
		os.MkdirAll(dir, 0o755)
		saveErr = os.WriteFile(filepath.Join(dir, "app.yaml"), out, 0o644)
	}
	data := h.loadCfgData(r.Context(), b, "backup")
	if saveErr != nil {
		data.Error = tr(resolveLang(r), "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		data.BackupMessage = tr(resolveLang(r), "Настройки бэкапа сохранены")
	}
	renderCfg(w, r, data)
}

func (h *handler) backupUpload(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir := h.backupDir(b)
	os.MkdirAll(dir, 0o755)

	lang := resolveLang(r)
	file, header, err := r.FormFile("backup_file")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка загрузки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	outPath, err := safeBackupPath(dir, name)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Недопустимое имя файла")
		renderCfg(w, r, data)
		return
	}
	f, err := os.Create(outPath)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer f.Close()
	io.Copy(f, file)

	data := h.loadCfgData(r.Context(), b, "backup")
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	data.BackupMessage = tr(lang, "Файл загружен") + ": " + name
	renderCfg(w, r, data)
}

func (h *handler) backupRestore(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	file := chi.URLParam(r, "file")
	dir := h.backupDir(b)
	fp, err := safeBackupPath(dir, file)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Недопустимое имя файла")
		renderCfg(w, r, data)
		return
	}
	if _, err := os.Stat(fp); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Файл не найден") + ": " + file
		renderCfg(w, r, data)
		return
	}

	if err := checkBackupFileMismatch(b, file); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = errText(r, err)
		renderCfg(w, r, data)
		return
	}

	wasRunning := h.runner.IsRunning(b.ID)
	if wasRunning {
		h.runner.Stop(b.ID)
		waitPortFree(b.Port, 3*time.Second)
	}

	restoreErr := restoreForBase(r.Context(), b, fp)
	data := h.loadCfgData(r.Context(), b, "backup")
	if restoreErr != nil {
		data.Error = tr(lang, "Ошибка восстановления") + ": " + restoreErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		msg := tr(lang, "База данных восстановлена из") + ": " + file
		if wasRunning {
			msg += ". " + tr(lang, "База остановлена — запустите её заново для применения изменений.")
		}
		data.BackupMessage = msg
	}
	renderCfg(w, r, data)
}

// backupFullExport creates a single .obz file containing both database dump and configuration.
// If the form field "compatible" is not "false", a universal (cross-engine) archive is created.
func (h *handler) backupFullExport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// compatible=true means universal cross-engine format; absent/other = binary.
	compatible := r.FormValue("compatible") == "true"

	name := b.Name + "_" + time.Now().Format("2006-01-02_15-04") + ".obz"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)

	lang := resolveLang(r)

	if compatible {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			http.Error(w, tr(lang, "Ошибка подключения к БД")+": "+errText(r, cerr), 500)
			return
		}
		defer db.Close()

		configSource := b.ConfigSource
		if configSource == "" {
			configSource = "database"
		}

		if err := backup.ExportUniversal(
			r.Context(), db,
			configSource, b.Path,
			db.FilesDir(),
			b.Name,
			w,
		); err != nil {
			// Headers already sent — log only; cannot change status.
			fmt.Fprintf(os.Stderr, "backupFullExport universal error: %v\n", err)
		}
		return
	}

	// Binary export (fast, same-engine only).
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	tmpDir, err := os.MkdirTemp("", "onebase-obz-dump-*")
	if err != nil {
		http.Error(w, tr(lang, "Ошибка создания временной папки")+": "+errText(r, err), 500)
		return
	}
	defer os.RemoveAll(tmpDir)

	dumpPath, dumpErr := dumpForBase(r.Context(), b, tmpDir)
	if dumpErr != nil {
		http.Error(w, tr(lang, "Ошибка выгрузки дампа")+": "+errText(r, dumpErr), 500)
		return
	}

	dumpData, err := os.ReadFile(dumpPath)
	if err != nil {
		http.Error(w, tr(lang, "Ошибка чтения дампа")+": "+errText(r, err), 500)
		return
	}
	dumpEntryName := "database.sql.gz"
	if b.DBType == "sqlite" {
		dumpEntryName = "database.db"
	}
	f, _ := zw.Create(dumpEntryName)
	f.Write(dumpData)

	// Configuration
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr == nil {
			defer db.Close()
			rows, qerr := db.Query(r.Context(), `SELECT path, content FROM _onebase_config ORDER BY path`)
			if qerr == nil {
				defer rows.Close()
				for rows.Next() {
					var p string
					var content []byte
					if rows.Scan(&p, &content) != nil {
						continue
					}
					cf, _ := zw.Create("config/" + strings.ReplaceAll(p, `\`, "/"))
					cf.Write(content)
				}
			}
		}
	} else {
		srcDir := b.Path
		filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(srcDir, path)
			rel = strings.ReplaceAll(rel, `\`, "/")
			if strings.HasPrefix(rel, "backups/") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			cf, _ := zw.Create("config/" + rel)
			cf.Write(content)
			return nil
		})
	}

	exportDBType := b.DBType
	if exportDBType == "" {
		exportDBType = "postgres"
	}
	meta := fmt.Sprintf("onebase_full_export\nversion=1.0\nformat=binary\ndate=%s\nbase=%s\nsource=%s\ndb_type=%s\n",
		time.Now().Format("2006-01-02T15:04:05"), b.Name, b.ConfigSource, exportDBType)
	mf, _ := zw.Create("META.txt")
	mf.Write([]byte(meta))

	zw.Close()
	w.Write(buf.Bytes())
}

// backupFullImport restores both database and configuration from a .obz file.
func (h *handler) backupFullImport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	lang := resolveLang(r)
	file, _, err := r.FormFile("obz_file")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка загрузки файла") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer file.Close()

	dtData, err := io.ReadAll(file)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка чтения файла") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	reader, err := zip.NewReader(bytes.NewReader(dtData), int64(len(dtData)))
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Неверный формат файла .obz") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	tmpDir, err := os.MkdirTemp("", "onebase-obz-import-*")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Temp dir error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer os.RemoveAll(tmpDir)

	// Pre-scan META.txt for format and db_type.
	archiveFormat := ""
	archiveDBType := ""
	for _, af := range reader.File {
		if af.Name == "META.txt" {
			rc, merr := af.Open()
			if merr == nil {
				metaBytes, _ := io.ReadAll(rc)
				rc.Close()
				for _, line := range strings.Split(string(metaBytes), "\n") {
					if strings.HasPrefix(line, "db_type=") {
						archiveDBType = strings.TrimSpace(strings.TrimPrefix(line, "db_type="))
					}
					if strings.HasPrefix(line, "format=") {
						archiveFormat = strings.TrimSpace(strings.TrimPrefix(line, "format="))
					}
				}
			}
			break
		}
	}

	// Universal format: cross-engine restore.
	if archiveFormat == "universal" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			// For SQLite: if the file exists but is not a valid database (new/empty/corrupt),
			// delete it so SQLite creates a fresh file — full restore will repopulate everything.
			if b.DBType == "sqlite" && b.DBPath != "" &&
				strings.Contains(cerr.Error(), "file is not a database") {
				if os.Remove(b.DBPath) == nil {
					db, cerr = OpenDB(r.Context(), b)
				}
			}
		}
		if cerr != nil {
			data := h.loadCfgData(r.Context(), b, "backup")
			data.Error = tr(lang, "Ошибка подключения к БД") + ": " + cerr.Error()
			renderCfg(w, r, data)
			return
		}
		defer db.Close()

		wasRunning := h.runner.IsRunning(b.ID)
		if wasRunning {
			h.runner.Stop(b.ID)
			waitPortFree(b.Port, 3*time.Second)
		}

		configDest := b.ConfigSource
		if configDest == "" {
			configDest = "database"
		}
		cfgFileDir := b.Path

		report, importErr := backup.ImportUniversal(
			r.Context(), db,
			configDest, cfgFileDir,
			db.FilesDir(),
			bytes.NewReader(dtData), int64(len(dtData)),
		)

		if importErr == nil {
			h.runner.MigrateBase(r.Context(), b)
		}

		data := h.loadCfgData(r.Context(), b, "backup")
		if importErr != nil {
			data.Error = tr(lang, "Ошибка восстановления") + ": " + importErr.Error()
		} else {
			data.FieldsSaved = true
			data.FieldsSavedEntity = "panel-backup"
			msg := fmt.Sprintf(tr(lang, "Полное восстановление выполнено: %d таблиц, %d файлов вложений"),
				len(report.Tables), report.Files)
			if wasRunning {
				msg += ". " + tr(lang, "База остановлена — запустите её заново.")
			}
			data.BackupMessage = msg
		}
		renderCfg(w, r, data)
		return
	}

	var dumpFile string
	var configDir string

	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			os.MkdirAll(filepath.Join(tmpDir, f.Name), 0o755)
			continue
		}
		outPath := filepath.Join(tmpDir, f.Name)
		os.MkdirAll(filepath.Dir(outPath), 0o755)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		outFile, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			continue
		}
		io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		switch f.Name {
		case "database.sql.gz":
			dumpFile = outPath
			if archiveDBType == "" {
				archiveDBType = "postgres"
			}
		case "database.db":
			dumpFile = outPath
			if archiveDBType == "" {
				archiveDBType = "sqlite"
			}
		}
		if strings.HasPrefix(f.Name, "config/") && configDir == "" {
			configDir = filepath.Join(tmpDir, "config")
		}
	}

	// Reject cross-engine restores for binary format.
	targetDBType := b.DBType
	if targetDBType == "" {
		targetDBType = "postgres"
	}
	if archiveDBType != "" && archiveDBType != targetDBType {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = fmt.Sprintf(
			tr(lang, "Нельзя восстановить %s-бэкап в %s-базу (%s). Создайте новую базу с типом БД %s или используйте совместимый формат (.obz с галочкой)."),
			archiveDBType, targetDBType, filepath.Base(r.FormValue("obz_file")), archiveDBType,
		)
		renderCfg(w, r, data)
		return
	}

	wasRunning := h.runner.IsRunning(b.ID)
	if wasRunning {
		h.runner.Stop(b.ID)
		waitPortFree(b.Port, 3*time.Second)
	}

	// Restore database
	var restoreErr error
	if dumpFile != "" {
		restoreErr = restoreForBase(r.Context(), b, dumpFile)
	} else {
		restoreErr = fmt.Errorf("database dump not found in archive (expected database.sql.gz or database.db)")
	}

	// Import configuration
	var configErr error
	if configDir != "" {
		if b.ConfigSource == "database" {
			db, cerr := OpenDB(r.Context(), b)
			if cerr != nil {
				configErr = cerr
			} else {
				defer db.Close()
				repo := configdb.New(db)
				configErr = repo.ImportFromDir(r.Context(), configDir)
				if configErr == nil {
					_, configErr = repo.CreateVersion(r.Context(), configdb.VersionOptions{
						AuthorLogin: cfgLogin(r.Context()),
						Message:     "full backup config import",
					})
				}
			}
		} else {
			configErr = filepath.WalkDir(configDir, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(configDir, path)
				dst, jerr := configdb.SafeJoin(b.Path, filepath.ToSlash(rel))
				if jerr != nil {
					return jerr
				}
				os.MkdirAll(filepath.Dir(dst), 0o755)
				content, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				os.WriteFile(dst, content, 0o644)
				return nil
			})
		}
	}

	if configErr == nil {
		h.runner.MigrateBase(r.Context(), b)
	}

	data := h.loadCfgData(r.Context(), b, "backup")
	if restoreErr != nil {
		data.Error = tr(lang, "Ошибка восстановления БД") + ": " + restoreErr.Error()
	} else if configErr != nil {
		data.Error = tr(lang, "Ошибка импорта конфигурации") + ": " + configErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		msg := tr(lang, "Полное восстановление выполнено: база данных + конфигурация")
		if wasRunning {
			msg += ". " + tr(lang, "База остановлена — запустите её заново для применения изменений.")
		}
		data.BackupMessage = msg
	}
	renderCfg(w, r, data)
}
