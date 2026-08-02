package launcher

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
)

// configExportZip exports the full configuration as a ZIP archive.
// Works for both database and file-based configs.
func (h *handler) configExportZip(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := addConfigToZip(r.Context(), zw, b, ""); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Close дописывает центральный каталог: без него архив нечитаем, а без
	// проверки — нечитаем молча.
	if err := zw.Close(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	name := b.Name + "_config.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	writeDownload(w, name, buf.Bytes())
}

// configImportZip imports a configuration from a ZIP archive into the database.
func (h *handler) configImportZip(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxConfigArchiveUpload)
	file, _, err := r.FormFile("config_zip")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Upload error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer file.Close()

	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "ZIP size error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "ZIP seek error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	reader, err := zip.NewReader(file, size)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "ZIP error: " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Extract to temp dir, then import
	tmpDir, err := os.MkdirTemp("", "onebase-import-*")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Temp dir error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer os.RemoveAll(tmpDir)

	if err := validateArchiveEntries(tmpDir, reader.File, maxConfigArchiveExpanded); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "ZIP error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if err := extractValidatedArchive(tmpDir, reader.File); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Extract error: " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Import into database
	db, cerr := OpenDB(r.Context(), b)
	if cerr != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "DB error: " + cerr.Error()
		renderCfg(w, r, data)
		return
	}
	defer db.Close()

	repo := configdb.New(db)
	if err := repo.ImportFromDir(r.Context(), tmpDir); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Import error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if _, err := repo.CreateVersion(r.Context(), configdb.VersionOptions{
		AuthorLogin: cfgLogin(r.Context()),
		Message:     "import from zip",
	}); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Version error: " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Migrate after import.
	//
	// Конфигурация уже импортирована — откатывать её из-за миграции нельзя, но
	// и молчать о несогласованной схеме тоже: базу после этого не запустить.
	data := h.loadCfgData(r.Context(), b, "backup")
	if _, migrateErr := h.runner.MigrateBase(r.Context(), b); migrateErr != nil {
		data.Error = tr(resolveLang(r), "Данные восстановлены, но миграция схемы не выполнена") + ": " + migrateErr.Error()
		renderCfg(w, r, data)
		return
	}
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	data.BackupMessage = "Configuration imported from ZIP"
	renderCfg(w, r, data)
}
