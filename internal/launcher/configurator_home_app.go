package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/ui"
	"gopkg.in/yaml.v3"
)

func homeWidgetsNames(hp *metadata.HomePage) []string {
	if hp == nil {
		return nil
	}
	var names []string
	for _, row := range hp.Rows {
		names = append(names, row.Widgets...)
	}
	for _, w := range hp.Widgets {
		names = append(names, w.Name)
	}
	return names
}

// homeLayoutMode возвращает режим раскладки для селектора: "rows" или "auto".
func homeLayoutMode(hp *metadata.HomePage) string {
	if hp != nil && hp.Layout == "rows" {
		return "rows"
	}
	return "auto"
}

// rowsFromForm строит ряды виджетов и режим раскладки из формы редактора.
// Режим «По рядам» (home_layout=rows) читает JSON home_rows из drag-конструктора;
// иначе отмеченные галочками виджеты (home_widgets) складываются в один ряд.
func rowsFromForm(r *http.Request) ([]metadata.HomePageRow, string) {
	clean := func(in []string) []string {
		var out []string
		for _, n := range in {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if r.FormValue("home_layout") == "rows" {
		var raw [][]string
		_ = json.Unmarshal([]byte(r.FormValue("home_rows")), &raw)
		var rows []metadata.HomePageRow
		for _, names := range raw {
			if c := clean(names); len(c) > 0 {
				rows = append(rows, metadata.HomePageRow{Widgets: c})
			}
		}
		return rows, "rows"
	}
	if names := clean(r.Form["home_widgets"]); len(names) > 0 {
		return []metadata.HomePageRow{{Widgets: names}}, "auto"
	}
	return nil, "auto"
}

func (h *handler) configuratorSaveSubsystem(w http.ResponseWriter, r *http.Request) {
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

	subName := strings.TrimSpace(r.FormValue("subsystem_name"))
	title := r.FormValue("title")
	icon := ui.NormalizeIconName(r.FormValue("icon"))
	orderStr := r.FormValue("order")
	var order int
	if orderStr != "" {
		fmt.Sscanf(orderStr, "%d", &order)
	}

	// Без имени подсистему не сохраняем — иначе на диске появляется битый
	// файл «.yaml» с пустым name (пустая подсистема в дереве).
	if subName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Укажите имя подсистемы")
		renderCfg(w, r, data)
		return
	}

	if title == "" {
		title = subName
	}

	dir := b.Path
	if b.ConfigSource == "database" {
		dir, err = workspacePath(b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	subDir := filepath.Join(dir, "subsystems")
	os.MkdirAll(subDir, 0o755)

	type yamlContents struct {
		Catalogs   []string `yaml:"catalogs,omitempty"`
		Documents  []string `yaml:"documents,omitempty"`
		Registers  []string `yaml:"registers,omitempty"`
		InfoRegs   []string `yaml:"inforegs,omitempty"`
		Reports    []string `yaml:"reports,omitempty"`
		Processors []string `yaml:"processors,omitempty"`
		Journals   []string `yaml:"journals,omitempty"`
		Pages      []string `yaml:"pages,omitempty"`
	}
	type yamlHomePage struct {
		Title   string                    `yaml:"title,omitempty"`
		Titles  map[string]string         `yaml:"titles,omitempty"`
		Layout  string                    `yaml:"layout,omitempty"`
		Rows    []metadata.HomePageRow    `yaml:"rows,omitempty"`
		Widgets []metadata.HomePageWidget `yaml:"widgets,omitempty"`
	}
	type yamlSubsystem struct {
		Name     string            `yaml:"name"`
		Title    string            `yaml:"title"`
		Titles   map[string]string `yaml:"titles,omitempty"`
		Icon     string            `yaml:"icon,omitempty"`
		Order    int               `yaml:"order"`
		Contents yamlContents      `yaml:"contents"`
		HomePage *yamlHomePage     `yaml:"home_page,omitempty"`
	}

	targetFile := filepath.Join(subDir, nameToFilename(subName)+".yaml")

	ys := yamlSubsystem{
		Name:  subName,
		Title: title,
		Icon:  icon,
		Order: order,
	}
	ys.Contents.Catalogs = r.Form["catalogs"]
	ys.Contents.Documents = r.Form["documents"]
	ys.Contents.Registers = r.Form["registers"]
	ys.Contents.InfoRegs = r.Form["inforegs"]
	ys.Contents.Reports = r.Form["reports"]
	ys.Contents.Processors = r.Form["processors"]
	ys.Contents.Journals = r.Form["journals"]

	// Сохраняем переводы (titles) и метаданные рабочего стола из уже
	// существующего файла, чтобы перезапись не теряла данные, которых нет в форме.
	if existing, lerr := metadata.LoadSubsystemFile(targetFile); lerr == nil && existing != nil {
		// Страницы в «Составе подсистемы» пока не показываются галочками —
		// переносим их из существующего файла, чтобы сохранение подсистемы не
		// затирало contents.pages (журналы и остальные категории берутся из формы).
		ys.Contents.Pages = existing.Contents.Pages
		if formHasMapField(r, "titles") {
			ys.Titles = parseMapForm(r, "titles")
		} else {
			ys.Titles = existing.Titles
		}
		if existing.HomePage != nil {
			ys.HomePage = &yamlHomePage{
				Title:   existing.HomePage.Title,
				Titles:  existing.HomePage.Titles,
				Layout:  existing.HomePage.Layout,
				Widgets: existing.HomePage.Widgets,
			}
		}
	} else if formHasMapField(r, "titles") {
		ys.Titles = parseMapForm(r, "titles")
	}

	// Раскладка виджетов рабочего стола из формы: режим «Авто» — отмеченные
	// галочками виджеты одним рядом; «По рядам» — ряды из drag-конструктора.
	// Перезаписывает rows/layout, сохраняя title/titles рабочего стола.
	rows, layout := rowsFromForm(r)
	if len(rows) > 0 {
		if ys.HomePage == nil {
			ys.HomePage = &yamlHomePage{}
		}
		ys.HomePage.Rows = rows
		ys.HomePage.Layout = layout
		ys.HomePage.Widgets = nil // rows и flat widgets взаимоисключающи
	} else if ys.HomePage != nil {
		ys.HomePage.Rows = nil
		// Если от рабочего стола ничего не осталось — убираем секцию целиком.
		if ys.HomePage.Title == "" && len(ys.HomePage.Titles) == 0 &&
			ys.HomePage.Layout == "" && len(ys.HomePage.Widgets) == 0 {
			ys.HomePage = nil
		}
	}

	out, _ := yaml.Marshal(&ys)

	data := h.loadCfgData(r.Context(), b, "tree")
	if err := os.WriteFile(targetFile, out, 0o644); err != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	data.FieldsSaved = true
	data.FieldsSavedEntity = subName
	renderCfg(w, r, data)
}

// ── App config save ───────────────────────────────────────────────────────────

func (h *handler) configuratorSaveApp(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Parse multipart form (up to 2 MiB for the logo plus framing).
	lang := resolveLang(r)
	const maxLogoBytes = int64(2 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+(1<<20))
	if err := r.ParseMultipartForm(maxLogoBytes); err != nil {
		http.Error(w, tr(lang, "Ошибка чтения формы")+": "+err.Error(), requestBodyErrorStatus(err))
		return
	}
	newName := strings.TrimSpace(r.FormValue("app_name"))
	newVersion := strings.TrimSpace(r.FormValue("app_version"))
	newLang := strings.TrimSpace(r.FormValue("app_lang"))
	newAuthor := strings.TrimSpace(r.FormValue("app_author"))
	newCopyright := strings.TrimSpace(r.FormValue("app_copyright"))
	newLicense := strings.TrimSpace(r.FormValue("app_license"))
	existingLogo := strings.TrimSpace(r.FormValue("app_logo_existing"))
	removeLogo := r.FormValue("app_logo_remove") == "1"

	if newName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя конфигурации не может быть пустым")
		renderCfg(w, r, data)
		return
	}

	// Determine logo path
	logoPath := existingLogo
	if removeLogo {
		logoPath = ""
	}

	// Handle uploaded logo file. The write itself is delayed so app.yaml and
	// logo changes become one config version in database mode.
	var (
		logoData      []byte
		hasLogoUpload bool
	)
	file, header, ferr := r.FormFile("app_logo_file")
	if ferr == nil {
		defer file.Close()
		data, rerr := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
		if rerr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка чтения логотипа") + ": " + rerr.Error()
			renderCfg(w, r, data)
			return
		}
		if int64(len(data)) > maxLogoBytes {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Логотип слишком большой (максимум 2 МБ)")
			renderCfg(w, r, data)
			return
		}
		// Determine storage path
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext == "" {
			ext = ".png"
		}
		logoPath = "config/logo" + ext
		logoData = data
		hasLogoUpload = true
	}

	type saveAppConfig struct {
		Name      string `yaml:"name"`
		Version   string `yaml:"version,omitempty"`
		Lang      string `yaml:"lang,omitempty"`
		Logo      string `yaml:"logo,omitempty"`
		Author    string `yaml:"author,omitempty"`
		Copyright string `yaml:"copyright,omitempty"`
		License   string `yaml:"license,omitempty"`
	}
	out, _ := yaml.Marshal(saveAppConfig{
		Name: newName, Version: newVersion, Lang: newLang, Logo: logoPath,
		Author: newAuthor, Copyright: newCopyright, License: newLicense,
	})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			repo := configdb.New(db)
			if err := repo.EnsureSchema(r.Context()); err != nil {
				saveErr = err
			} else {
				saves := []configdb.ConfigFile{{Path: "config/app.yaml", Content: out}}
				if hasLogoUpload {
					saves = append([]configdb.ConfigFile{{Path: logoPath, Content: logoData}}, saves...)
				}
				var deletes []string
				if removeLogo && existingLogo != "" {
					deletes = append(deletes, existingLogo)
				}
				saveErr = repo.ApplyFiles(r.Context(), saves, deletes, configdb.VersionOptions{
					AuthorLogin: cfgLogin(r.Context()),
					Message:     "save app settings",
				})
			}
		}
	} else {
		if removeLogo && existingLogo != "" {
			full, err := configdb.SafeJoin(b.Path, existingLogo)
			if err != nil {
				saveErr = err
			} else if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				saveErr = err
			}
		}
		if saveErr == nil && hasLogoUpload {
			saveErr = h.writeConfigFileRaw(r.Context(), b, logoPath, logoData)
		}
		if saveErr == nil {
			saveErr = h.writeConfigFileRaw(r.Context(), b, "config/app.yaml", out)
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "__app__"
	}
	renderCfg(w, r, data)
}

// ── InfoRegister field save ───────────────────────────────────────────────────
