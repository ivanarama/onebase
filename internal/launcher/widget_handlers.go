package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// configuratorSaveWidget upserts a single widgets/<name>.yaml entry. The body
// is taken verbatim from the textarea, then validated by re-parsing through
// metadata.LoadWidgetFile so users see syntax errors instead of broken pages.
func (h *handler) configuratorSaveWidget(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("widget_name"))
	body := r.FormValue("yaml")
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя виджета не задано")
		renderCfg(w, r, data)
		return
	}

	// Validate: parse without writing to disk first so a malformed YAML never
	// replaces a working widget definition.
	tmp, err := os.CreateTemp("", "widget-*.yaml")
	if err == nil {
		tmp.WriteString(body)
		tmp.Close()
		defer os.Remove(tmp.Name())
		if _, perr := metadata.LoadWidgetFile(tmp.Name()); perr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка YAML") + ": " + perr.Error()
			renderCfg(w, r, data)
			return
		}
	}

	relPath := "widgets/" + nameToFilename(name) + ".yaml"
	saveErr := saveConfigFile(r, h, b, relPath, []byte(body))

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = name
	}
	renderCfg(w, r, data)
}

// configuratorDeleteWidget removes widgets/<name>.yaml from the configuration.
func (h *handler) configuratorDeleteWidget(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("widget_name"))
	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя виджета не задано")
		renderCfg(w, r, data)
		return
	}
	relPath := "widgets/" + nameToFilename(name) + ".yaml"
	delErr := deleteConfigFile(r, h, b, relPath)
	data := h.loadCfgData(r.Context(), b, "tree")
	if delErr != nil {
		data.Error = tr(lang, "Ошибка удаления") + ": " + delErr.Error()
	}
	renderCfg(w, r, data)
}

// configuratorSaveHomePage writes config/home_page.yaml from the visual editor:
// checked widgets (home_widgets) grouped into rows by home_cols, layout mode
// (home_layout: auto|rows) and an optional title. Existing title translations
// (titles) are preserved by loading the current file. The verbatim YAML editor
// lives in configuratorSaveHomePageYAML.
func (h *handler) configuratorSaveHomePage(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	type yamlHomePage struct {
		Title   string                      `yaml:"title,omitempty"`
		Titles  map[string]string           `yaml:"titles,omitempty"`
		Layout  string                      `yaml:"layout,omitempty"`
		Rows    []metadata.HomePageRow      `yaml:"rows,omitempty"`
		Widgets []metadata.HomePageWidget   `yaml:"widgets,omitempty"`
		Nav     *metadata.SubsystemContents `yaml:"nav,omitempty"`
	}
	hp := yamlHomePage{Title: strings.TrimSpace(r.FormValue("home_title"))}
	// Titles: если форма содержит блок titles.* — берём из формы; иначе переносим
	// из существующего файла (визуальный редактор раскладки их не должен терять).
	if formHasMapField(r, "titles") {
		hp.Titles = parseMapForm(r, "titles")
	}

	// Сохраняем Nav и fallback-Title из существующего файла.
	if proj, lerr := h.loadProjectFor(r.Context(), b); lerr == nil && proj != nil {
		if proj.HomePage != nil {
			if !formHasMapField(r, "titles") {
				hp.Titles = proj.HomePage.Titles
			}
			hp.Nav = proj.HomePage.Nav
			if hp.Title == "" {
				hp.Title = proj.HomePage.Title
			}
		}
		proj.Close()
	}

	hp.Rows, hp.Layout = rowsFromForm(r)

	out, _ := yaml.Marshal(&hp)
	saveErr := saveConfigFile(r, h, b, "config/home_page.yaml", out)
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "home-page"
	}
	renderCfg(w, r, data)
}

// configuratorSaveHomePageYAML writes config/home_page.yaml verbatim from the
// «Расширенно (YAML)» editor. Validation is YAML-only — empty layout means
// "use defaults" which is supported by the runtime.
func (h *handler) configuratorSaveHomePageYAML(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	body := r.FormValue("yaml")

	if strings.TrimSpace(body) != "" {
		var probe map[string]any
		if perr := yaml.Unmarshal([]byte(body), &probe); perr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка YAML") + ": " + perr.Error()
			renderCfg(w, r, data)
			return
		}
	}

	saveErr := saveConfigFile(r, h, b, "config/home_page.yaml", []byte(body))
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "home-page"
	}
	renderCfg(w, r, data)
}

// configFileEntry — путь (относительный, со слешами) и содержимое одного файла
// конфигурации для пакетной записи через saveConfigFiles.
type configFileEntry struct {
	relPath string
	content []byte
}

// saveConfigFile is a small wrapper used by widget/homepage handlers. It writes
// the given relative path either to the database-backed config table or to the
// file-based project directory, matching whichever storage mode the base uses.
func saveConfigFile(r *http.Request, h *handler, b *Base, relPath string, content []byte) error {
	return saveConfigFiles(r, h, b, []configFileEntry{{relPath: relPath, content: content}})
}

// saveConfigFiles атомарно сохраняет несколько файлов конфигурации как одну
// операцию (например, страница = pages/X.yaml + src/X.page.os).
//
//	database: все upsert'ы в ОДНОЙ транзакции (WithTx) — перечитка конфигурации
//	          не увидит половину объекта, и отказ на втором файле откатывает первый.
//	file:     каждый файл пишется атомарно (temp+rename), поэтому --watch никогда
//	          не читает частичный/пустой файл. Порядок files сохраняется —
//	          вызывающий передаёт сначала исходник, затем метаданные, чтобы reload
//	          по записи .yaml уже видел готовый .os.
func saveConfigFiles(r *http.Request, h *handler, b *Base, files []configFileEntry) error {
	return saveConfigFilesWithVersion(r, h, b, files, configdb.VersionOptions{AuthorLogin: cfgLogin(r.Context())})
}

func saveConfigFilesWithVersion(r *http.Request, h *handler, b *Base, files []configFileEntry, opts configdb.VersionOptions) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(r.Context()); err != nil {
			return err
		}
		if opts.AuthorLogin == "" {
			opts.AuthorLogin = cfgLogin(r.Context())
		}
		return repo.SaveFiles(r.Context(), configDBFiles(files), opts)
	}
	for _, f := range files {
		if err := atomicWriteConfigFile(b.Path, f.relPath, f.content); err != nil {
			return err
		}
	}
	return nil
}

// atomicWriteConfigFile пишет файл через temp+rename в том же каталоге, чтобы
// читатель (--watch) никогда не видел частично записанный файл (эталон — store.save).
func atomicWriteConfigFile(basePath, relPath string, content []byte) error {
	full, err := configdb.SafeJoin(basePath, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

func deleteConfigFile(r *http.Request, h *handler, b *Base, relPath string) error {
	return deleteConfigFiles(r, h, b, []string{relPath})
}

// deleteConfigFiles удаляет несколько файлов конфигурации как одну операцию.
// database: в одной транзакции; file: последовательно (отсутствие файла — не ошибка).
// Вызывающий передаёт пути в порядке, безопасном для reload (сначала метаданные).
func deleteConfigFiles(r *http.Request, h *handler, b *Base, relPaths []string) error {
	return deleteConfigFilesWithVersion(r, h, b, relPaths, configdb.VersionOptions{AuthorLogin: cfgLogin(r.Context())})
}

func deleteConfigFilesWithVersion(r *http.Request, h *handler, b *Base, relPaths []string, opts configdb.VersionOptions) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		if opts.AuthorLogin == "" {
			opts.AuthorLogin = cfgLogin(r.Context())
		}
		return repo.DeleteFiles(r.Context(), relPaths, opts)
	}
	for _, p := range relPaths {
		full, err := configdb.SafeJoin(b.Path, p)
		if err != nil {
			return err
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func configDBFiles(files []configFileEntry) []configdb.ConfigFile {
	out := make([]configdb.ConfigFile, 0, len(files))
	for _, f := range files {
		out = append(out, configdb.ConfigFile{Path: f.relPath, Content: f.content})
	}
	return out
}
