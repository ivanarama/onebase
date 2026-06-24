package launcher

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			http.Error(w, cerr.Error(), 500)
			return
		}
		defer db.Close()

		rows, qerr := db.Query(r.Context(), `SELECT path, content FROM _onebase_config ORDER BY path`)
		if qerr != nil {
			http.Error(w, qerr.Error(), 500)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			var content []byte
			if err := rows.Scan(&p, &content); err != nil {
				continue
			}
			f, _ := zw.Create(p)
			f.Write(content)
		}
	} else {
		srcDir := b.Path
		filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(srcDir, path)
			rel = strings.ReplaceAll(rel, `\`, `/`)
			if strings.HasPrefix(rel, "backups/") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			f, _ := zw.Create(rel)
			f.Write(content)
			return nil
		})
	}

	zw.Close()

	name := b.Name + "_config.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Write(buf.Bytes())
}

// configImportZip imports a configuration from a ZIP archive into the database.
func (h *handler) configImportZip(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	file, _, err := r.FormFile("config_zip")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Upload error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer file.Close()

	zipData, err := io.ReadAll(file)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Read error: " + err.Error()
		renderCfg(w, r, data)
		return
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
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

	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			os.MkdirAll(filepath.Join(tmpDir, f.Name), 0o755)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		outPath := filepath.Join(tmpDir, f.Name)
		os.MkdirAll(filepath.Dir(outPath), 0o755)
		outFile, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			continue
		}
		io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
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

	// Migrate after import
	h.runner.MigrateBase(r.Context(), b)

	data := h.loadCfgData(r.Context(), b, "backup")
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	data.BackupMessage = "Configuration imported from ZIP"
	renderCfg(w, r, data)
}
