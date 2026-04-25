package launcher

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/converter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// ── view types ────────────────────────────────────────────────────────────────

type cfgField struct {
	Name      string
	Type      string
	RefEntity string
}

type cfgTablePart struct {
	Name   string
	Fields []cfgField
}

type cfgEntity struct {
	Name       string
	Kind       string // "Справочник" / "Документ"
	Posting    bool
	Fields     []cfgField
	TableParts []cfgTablePart
	Source     string // raw .os content, empty if none
}

type cfgRegister struct {
	Name       string
	Dimensions []cfgField
	Resources  []cfgField
	Attributes []cfgField
}

type cfgReport struct {
	Name   string
	Title  string
	Query  string
	Params []string
}

type configuratorData struct {
	Base     *Base
	AppName  string
	Tab      string // "tree" | "convert" | "files"
	Entities []cfgEntity
	Catalogs []cfgEntity
	Docs     []cfgEntity
	Registers []cfgRegister
	Reports  []cfgReport
	Error    string
	// converter
	ConvertSrcDir  string
	ConvertResult  string
	ConvertApplied bool
	// module save
	ModuleSaved       bool
	ModuleSavedEntity string
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (h *handler) configuratorPage(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "tree"
	}
	data := h.loadCfgData(r.Context(), b, tab)
	renderCfg(w, data)
}

func (h *handler) configuratorConvert(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	srcDir := strings.TrimSpace(r.FormValue("src_dir"))
	apply := r.FormValue("apply") == "1"

	data := h.loadCfgData(r.Context(), b, "convert")
	data.ConvertSrcDir = srcDir

	if srcDir == "" {
		data.Error = "Укажите путь к папке конфигурации 1С"
		renderCfg(w, data)
		return
	}

	outDir, err := workspacePath(b.ID + "-convert")
	if err != nil {
		data.Error = err.Error()
		renderCfg(w, data)
		return
	}
	// clean previous conversion
	os.RemoveAll(outDir)

	rep, err := converter.Convert(converter.Options{SourceDir: srcDir, OutDir: outDir})
	if err != nil {
		data.Error = "Ошибка конвертации: " + err.Error()
		renderCfg(w, data)
		return
	}
	data.ConvertResult = rep.String()

	if apply {
		if b.ConfigSource == "database" {
			db, cerr := storage.Connect(r.Context(), b.DB)
			if cerr != nil {
				data.Error = "Ошибка подключения к БД: " + cerr.Error()
				renderCfg(w, data)
				return
			}
			defer db.Close()
			repo := configdb.New(db.Pool())
			repo.EnsureSchema(r.Context())
			if cerr := repo.ImportFromDir(r.Context(), outDir); cerr != nil {
				data.Error = "Ошибка импорта: " + cerr.Error()
				renderCfg(w, data)
				return
			}
		} else {
			// file mode — copy files into base path
			if cerr := copyDir(outDir, b.Path); cerr != nil {
				data.Error = "Ошибка копирования: " + cerr.Error()
				renderCfg(w, data)
				return
			}
		}
		data.ConvertApplied = true
		// reload tree with new data
		fresh := h.loadCfgData(r.Context(), b, "convert")
		fresh.ConvertSrcDir = srcDir
		fresh.ConvertResult = data.ConvertResult
		fresh.ConvertApplied = true
		data = fresh
	}

	renderCfg(w, data)
}

// ── data loading ──────────────────────────────────────────────────────────────

func (h *handler) loadCfgData(ctx context.Context, b *Base, tab string) *configuratorData {
	data := &configuratorData{Base: b, Tab: tab}

	var proj *project.Project
	var err error

	if b.ConfigSource == "database" {
		db, cerr := storage.Connect(ctx, b.DB)
		if cerr != nil {
			data.Error = "Нет подключения к БД: " + cerr.Error()
			return data
		}
		defer db.Close()
		repo := configdb.New(db.Pool())
		if cerr := repo.EnsureSchema(ctx); cerr != nil {
			data.Error = cerr.Error()
			return data
		}
		empty, _ := repo.IsEmpty(ctx)
		if empty {
			data.Error = "Конфигурация не загружена в базу данных. Воспользуйтесь вкладкой «Файлы»."
			return data
		}
		proj, err = project.LoadFromDB(ctx, repo)
	} else {
		proj, err = project.Load(b.Path)
	}

	if err != nil {
		data.Error = "Ошибка загрузки конфигурации: " + err.Error()
		return data
	}
	defer proj.Close()

	if appCfg, _ := project.LoadConfig(proj.Dir); appCfg != nil {
		data.AppName = appCfg.Name
	}

	sources := readOSSources(proj.Dir)

	for _, e := range proj.Entities {
		ev := cfgEntity{
			Name:    e.Name,
			Posting: e.Posting,
			Source:  sources[e.Name],
		}
		if e.Kind == metadata.KindCatalog {
			ev.Kind = "Справочник"
		} else {
			ev.Kind = "Документ"
		}
		for _, f := range e.Fields {
			ev.Fields = append(ev.Fields, toCfgField(f))
		}
		for _, tp := range e.TableParts {
			tpv := cfgTablePart{Name: tp.Name}
			for _, f := range tp.Fields {
				tpv.Fields = append(tpv.Fields, toCfgField(f))
			}
			ev.TableParts = append(ev.TableParts, tpv)
		}
		data.Entities = append(data.Entities, ev)
	}

	sort.Slice(data.Entities, func(i, j int) bool {
		return data.Entities[i].Name < data.Entities[j].Name
	})
	for _, e := range data.Entities {
		if e.Kind == "Справочник" {
			data.Catalogs = append(data.Catalogs, e)
		} else {
			data.Docs = append(data.Docs, e)
		}
	}

	for _, reg := range proj.Registers {
		rv := cfgRegister{Name: reg.Name}
		for _, f := range reg.Dimensions {
			rv.Dimensions = append(rv.Dimensions, toCfgField(f))
		}
		for _, f := range reg.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		for _, f := range reg.Attributes {
			rv.Attributes = append(rv.Attributes, toCfgField(f))
		}
		data.Registers = append(data.Registers, rv)
	}

	for _, rep := range proj.Reports {
		rv := cfgReport{Name: rep.Name, Title: rep.Title, Query: rep.Query}
		for _, p := range rep.Params {
			rv.Params = append(rv.Params, p.DisplayLabel()+" ["+p.Type+"]")
		}
		data.Reports = append(data.Reports, rv)
	}

	return data
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toCfgField(f metadata.Field) cfgField {
	typ := string(f.Type)
	if f.RefEntity != "" {
		typ = "reference"
	}
	return cfgField{Name: f.Name, Type: typ, RefEntity: f.RefEntity}
}

func readOSSources(dir string) map[string]string {
	out := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".os")
		r, size := utf8.DecodeRuneInString(base)
		key := string(unicode.ToUpper(r)) + base[size:]
		out[key] = string(raw)
	}
	return out
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func (h *handler) configuratorSaveModule(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	entityName := r.FormValue("entity")
	source := r.FormValue("source")

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := storage.Connect(r.Context(), b.DB)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			filename := entityToFilename(entityName)
			_, saveErr = db.Pool().Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, now())
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=now()
			`, "src/"+filename, []byte(source))
		}
	} else {
		filename := entityToFilename(entityName)
		srcDir := filepath.Join(b.Path, "src")
		os.MkdirAll(srcDir, 0o755)
		saveErr = os.WriteFile(filepath.Join(srcDir, filename), []byte(source), 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = "Ошибка сохранения: " + saveErr.Error()
	} else {
		data.ModuleSaved = true
		data.ModuleSavedEntity = entityName
	}
	renderCfg(w, data)
}

// entityToFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.os"
// (mirrors fileNameToEntity: lower the first rune, keep the rest).
func entityToFilename(name string) string {
	if name == "" {
		return ".os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".os"
}

func renderCfg(w http.ResponseWriter, data *configuratorData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cfgTmpl.ExecuteTemplate(w, "cfg-main", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
