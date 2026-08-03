package launcher

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

func (h *handler) configuratorSavePrintForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if failForm(w, r) {
		return
	}
	filename := strings.TrimSpace(r.FormValue("printform_filename"))
	source := r.FormValue("source")
	relPath := "printforms/" + filename

	if filename == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя файла печатной формы не указано")
		renderCfg(w, r, data)
		return
	}
	if err := configdb.ValidatePath(relPath); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя файла печатной формы") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, relPath, []byte(source))
	}

	var hdr struct {
		Name string `yaml:"name"`
	}
	yaml.Unmarshal([]byte(source), &hdr) //nolint
	pfName := hdr.Name
	isDSL := r.FormValue("printform_dsl") == "1"
	if isDSL {
		pfName = strings.TrimSuffix(filename, ".os")
	} else if pfName == "" {
		pfName = strings.TrimSuffix(filename, ".yaml")
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = pfName
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorNewPrintForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if failForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	document := strings.TrimSpace(r.FormValue("document"))

	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя печатной формы обязательно")
		renderCfg(w, r, data)
		return
	}

	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	filename := string(runes) + ".yaml"
	relPath := "printforms/" + filename
	if err := configdb.ValidatePath(relPath); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя файла печатной формы") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	source := fmt.Sprintf("name: %s\ndocument: %s\ntitle: \"{{Номер}} от {{Дата | date}}\"\n\nheader: |\n  ## %s\n\ntable:\n  source: Товары\n  columns:\n    - field: \"@row\"\n      label: \"№\"\n      width: 36px\n      align: center\n", name, document, name)

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
		}
	} else {
		fullPath, jerr := configdb.SafeJoin(b.Path, relPath)
		if jerr != nil {
			saveErr = jerr
		} else if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) { //nolint:gosec // G703: fullPath построен configdb.SafeJoin — это и есть guard от traversal
			saveErr = h.writeConfigFileRaw(r.Context(), b, relPath, []byte(source))
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка создания") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = name
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorSaveLayout(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if failForm(w, r) {
		return
	}
	layoutName := strings.TrimSpace(r.FormValue("layout_name"))
	source := r.FormValue("source")

	if layoutName == "" {
		http.Error(w, "layout name required", 400)
		return
	}
	if !validLayoutName(layoutName) {
		http.Error(w, "bad layout name", http.StatusBadRequest)
		return
	}

	filename := layoutName + ".layout.yaml"
	relPath := "printforms/" + filename

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
		}
	} else {
		pfDir := filepath.Join(b.Path, "printforms")
		layoutPath := ""
		walkErr := filepath.Walk(pfDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Base(path), filename) {
				layoutPath = path
			}
			return nil
		})
		bestEffort("обойти каталог печатных форм", walkErr)
		if layoutPath == "" {
			walkErr := filepath.Walk(pfDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if strings.EqualFold(filepath.Base(path), layoutName+".os") {
					layoutPath = filepath.Join(filepath.Dir(path), filename)
				}
				return nil
			})
			bestEffort("обойти каталог печатных форм", walkErr)
		}
		if layoutPath == "" {
			layoutPath, saveErr = configdb.SafeJoin(b.Path, relPath)
		}
		if saveErr != nil {
			// keep saveErr for the common response below
		} else {
			saveErr = os.WriteFile(layoutPath, []byte(source), 0o644)
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения макета") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = layoutName
		data.SavedMessage = tr(lang, "✓ Макет") + " «" + layoutName + "» " + tr(lang, "сохранён. Перезапустите базу, чтобы изменения вступили в силу.")
		data.SelectedTreeID = "mkt-" + layoutName
	}
	renderCfg(w, r, data)
}

// ── Subsystem save ──────────────────────────────────────────────────────────

// homeWidgetsNames разворачивает раскладку рабочего стола в плоский список имён
