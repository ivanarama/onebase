package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/version"
	"gopkg.in/yaml.v3"
)

func configDirtyAfter(rootDir string, threshold time.Time) bool {
	dirty := false
	filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "backups" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".os" && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.ModTime().After(threshold) {
			dirty = true
			return filepath.SkipAll
		}
		return nil
	})
	return dirty
}

func (h *handler) loadCfgData(ctx context.Context, b *Base, tab string, lang ...string) *configuratorData {
	l := "ru"
	if len(lang) > 0 {
		l = lang[0]
	}
	data := &configuratorData{Base: b, Tab: tab, PlatformVer: version.String(), UIServerURL: fmt.Sprintf("http://localhost:%d", b.Port), DSNMasked: maskDSN(b.DB), InlineJSYaml: InlineJSYaml}

	if startedAt, ok := h.runner.StartedAt(b.ID); ok {
		data.IsRunning = true
		if b.ConfigSource == "file" {
			// Звёздочка означает «БД/сессия отстала от метаданных»: сравниваем с
			// моментом последней миграции, а при её отсутствии — с моментом запуска.
			threshold := startedAt
			if t, ok := migratedAt(b.ID); ok && t.After(threshold) {
				threshold = t
			}
			data.ConfigDirty = configDirtyAfter(b.Path, threshold)
		}
	}

	var proj *project.Project
	var err error

	if b.ConfigSource == "database" {
		db, cerr := OpenDB(ctx, b)
		if cerr != nil {
			data.Error = tr(l, "Нет подключения к БД") + ": " + cerr.Error()
			return data
		}
		defer db.Close()
		repo := configdb.New(db)
		if cerr := repo.EnsureSchema(ctx); cerr != nil {
			data.Error = cerr.Error()
			return data
		}
		empty, _ := repo.IsEmpty(ctx)
		if empty {
			data.Error = tr(l, "Конфигурация не загружена в базу данных. Воспользуйтесь вкладкой «Файлы».")
			return data
		}
		proj, err = project.LoadFromDB(ctx, repo)
	} else {
		proj, err = project.Load(b.Path)
	}

	if err != nil {
		data.Error = tr(l, "Ошибка загрузки конфигурации") + ": " + err.Error()
		return data
	}
	defer proj.Close()

	// Вкладка «Файлы»: дерево файлов конфигурации (issue #132).
	if tab == "files" {
		data.ConfigFileTree = h.buildConfigFileTree(ctx, b, proj)
	}

	if appCfg, _ := project.LoadConfig(proj.Dir); appCfg != nil {
		data.AppName = appCfg.Name
		data.AppVersion = appCfg.Version
		data.AppLogo = appCfg.Logo
		data.AppLang = appCfg.Lang
		data.AppAuthor = appCfg.Author
		data.AppCopyright = appCfg.Copyright
		data.AppLicense = appCfg.License
	}
	if launcherBundle != nil {
		data.AvailableLangs = launcherBundle.Available()
	}

	sources, postingSources, managerSources := readOSSources(proj.Dir)

	for _, e := range proj.Entities {
		ev := cfgEntity{
			Name:          e.Name,
			Posting:       e.Posting,
			Hierarchical:  e.Hierarchical,
			BasedOn:       append([]string(nil), e.BasedOn...),
			Source:        sources[strings.ToLower(e.Name)],
			PostingSource: postingSources[strings.ToLower(e.Name)],
			ManagerSource: managerSources[strings.ToLower(e.Name)],
			Titles:        e.Titles,
		}
		if e.Kind == metadata.KindCatalog {
			ev.Kind = "Справочник"
		} else {
			ev.Kind = "Документ"
		}
		if e.Activity != nil {
			ev.Activity = &cfgActivity{
				Field:          e.Activity.Field,
				DefaultScope:   e.Activity.DefaultScope,
				HideFromChoice: e.Activity.HideFromChoice,
			}
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
		for _, pd := range e.Predefined {
			pv := cfgPredefined{Name: pd.Name, Fields: make(map[string]string, len(pd.Fields))}
			for k, v := range pd.Fields {
				pv.Fields[k] = fmt.Sprintf("%v", v)
			}
			ev.Predefined = append(ev.Predefined, pv)
		}
		// Mark fields hidden based on form config
		if len(e.ListForm) > 0 {
			lfSet := make(map[string]bool, len(e.ListForm))
			for _, n := range e.ListForm {
				lfSet[n] = true
			}
			for i := range ev.Fields {
				if !lfSet[ev.Fields[i].Name] {
					ev.Fields[i].FormListHidden = true
				}
			}
		}
		if len(e.ItemForm) > 0 {
			ifSet := make(map[string]bool, len(e.ItemForm))
			for _, n := range e.ItemForm {
				ifSet[n] = true
			}
			for i := range ev.Fields {
				if !ifSet[ev.Fields[i].Name] {
					ev.Fields[i].FormItemHidden = true
				}
			}
		}
		data.Entities = append(data.Entities, ev)
	}

	sort.Slice(data.Entities, func(i, j int) bool {
		return data.Entities[i].Name < data.Entities[j].Name
	})

	// Обратный индекс: какие документы/справочники вводятся НА ОСНОВАНИИ
	// текущего объекта. Показывается на форме источника как read-only —
	// аналог одноимённой вкладки в 1С:Конфигураторе.
	for i := range data.Entities {
		src := data.Entities[i].Name
		for j := range data.Entities {
			for _, b := range data.Entities[j].BasedOn {
				if strings.EqualFold(b, src) {
					data.Entities[i].Receivers = append(data.Entities[i].Receivers, data.Entities[j].Name)
					break
				}
			}
		}
	}

	// load print forms and link to entities
	pfSources := readPrintFormSources(proj.Dir)

	// индекс «(doc,name)→true» по .os формам, чтобы пометить
	// YAML-формы, которые runtime тихо игнорирует.
	dslIndex := make(map[string]bool, len(proj.DSLPrintForms))
	for _, df := range proj.DSLPrintForms {
		dslIndex[strings.ToLower(df.Document)+"|"+strings.ToLower(df.Name)] = true
	}
	// И обратный индекс для пометки .os-форм, которые перебивают YAML.
	yamlIndex := make(map[string]bool, len(proj.PrintForms))
	for _, pf := range proj.PrintForms {
		yamlIndex[strings.ToLower(pf.Document)+"|"+strings.ToLower(pf.Name)] = true
	}

	pfByDoc := make(map[string][]cfgPrintForm)
	for _, pf := range proj.PrintForms {
		entry := pfSources[pf.Name]
		cpf := cfgPrintForm{
			Name:     pf.Name,
			Document: pf.Document,
			Source:   entry.source,
			FileName: entry.filename,
			Shadowed: dslIndex[strings.ToLower(pf.Document)+"|"+strings.ToLower(pf.Name)],
		}
		data.PrintForms = append(data.PrintForms, cpf)
		pfByDoc[strings.ToLower(pf.Document)] = append(pfByDoc[strings.ToLower(pf.Document)], cpf)
	}

	// load DSL print forms (.os)
	for _, df := range proj.DSLPrintForms {
		cpf := cfgDSLPrintForm{
			Name:      df.Name,
			Document:  df.Document,
			Source:    df.Source,
			FileName:  df.Name + ".os",
			Overrides: yamlIndex[strings.ToLower(df.Document)+"|"+strings.ToLower(df.Name)],
		}
		if df.Layout != nil {
			cpf.HasLayout = true
			cpf.LayoutPreview = template.HTML(df.Layout.PreviewHTML())
			if df.LayoutPath != "" {
				if raw, err := os.ReadFile(df.LayoutPath); err == nil {
					cpf.LayoutYAML = string(raw)
				}
			}
		}
		data.DSLPrintForms = append(data.DSLPrintForms, cpf)
	}
	for _, e := range data.Entities {
		data.AllEntityNames = append(data.AllEntityNames, e.Name)
		e.LinkedPrintForms = pfByDoc[strings.ToLower(e.Name)]
		if e.Kind == "Справочник" {
			data.Catalogs = append(data.Catalogs, e)
		} else {
			data.Docs = append(data.Docs, e)
		}
	}

	for _, reg := range proj.Registers {
		rv := cfgRegister{Name: reg.Name, Title: reg.Title, Titles: reg.Titles}
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

	for _, ir := range proj.InfoRegisters {
		rv := cfgInfoRegister{Name: ir.Name, Title: ir.Title, Titles: ir.Titles, Periodic: ir.Periodic}
		for _, f := range ir.Dimensions {
			rv.Dimensions = append(rv.Dimensions, toCfgField(f))
		}
		for _, f := range ir.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.InfoRegisters = append(data.InfoRegisters, rv)
	}

	for _, ar := range proj.AccountRegisters {
		rv := cfgAccountRegister{Name: ar.Name, Title: ar.Title, Titles: ar.Titles, Accounts: ar.Accounts}
		for _, f := range ar.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.AccountRegisters = append(data.AccountRegisters, rv)
	}

	for _, en := range proj.Enums {
		ev := cfgEnum{Name: en.Name, Values: en.Values}
		for _, v := range en.Values {
			ev.EnumValues = append(ev.EnumValues, cfgEnumValue{
				Name:   v,
				Titles: en.ValueTitles[v],
			})
		}
		data.Enums = append(data.Enums, ev)
		data.AllEnumNames = append(data.AllEnumNames, en.Name)
	}

	for _, c := range proj.Constants {
		typ := string(c.Type)
		if c.RefEntity != "" {
			typ = "reference"
		}
		data.Constants = append(data.Constants, cfgConstant{
			Name:      c.Name,
			Type:      typ,
			RefEntity: c.RefEntity,
			Default:   c.Default,
			Label:     c.Label,
			Labels:    c.Labels,
			Length:    c.Length,
			Scale:     c.Scale,
		})
	}

	repSources := readReportSources(proj.Dir)

	for _, rep := range proj.Reports {
		rv := cfgReport{Name: rep.Name, Title: rep.Title, Titles: rep.Titles, Query: rep.Query, ChartProc: rep.ChartProc, Composition: rep.Composition}
		if src, ok := repSources[strings.ToLower(rep.Name)]; ok {
			rv.ChartSource = src
		}
		for _, p := range rep.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label, Labels: p.Labels})
		}
		data.Reports = append(data.Reports, rv)
	}

	moduleSources, procSources := readModuleAndProcSources(proj.Dir)

	for name := range proj.Modules {
		data.Modules = append(data.Modules, cfgModule{
			Name:   name,
			Source: moduleSources[strings.ToLower(name)],
		})
	}
	sort.Slice(data.Modules, func(i, j int) bool { return data.Modules[i].Name < data.Modules[j].Name })

	for _, proc := range proj.Processors {
		rv := cfgProcessor{
			Name:   proc.Name,
			Title:  proc.Title,
			Titles: proc.Titles,
			Source: procSources[strings.ToLower(proc.Name)],
		}
		for _, p := range proc.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label, Labels: p.Labels})
		}
		data.Processors = append(data.Processors, rv)
	}
	sort.Slice(data.Processors, func(i, j int) bool { return data.Processors[i].Name < data.Processors[j].Name })

	// Pages (план 66): метаданные pages/*.yaml + обработчик src/*.page.os.
	// proj.Pages уже отсортирован по имени (page.LoadDir).
	pageSources := readPageSources(proj.Dir)
	for _, pg := range proj.Pages {
		data.Pages = append(data.Pages, cfgPage{
			Name:   pg.Name,
			Title:  pg.Title,
			Titles: pg.Titles,
			Icon:   pg.Icon,
			Roles:  append([]string(nil), pg.Roles...),
			Params: append([]string(nil), pg.Params...),
			Source: pageSources[strings.ToLower(pg.Name)],
		})
	}

	// Subsystems
	for _, sub := range proj.Subsystems {
		var rows [][]string
		if sub.HomePage != nil {
			rows = sub.HomePage.RowGroups()
		}
		data.Subsystems = append(data.Subsystems, cfgSubsystem{
			Name:        sub.Name,
			Title:       sub.Title,
			Titles:      sub.Titles,
			Icon:        sub.Icon,
			Order:       sub.Order,
			Contents:    sub.Contents,
			HomeWidgets: homeWidgetsNames(sub.HomePage),
			HomeRows:    rows,
			HomeLayout:  homeLayoutMode(sub.HomePage),
		})
	}

	// Widgets + home page
	widgetSources := readWidgetSources(proj.Dir)
	for _, wMeta := range proj.Widgets {
		yamlText, ok := widgetSources[wMeta.Name]
		if !ok {
			// Fallback: re-marshal from parsed metadata so the editor still
			// shows something even when the source file was lost.
			b, _ := yaml.Marshal(wMeta)
			yamlText = string(b)
		}
		data.Widgets = append(data.Widgets, cfgWidget{
			Name:  wMeta.Name,
			Type:  string(wMeta.Type),
			Title: wMeta.Title,
			YAML:  yamlText,
		})
	}
	sort.Slice(data.Widgets, func(i, j int) bool { return data.Widgets[i].Name < data.Widgets[j].Name })
	for _, wc := range data.Widgets {
		data.WidgetOptions = append(data.WidgetOptions, widgetOption{Name: wc.Name, Title: wc.Title})
	}
	data.HomePageYAML = readHomePageYAML(proj.Dir)

	// Глобальная главная для визуального редактора (галочки / drag-конструктор).
	ghTitle := ""
	var ghRows [][]string
	var ghTitles map[string]string
	if proj.HomePage != nil {
		ghRows = proj.HomePage.RowGroups()
		if proj.HomePage.Title != "" && proj.HomePage.Title != "Главная" {
			ghTitle = proj.HomePage.Title
		}
		ghTitles = proj.HomePage.Titles
	}
	data.GlobalHome = cfgHomePage{
		Title:   ghTitle,
		Titles:  ghTitles,
		Widgets: homeWidgetsNames(proj.HomePage),
		Rows:    ghRows,
		Layout:  homeLayoutMode(proj.HomePage),
	}

	// Generate query builder schema
	data.QBSchema = buildQBSchema(data)

	// Generate layout designer data-binding metadata
	data.LayoutMeta = buildLayoutMeta(data)

	// Backup dir & files
	data.BackupDir = h.backupDir(b)
	backupDir := data.BackupDir
	if files, err := backup.BackupFiles(backupDir); err == nil {
		for _, f := range files {
			data.BackupFiles = append(data.BackupFiles, backupFile{
				Name: filepath.Base(f.Path),
				Size: fmt.Sprintf("%.1f KB", float64(f.Info.Size())/1024),
				Date: f.Info.ModTime().Format("2006-01-02 15:04"),
			})
		}
	}

	// Backup settings from app.yaml
	{
		var raw []byte
		var readErr error
		if b.ConfigSource == "database" {
			if db, err := OpenDB(ctx, b); err == nil {
				readErr = db.QueryRow(ctx,
					"SELECT content FROM _onebase_config WHERE path='config/app.yaml'").Scan(&raw)
				db.Close()
			}
		} else {
			raw, readErr = os.ReadFile(filepath.Join(b.Path, "config", "app.yaml"))
		}
		if readErr == nil && len(raw) > 0 {
			var tmp struct {
				Backup struct {
					Enabled   bool   `yaml:"enabled"`
					Schedule  string `yaml:"schedule"`
					KeepLast  int    `yaml:"keep_last"`
					Directory string `yaml:"directory"`
				} `yaml:"backup"`
			}
			if yaml.Unmarshal(raw, &tmp) == nil {
				data.BackupSettings = backupSettings{
					Enabled:   tmp.Backup.Enabled,
					Schedule:  tmp.Backup.Schedule,
					KeepLast:  tmp.Backup.KeepLast,
					Directory: tmp.Backup.Directory,
				}
			}
		}
	}

	// Управляемые формы (план 37, этап 4): подгружаем для бокового дерева.
	// listManagedForms требует http.Request только для DB-режима; в file-mode
	// nil-Request не используется. В случае ошибки оставляем срез пустым —
	// дерево просто не покажет раздел.
	if forms, err := listManagedFormsFromFS(b); err == nil {
		data.ManagedForms = forms
	}
	if b.ConfigSource == "database" {
		if forms, err := h.listManagedFormsFromDBNoRequest(ctx, b); err == nil {
			data.ManagedForms = forms
		}
	}

	// Пользовательский порядок объектов и групп в дереве (ручное перемещение,
	// как в 1С) — одинаково для file- и database-режимов.
	applyTreeOrder(data, h.loadTreeOrderFor(ctx, b))

	return data
}

// populateBootstrap собирает JSON-блоб window.__cfg и словарь window.__cfgI18n
// из уже заполненных полей configuratorData. Вынесено отдельно, чтобы тест мог
// эмулировать продакшен-заполнение. lang — язык рендера (как в renderCfg).
func populateBootstrap(data *configuratorData, lang string) {
	// nil-срезы кодируем как [] (а не null): фронт делает .map()/.length по этим
	// массивам без guard'ов — старый {{range}} тоже отдавал [] для пустого среза.
	boot := map[string]any{
		"entityNames":    orEmpty(data.AllEntityNames),
		"enumNames":      orEmpty(data.AllEnumNames),
		"selectedTreeId": data.SelectedTreeID,
		"fieldsSaved":    data.FieldsSavedEntity,
		"moduleSaved":    data.ModuleSavedEntity,
		"baseId":         baseIDOf(data.Base),
		"basePort":       basePortOf(data.Base),
		"hasSession":     data.SessionToken != "",
		"groupOrder":     orEmpty(data.GroupOrder),
	}
	if bb, err := json.Marshal(boot); err == nil {
		data.Bootstrap = template.JS(bb)
	}
	var dict map[string]string
	if launcherBundle != nil {
		dict = launcherBundle.Dict(lang)
	}
	if ib, err := json.Marshal(dict); err == nil {
		data.I18n = template.JS(ib) // nil-словарь → "null"; в шаблоне `|| {}`
	}
}

// orEmpty гарантирует non-nil срез, чтобы json.Marshal дал [] вместо null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// baseIDOf/basePortOf — безопасные геттеры (data.Base может быть nil в тестах
// минимального рендера, например renderCfgFoot).
func baseIDOf(b *Base) string {
	if b == nil {
		return ""
	}
	return b.ID
}

func basePortOf(b *Base) int {
	if b == nil {
		return 0
	}
	return b.Port
}

// listManagedFormsFromDBNoRequest — версия для loadCfgData, где нет
// http.Request. Использует ctx напрямую вместо r.Context().
func (h *handler) listManagedFormsFromDBNoRequest(ctx context.Context, b *Base) ([]cfgManagedForm, error) {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	repo := configdb.New(db)
	files, err := repo.ListByPrefix(ctx, "forms/")
	if err != nil {
		return nil, err
	}
	type pair struct{ yaml, os string }
	groups := map[string]*cfgManagedForm{}
	for _, f := range files {
		parts := strings.Split(strings.TrimPrefix(f.Path, "forms/"), "/")
		if len(parts) < 2 {
			continue
		}
		entityLower := parts[0]
		base := parts[1]
		var name string
		var isYAML, isOS bool
		if strings.HasSuffix(base, ".form.yaml") {
			name = strings.TrimSuffix(base, ".form.yaml")
			isYAML = true
		} else if strings.HasSuffix(base, ".form.os") {
			name = strings.TrimSuffix(base, ".form.os")
			isOS = true
		} else {
			continue
		}
		key := entityLower + "/" + name
		g, ok := groups[key]
		if !ok {
			g = &cfgManagedForm{
				Entity:   entityLower,
				Name:     name,
				YAMLPath: "forms/" + entityLower + "/" + name + ".form.yaml",
				OSPath:   "forms/" + entityLower + "/" + name + ".form.os",
			}
			groups[key] = g
		}
		if isYAML {
			g.YAML = string(f.Content)
			g.Kind = extractFormKindFromYAML(g.YAML)
		}
		if isOS {
			g.OS = string(f.Content)
			g.HasOS = true
		}
	}
	out := make([]cfgManagedForm, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	return out, nil
}

// ── query builder schema ────────────────────────────────────────────────────

type cfgQBField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type cfgQBSource struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Group   string       `json:"group"`
	VTParam string       `json:"vtParam,omitempty"`
	Fields  []cfgQBField `json:"fields"`
}

func cfgQBFieldType(t string) string {
	switch t {
	case "number":
		return "number"
	case "date":
		return "date"
	case "bool", "boolean":
		return "bool"
	case "reference":
		return "ref"
	default:
		return "string"
	}
}

func buildQBSchema(d *configuratorData) template.JS {
	var sources []cfgQBSource

	for _, e := range d.Catalogs {
		src := cfgQBSource{ID: "catalog:" + e.Name, Label: "Справочник." + e.Name, Group: "Справочники"}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, src)
	}

	for _, e := range d.Docs {
		src := cfgQBSource{ID: "document:" + e.Name, Label: "Документ." + e.Name, Group: "Документы"}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, src)
	}

	for _, reg := range d.Registers {
		raw := cfgQBSource{ID: "register:" + reg.Name, Label: "РегистрНакопления." + reg.Name, Group: "Регистры накопления"}
		raw.Fields = append(raw.Fields, cfgQBField{Name: "период", Label: "Период", Type: "date"})
		raw.Fields = append(raw.Fields, cfgQBField{Name: "вид_движения", Label: "ВидДвижения", Type: "string"})
		for _, f := range reg.Dimensions {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, raw)

		bal := cfgQBSource{ID: "vt_balances:" + reg.Name, Label: "РегистрНакопления." + reg.Name + ".Остатки(&НаДату)", Group: "Виртуальные таблицы", VTParam: "&НаДату"}
		for _, f := range reg.Dimensions {
			bal.Fields = append(bal.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			bal.Fields = append(bal.Fields, cfgQBField{Name: f.Name + "Остаток", Label: f.Name + "Остаток", Type: "res"})
		}
		sources = append(sources, bal)

		trn := cfgQBSource{ID: "vt_turnovers:" + reg.Name, Label: "РегистрНакопления." + reg.Name + ".Обороты(&Начало, &Конец)", Group: "Виртуальные таблицы", VTParam: "&Начало, &Конец"}
		for _, f := range reg.Dimensions {
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Приход", Label: f.Name + "Приход", Type: "res"})
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Расход", Label: f.Name + "Расход", Type: "res"})
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Оборот", Label: f.Name + "Оборот", Type: "res"})
		}
		sources = append(sources, trn)
	}

	for _, ir := range d.InfoRegisters {
		raw := cfgQBSource{ID: "inforeg:" + ir.Name, Label: "РегистрСведений." + ir.Name, Group: "Регистры сведений"}
		if ir.Periodic {
			raw.Fields = append(raw.Fields, cfgQBField{Name: "period", Label: "Период", Type: "date"})
		}
		for _, f := range ir.Dimensions {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range ir.Resources {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, raw)

		if ir.Periodic {
			sl := cfgQBSource{ID: "vt_slice:" + ir.Name, Label: "РегистрСведений." + ir.Name + ".СрезПоследних(&НаДату)", Group: "Виртуальные таблицы", VTParam: "&НаДату"}
			for _, f := range ir.Dimensions {
				sl.Fields = append(sl.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
			}
			for _, f := range ir.Resources {
				sl.Fields = append(sl.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
			}
			sources = append(sources, sl)
		}
	}

	if sources == nil {
		sources = []cfgQBSource{}
	}
	b, _ := json.Marshal(sources)
	return template.JS(b)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toCfgField(f metadata.Field) cfgField {
	typ := string(f.Type)
	if f.RefEntity != "" {
		typ = "reference"
	} else if f.EnumName != "" {
		typ = "enum"
	}
	return cfgField{Name: f.Name, Type: typ, RefEntity: f.RefEntity, EnumName: f.EnumName, Length: f.Length, Scale: f.Scale, AllowInlineCreate: f.AllowInlineCreate, Titles: f.Titles}
}

func readOSSources(dir string) (sources, postingSources, managerSources map[string]string) {
	sources = make(map[string]string)
	postingSources = make(map[string]string)
	managerSources = make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".posting.os"):
			base := strings.ToLower(strings.TrimSuffix(name, ".posting.os"))
			postingSources[base] = string(raw)
		case strings.HasSuffix(name, ".manager.os"):
			base := strings.ToLower(strings.TrimSuffix(name, ".manager.os"))
			managerSources[base] = string(raw)
		case strings.HasSuffix(name, ".module.os"), strings.HasSuffix(name, ".proc.os"), strings.HasSuffix(name, ".rep.os"), strings.HasSuffix(name, ".page.os"):
			// skip — handled by readModuleAndProcSources / report / page editor
		default:
			base := strings.ToLower(strings.TrimSuffix(name, ".os"))
			sources[base] = string(raw)
		}
	}
	return
}

func readReportSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rep.os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(e.Name(), ".rep.os"))
		result[base] = string(raw)
	}
	return result
}

// readPageSources читает src/*.page.os (обработчики страниц, план 66) и
// раскладывает их по имени страницы в нижнем регистре — так же, как лукапит
// loadCfgData (pageSources[ToLower(pg.Name)]).
func readPageSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".page.os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(e.Name(), ".page.os"))
		result[base] = string(raw)
	}
	return result
}

// readWidgetSources reads widgets/*.yaml from the exported project dir and maps
// raw YAML content by widget name (extracted from the "name:" key, falling back
// to the file's basename). This lets the configurator show the source text in
// the editor without re-marshalling parsed metadata.
func readWidgetSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "widgets"))
	if err != nil {
		return result
	}
	type nameOnly struct {
		Name string `yaml:"name"`
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "widgets", e.Name()))
		if err != nil {
			continue
		}
		var no nameOnly
		key := strings.TrimSuffix(e.Name(), ".yaml")
		if yaml.Unmarshal(raw, &no) == nil && no.Name != "" {
			key = no.Name
		}
		result[key] = string(raw)
	}
	return result
}

// loadProjectFor загружает конфигурацию базы из файлов или из БД, повторяя
// ветвление loadCfgData. Используется обработчиками сохранения, которым нужны
// уже распарсенные метаданные (например, существующая главная страница).
// Вызывающий обязан вызвать proj.Close().
func (h *handler) loadProjectFor(ctx context.Context, b *Base) (*project.Project, error) {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			return nil, err
		}
		return project.LoadFromDB(ctx, repo)
	}
	return project.Load(b.Path)
}

// readHomePageYAML reads config/home_page.yaml verbatim. Empty string when the
// file is missing — caller decides whether to show a placeholder.
func readHomePageYAML(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "config", "home_page.yaml"))
	if err != nil {
		return ""
	}
	return string(raw)
}

func readModuleAndProcSources(dir string) (moduleSources, procSources map[string]string) {
	moduleSources = make(map[string]string)
	procSources = make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".os") {
			continue
		}
		name := e.Name()
		raw, err := os.ReadFile(filepath.Join(dir, "src", name))
		if err != nil {
			continue
		}
		if strings.HasSuffix(name, ".module.os") {
			base := strings.ToLower(strings.TrimSuffix(name, ".module.os"))
			moduleSources[base] = string(raw)
		} else if strings.HasSuffix(name, ".proc.os") {
			base := strings.ToLower(strings.TrimSuffix(name, ".proc.os"))
			procSources[base] = string(raw)
		}
	}
	return
}

type pfSourceEntry struct {
	source   string
	filename string
}

func readPrintFormSources(dir string) map[string]pfSourceEntry {
	result := make(map[string]pfSourceEntry)
	entries, err := os.ReadDir(filepath.Join(dir, "printforms"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "printforms", e.Name()))
		if err != nil {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &hdr) == nil && hdr.Name != "" {
			result[hdr.Name] = pfSourceEntry{source: string(raw), filename: e.Name()}
		}
	}
	return result
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
