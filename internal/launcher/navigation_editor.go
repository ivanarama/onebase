package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/ui"
	"gopkg.in/yaml.v3"
)

const maxNavigationEditorBody = 2 << 20

type navigationEditorRequest struct {
	Subsystem string         `json:"subsystem"`
	Menu      *metadata.Menu `json:"menu"`
	Lang      string         `json:"lang,omitempty"`
}

type navigationPaletteItem struct {
	Target string `json:"target"`
	Label  string `json:"label"`
}

type navigationEditorData struct {
	BaseID    string                        `json:"base_id"`
	Subsystem string                        `json:"subsystem"`
	Title     string                        `json:"title"`
	Lang      string                        `json:"lang"`
	Menu      *metadata.Menu                `json:"menu"`
	Palette   []navigationPaletteItem       `json:"palette"`
	Preview   []ui.NavigationPreviewSection `json:"preview"`
	Warnings  []string                      `json:"warnings"`
	Error     string                        `json:"error,omitempty"`
}

type navigationEditorContext struct {
	reg      *runtime.Registry
	contents *metadata.SubsystemContents
	menu     *metadata.Menu
	sub      string
	title    string
	relPath  string
	raw      []byte
	global   bool
}

func navigationRegistry(p *project.Project) *runtime.Registry {
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: p.Entities, Registers: p.Registers, InfoRegs: p.InfoRegisters,
		Enums: p.Enums, Constants: p.Constants, Reports: p.Reports})
	reg.LoadProcessors(p.Processors)
	reg.LoadPages(p.Pages)
	reg.LoadJournals(p.Journals)
	reg.LoadSubsystems(p.Subsystems)
	reg.LoadHomePage(p.HomePage)
	return reg
}

// Source paths are discovered from the actual snapshot. An object's name need
// not match its filename, and the client never chooses a path to overwrite.
func (h *handler) navigationEditorContext(ctx context.Context, b *Base, p *project.Project, sub, lang string) (*navigationEditorContext, error) {
	c := &navigationEditorContext{reg: navigationRegistry(p), global: sub == "", relPath: "config/home_page.yaml", title: tr(lang, "Главная")}
	if c.global {
		if p.HomePage != nil {
			c.contents, c.menu = p.HomePage.Nav, p.HomePage.Menu
		}
	} else {
		var selected *metadata.Subsystem
		for _, candidate := range p.Subsystems {
			if strings.EqualFold(candidate.Name, sub) {
				selected = candidate
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("подсистема %q не найдена", sub)
		}
		c.sub, c.title, c.contents, c.menu = selected.Name, selected.DisplayName(lang), &selected.Contents, selected.Menu
		c.relPath = ""
	}
	files, err := h.listConfiguratorFiles(ctx, b)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if c.global {
			if file.Path == c.relPath {
				c.raw = file.Content
			}
			continue
		}
		if path.Dir(file.Path) != "subsystems" || path.Ext(file.Path) != ".yaml" {
			continue
		}
		var header struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(file.Content, &header); err != nil {
			return nil, err
		}
		if header.Name == "" {
			header.Name = strings.TrimSuffix(path.Base(file.Path), ".yaml")
		}
		if strings.EqualFold(header.Name, c.sub) {
			if c.relPath != "" {
				return nil, fmt.Errorf("несколько YAML-файлов подсистемы %q", c.sub)
			}
			c.relPath, c.raw = file.Path, file.Content
		}
	}
	if c.relPath == "" {
		return nil, fmt.Errorf("YAML подсистемы %q не найден", c.sub)
	}
	return c, nil
}

func (c *navigationEditorContext) scope() navigation.Scope {
	return navigation.NewScope(ui.NavigationObjects(c.reg), c.contents, c.global)
}

func (c *navigationEditorContext) validate(menu *metadata.Menu) ([]string, error) {
	context := "global"
	if !c.global {
		context = "subsystem:" + c.sub
	}
	_, diagnostics := navigation.Normalize(context, menu, c.scope())
	warnings := []string{}
	var failures []string
	for _, d := range diagnostics {
		message := d.Code + ": " + d.Node + ": " + d.Message
		if d.Warning {
			warnings = append(warnings, message)
		} else {
			failures = append(failures, message)
		}
	}
	if len(failures) != 0 {
		return warnings, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return warnings, nil
}

func (c *navigationEditorContext) data(baseID, lang string, menu *metadata.Menu) navigationEditorData {
	d := navigationEditorData{BaseID: baseID, Subsystem: c.sub, Title: c.title, Lang: lang, Menu: menu, Palette: []navigationPaletteItem{}}
	translate := func(key string) string { return tr(lang, key) }
	for _, section := range c.scope().Sections {
		for _, item := range section.Items {
			d.Palette = append(d.Palette, navigationPaletteItem{Target: item.Target, Label: item.Object.Label(lang, translate, c.global && c.contents.IsEmpty())})
		}
	}
	var err error
	d.Warnings, err = c.validate(menu)
	if err != nil {
		d.Error = err.Error()
	} else {
		d.Preview = ui.AdminNavigationPreview(c.reg, menu, c.contents, c.global, c.sub, lang, launcherBundle)
	}
	return d
}

// Import materializes the legacy projection into an editable draft. tree_order
// is an explicit hint for unordered flat navigation; contents arrays keep their
// declared order. Neither import path writes configuration or tree_order.
func (c *navigationEditorContext) importMenu(order map[string][]string, lang string) *metadata.Menu {
	sections := c.scope().Sections
	// Flat legacy navigation sorts translated labels. Import the actual runtime
	// order for this language before applying explicit configurator hints.
	if c.global && c.contents.IsEmpty() {
		for _, group := range ui.AdminNavigationPreview(c.reg, nil, c.contents, true, "", lang, launcherBundle) {
			positions := map[string]int{}
			for i, item := range group.Items {
				positions[item.ID] = i
			}
			for i := range sections {
				if sections[i].ID != group.ID {
					continue
				}
				sort.SliceStable(sections[i].Items, func(a, b int) bool {
					x, okx := positions[sections[i].Items[a].ID]
					y, oky := positions[sections[i].Items[b].ID]
					if okx != oky {
						return okx
					}
					return okx && x < y
				})
			}
		}
	}
	groupKeys := map[string]string{"catalog": "catalogs", "document": "documents", "register": "registers",
		"inforeg": "inforegisters", "report": "reports", "processor": "processors", "journal": "journals", "page": "pages", "system": "constants"}
	key := func(section navigation.Section) string {
		return groupKeys[strings.TrimPrefix(section.ID, "cfg:legacy-")]
	}
	if len(order) > 0 {
		positions := map[string]int{}
		for i, name := range order["groups"] {
			positions[name] = i
		}
		sort.SliceStable(sections, func(i, j int) bool {
			a, oka := positions[key(sections[i])]
			b, okb := positions[key(sections[j])]
			if oka != okb {
				return oka
			}
			return oka && a < b
		})
		if c.global && c.contents.IsEmpty() {
			for i := range sections {
				positions := map[string]int{}
				for j, name := range order[key(sections[i])] {
					positions[strings.ToLower(name)] = j
				}
				sort.SliceStable(sections[i].Items, func(a, b int) bool {
					x, okx := positions[strings.ToLower(sections[i].Items[a].Object.Target.Name)]
					y, oky := positions[strings.ToLower(sections[i].Items[b].Object.Target.Name)]
					if okx != oky {
						return okx
					}
					return okx && x < y
				})
			}
		}
	}
	menu := &metadata.Menu{Sections: []metadata.MenuSection{}}
	for _, section := range sections {
		next := metadata.MenuSection{ID: strings.TrimPrefix(section.ID, "cfg:"), Title: section.Title,
			Titles: map[string]string{"en": tr("en", section.Title)}, Items: []metadata.MenuItem{}}
		for _, item := range section.Items {
			sum := sha256.Sum256([]byte(item.Target))
			next.Items = append(next.Items, metadata.MenuItem{ID: "i-" + hex.EncodeToString(sum[:])[:60], Target: item.Target})
		}
		menu.Sections = append(menu.Sections, next)
	}
	return menu
}

func (h *handler) configuratorNavigation(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := h.loadProjectFor(r.Context(), b)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	defer p.Close()
	lang := resolveLang(r)
	c, err := h.navigationEditorContext(r.Context(), b, p, r.URL.Query().Get("subsystem"), lang)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	menu := c.menu
	switch r.URL.Query().Get("import") {
	case "":
	case "legacy":
		menu = c.importMenu(nil, lang)
	case "tree-order":
		menu = c.importMenu(h.loadTreeOrderFor(r.Context(), b), lang)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "неизвестный режим импорта"})
		return
	}
	d := c.data(b.ID, lang, menu)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, d)
		return
	}
	renderNavigationEditor(w, d)
}

func decodeNavigationEditorRequest(w http.ResponseWriter, r *http.Request) (*navigationEditorRequest, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("ожидался Content-Type application/json")
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNavigationEditorBody))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("ожидался UTF-8 JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var request navigationEditorRequest
	if err := dec.Decode(&request); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("ожидался один JSON-объект")
	}
	return &request, nil
}

func (h *handler) configuratorNavigationPreview(w http.ResponseWriter, r *http.Request) {
	h.navigationEditorPost(w, r, false)
}

func (h *handler) configuratorNavigationSave(w http.ResponseWriter, r *http.Request) {
	h.navigationEditorPost(w, r, true)
}

func (h *handler) navigationEditorPost(w http.ResponseWriter, r *http.Request, save bool) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	request, err := decodeNavigationEditorRequest(w, r)
	if err != nil || (save && request.Menu == nil) {
		if err == nil {
			err = fmt.Errorf("создайте меню перед сохранением")
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	p, err := h.loadProjectFor(r.Context(), b)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	defer p.Close()
	lang := resolveLang(r)
	if request.Lang != "" {
		lang = request.Lang
	}
	c, err := h.navigationEditorContext(r.Context(), b, p, request.Subsystem, lang)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	d := c.data(b.ID, lang, request.Menu)
	if d.Error != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": d.Error})
		return
	}
	if save {
		out, err := updateYAMLMapping(c.raw, c.relPath, func(doc *yaml.Node) error { return setNavigationMenuYAML(doc, request.Menu) })
		if err == nil {
			err = saveConfigFile(r, h, b, c.relPath, out)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, d)
}
