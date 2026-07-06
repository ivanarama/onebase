package parser1c

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// xmlProperties — корневой элемент Metadata.xml 1С (старый формат)
type xmlProperties struct {
	XMLName xml.Name `xml:"Properties"`
	Name    string   `xml:"Name"`
	Synonym xmlLang  `xml:"Synonym"`
	// Catalogs/Documents attributes
	Attributes      []xmlAttribute      `xml:"Attributes>Attribute"`
	TabularSections []xmlTabularSection `xml:"TabularSections>TabularSection"`
	// Accumulation registers
	Dimensions []xmlAttribute `xml:"Dimensions>Dimension"`
	Resources  []xmlAttribute `xml:"Resources>Resource"`
}

type xmlLang struct {
	Content string `xml:"content"`
}

type xmlAttribute struct {
	Name    string  `xml:"Properties>Name"`
	Synonym xmlLang `xml:"Properties>Synonym"`
	Type    xmlType `xml:"Properties>Type"`
}

type xmlTabularSection struct {
	Name       string         `xml:"Properties>Name"`
	Synonym    xmlLang        `xml:"Properties>Synonym"`
	Attributes []xmlAttribute `xml:"Attributes>Attribute"`
}

type xmlType struct {
	Types []string `xml:"Types>Type"`
}

// v8.3 MDClasses XML structures (sibling .xml files, MetaDataObject root)
type xmlV8Root struct {
	Catalog         *xmlV8Obj      `xml:"Catalog"`
	Document        *xmlV8Obj      `xml:"Document"`
	Task            *xmlV8Obj      `xml:"Task"`
	DataProcessor   *xmlV8Obj      `xml:"DataProcessor"`
	BusinessProcess *xmlV8Obj      `xml:"BusinessProcess"`
	AccReg          *xmlV8Obj      `xml:"AccumulationRegister"`
	InfoReg         *xmlV8Obj      `xml:"InformationRegister"`
	AcctReg         *xmlV8Obj      `xml:"AccountingRegister"`
	Enum            *xmlV8Enum     `xml:"Enumeration"`
	EnumAlias       *xmlV8Enum     `xml:"Enum"`
	Constant        *xmlV8Const    `xml:"Constant"`
	ChartOfAccounts *xmlV8Obj      `xml:"ChartOfAccounts"`
	ScheduledJob    *xmlV8SchedJob `xml:"ScheduledJob"`
}

type xmlV8Obj struct {
	Props        xmlV8ObjProps `xml:"Properties"`
	ChildObjects xmlV8Children `xml:"ChildObjects"`
}

type xmlV8ObjProps struct {
	Name string `xml:"Name"`
}

type xmlV8Children struct {
	Attributes      []xmlV8Attr    `xml:"Attribute"`
	Dimensions      []xmlV8Attr    `xml:"Dimension"`
	Resources       []xmlV8Attr    `xml:"Resource"`
	TabularSections []xmlV8TabSect `xml:"TabularSection"`
}

type xmlV8Attr struct {
	Props xmlV8AttrProps `xml:"Properties"`
}

type xmlV8AttrProps struct {
	Name string    `xml:"Name"`
	Type xmlV8Type `xml:"Type"`
}

type xmlV8Type struct {
	Types []string `xml:"http://v8.1c.ru/8.1/data/core Type"`
}

type xmlV8TabSect struct {
	Props        xmlV8ObjProps `xml:"Properties"`
	ChildObjects xmlV8Children `xml:"ChildObjects"`
}

type xmlV8EnumValue struct {
	Props xmlV8ObjProps `xml:"Properties"`
}

type xmlV8Enum struct {
	Props        xmlV8ObjProps `xml:"Properties"`
	ChildObjects struct {
		Values []xmlV8EnumValue `xml:"EnumValue"`
	} `xml:"ChildObjects"`
}

type xmlV8ConstProps struct {
	Name string    `xml:"Name"`
	Type xmlV8Type `xml:"Type"`
}

type xmlV8Const struct {
	Props xmlV8ConstProps `xml:"Properties"`
}

type xmlV8SchedJobProps struct {
	Name       string `xml:"Name"`
	Schedule   string `xml:"Schedule"`
	MethodName string `xml:"MethodName"`
}

type xmlV8SchedJob struct {
	Props xmlV8SchedJobProps `xml:"Properties"`
}

// parseV83File пробует прочитать файл как v8.3 MDClasses XML.
// Возвращает nil, nil если файл не существует или не является v8.3 форматом.
func parseV83File(path string) (*xmlV8Root, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var root xmlV8Root
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, nil
	}
	// Файл является v8.3 MDClasses, если внутри распознан хотя бы один известный
	// тип объекта. Раньше проверялись только Catalog/Document/AccReg — из-за этого
	// перечисления, константы, регистры сведений и т.п. молча отбраковывались
	// (см. issue #16: «Перечислений: 0 → 0», хотя <Enumeration> в выгрузке есть).
	if root.Catalog == nil && root.Document == nil && root.Task == nil &&
		root.DataProcessor == nil && root.BusinessProcess == nil &&
		root.AccReg == nil && root.InfoReg == nil && root.AcctReg == nil &&
		root.enumObject() == nil && root.Constant == nil && root.ChartOfAccounts == nil &&
		root.ScheduledJob == nil {
		return nil, nil
	}
	return &root, nil
}

func (r *xmlV8Root) enumObject() *xmlV8Enum {
	if r == nil {
		return nil
	}
	if r.Enum != nil {
		return r.Enum
	}
	return r.EnumAlias
}

func convertV83Attrs(attrs []xmlV8Attr) []Attribute {
	var result []Attribute
	for _, a := range attrs {
		result = append(result, Attribute{
			Name: a.Props.Name,
			Type: parseType(a.Props.Type.Types),
		})
	}
	return result
}

// ParseDir читает директорию выгрузки конфигурации 1С и возвращает ConfigDump.
func ParseDir(dir string) (*ConfigDump, error) {
	dump := &ConfigDump{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("parser1c: read dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		kind := e.Name()
		subDir := filepath.Join(dir, kind)

		switch kind {
		case "Catalogs":
			cats, err := parseCatalogs(subDir)
			if err != nil {
				return nil, err
			}
			dump.Catalogs = append(dump.Catalogs, cats...)

		case "Documents":
			docs, err := parseDocuments(subDir)
			if err != nil {
				return nil, err
			}
			dump.Documents = append(dump.Documents, docs...)

		case "AccumulationRegisters":
			regs, err := parseRegisters(subDir)
			if err != nil {
				return nil, err
			}
			dump.Registers = append(dump.Registers, regs...)

		case "Enumerations", "Enums":
			enums, err := parseEnumerations(subDir)
			if err != nil {
				return nil, err
			}
			dump.Enums = append(dump.Enums, enums...)

		case "Constants":
			consts, err := parseConstants(subDir)
			if err != nil {
				return nil, err
			}
			dump.Constants = append(dump.Constants, consts...)

		case "InformationRegisters":
			iregs, err := parseInfoRegisters(subDir)
			if err != nil {
				return nil, err
			}
			dump.InfoRegisters = append(dump.InfoRegisters, iregs...)

		case "AccountingRegisters":
			aregs, err := parseAccountingRegisters(subDir)
			if err != nil {
				return nil, err
			}
			dump.AccountRegisters = append(dump.AccountRegisters, aregs...)

		case "ChartsOfAccounts":
			charts, err := parseChartsOfAccounts(subDir)
			if err != nil {
				return nil, err
			}
			dump.ChartsOfAccounts = append(dump.ChartsOfAccounts, charts...)

		case "ScheduledJobs":
			jobs, err := parseScheduledJobs(subDir)
			if err != nil {
				return nil, err
			}
			dump.ScheduledJobs = append(dump.ScheduledJobs, jobs...)

		case "ConfigDumpInfo.xml", "config.xml", "Languages":
			// служебный файл / языки — пропускаем

		case "CommonPictures", "Styles", "StyleItems", "Interfaces",
			"WebServices", "HTTPServices", "WSReferences", "XDTOPackages",
			"CommonCommandGroups", "CommandInterfaces":
			// ресурсы оформления/интеграции — не прикладные данные. Раньше они
			// попадали в default-ветку и конвертировались в справочники
			// (issue #16: «логотип» из CommonPictures становился справочником).
			collectSkipped(subDir, kind, dump)

		case "CommonModules":
			mods, err := parseCommonModules(subDir)
			if err != nil {
				return nil, err
			}
			dump.Modules = append(dump.Modules, mods...)

		case "DataProcessors":
			procs, err := parseDataProcessors(subDir)
			if err != nil {
				return nil, err
			}
			dump.Processors = append(dump.Processors, procs...)

		case "Subsystems", "Tasks", "BusinessProcesses",
			"ExchangePlans", "ChartsOfCharacteristicTypes",
			"ChartsOfCalculationTypes", "FilterCriteria",
			"SettingsStorages", "FunctionalOptions",
			"FunctionalOptionsParameters", "DefinedTypes",
			"CommandGroups", "Roles", "CommonTemplates",
			"CommonForms", "CommonCommands",
			"CommonAttributes", "EventSubscriptions",
			"SequenceRegisters", "Recalculations",
			"CalculationRegisters", "ExternalDataSources":
			// Не прикладные данные (подсистемы, роли, общие формы/макеты, планы
			// обмена и т.п.). Раньше они конвертировались в справочники — отсюда
			// «лишние» справочники из подсистемы/логотипа (issue #26 п.1, #16 п.4).
			// Теперь только помечаем как пропущенные.
			collectSkipped(subDir, kind, dump)

		default:
			// Неизвестный раздел. Никогда не конвертируем «вслепую» в справочник —
			// помечаем как пропущенный, чтобы не плодить фантомные объекты.
			collectSkipped(subDir, kind, dump)
		}
	}

	return dump, nil
}

// objectNames возвращает имена объектов раздела выгрузки: объединение имён
// подкаталогов и одиночных *.xml-файлов (без расширения), без дубликатов.
// Объект без подчинённых элементов представлен в выгрузке 1С только файлом
// «Имя.xml» — перебор одних подкаталогов молча терял такие объекты
// (issue #16 для перечислений, issue #48 п.1 для остальных типов).
func objectNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}
	for _, e := range entries {
		if e.IsDir() {
			add(e.Name())
		} else if strings.EqualFold(filepath.Ext(e.Name()), ".xml") {
			add(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	return names
}

func parseCatalogs(dir string) ([]*CatalogMeta, error) {
	var result []*CatalogMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil {
			obj := v8.Catalog
			if obj == nil {
				obj = v8.Task
			}
			if obj == nil {
				obj = v8.DataProcessor
			}
			if obj == nil {
				obj = v8.BusinessProcess
			}
			if obj != nil {
				cat := &CatalogMeta{
					Name:       orDefault(obj.Props.Name, name),
					Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
				}
				for _, ts := range obj.ChildObjects.TabularSections {
					cat.TabularSections = append(cat.TabularSections, TabularSection{
						Name:       ts.Props.Name,
						Attributes: convertV83Attrs(ts.ChildObjects.Attributes),
					})
				}
				cat.Forms = scanForms(filepath.Join(dir, name), cat.Name)
				result = append(result, cat)
				continue
			}
		}

		metaFile := filepath.Join(dir, name, "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			metaFile = filepath.Join(dir, name, "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			result = append(result, &CatalogMeta{Name: name})
			continue
		}
		cat := &CatalogMeta{
			Name:       orDefault(props.Name, name),
			Synonym:    props.Synonym.Content,
			Attributes: convertAttrs(props.Attributes),
		}
		for _, ts := range props.TabularSections {
			cat.TabularSections = append(cat.TabularSections, TabularSection{
				Name:       ts.Name,
				Synonym:    ts.Synonym.Content,
				Attributes: convertAttrs(ts.Attributes),
			})
		}
		cat.Forms = scanForms(filepath.Join(dir, name), cat.Name)
		result = append(result, cat)
	}
	return result, nil
}

func parseDocuments(dir string) ([]*DocumentMeta, error) {
	var result []*DocumentMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.Document != nil {
			obj := v8.Document
			doc := &DocumentMeta{
				Name:       orDefault(obj.Props.Name, name),
				Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
			}
			for _, ts := range obj.ChildObjects.TabularSections {
				doc.TabularSections = append(doc.TabularSections, TabularSection{
					Name:       ts.Props.Name,
					Attributes: convertV83Attrs(ts.ChildObjects.Attributes),
				})
			}
			doc.Forms = scanForms(filepath.Join(dir, name), doc.Name)
			result = append(result, doc)
			continue
		}

		metaFile := filepath.Join(dir, name, "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			metaFile = filepath.Join(dir, name, "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			result = append(result, &DocumentMeta{Name: name})
			continue
		}
		doc := &DocumentMeta{
			Name:       orDefault(props.Name, name),
			Synonym:    props.Synonym.Content,
			Attributes: convertAttrs(props.Attributes),
		}
		for _, ts := range props.TabularSections {
			doc.TabularSections = append(doc.TabularSections, TabularSection{
				Name:       ts.Name,
				Synonym:    ts.Synonym.Content,
				Attributes: convertAttrs(ts.Attributes),
			})
		}
		doc.Forms = scanForms(filepath.Join(dir, name), doc.Name)
		result = append(result, doc)
	}
	return result, nil
}

func parseRegisters(dir string) ([]*RegisterMeta, error) {
	var result []*RegisterMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.AccReg != nil {
			obj := v8.AccReg
			result = append(result, &RegisterMeta{
				Name:       orDefault(obj.Props.Name, name),
				Dimensions: convertV83Attrs(obj.ChildObjects.Dimensions),
				Resources:  convertV83Attrs(obj.ChildObjects.Resources),
				Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
			})
			continue
		}

		metaFile := filepath.Join(dir, name, "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			metaFile = filepath.Join(dir, name, "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			result = append(result, &RegisterMeta{Name: name})
			continue
		}
		result = append(result, &RegisterMeta{
			Name:       orDefault(props.Name, name),
			Synonym:    props.Synonym.Content,
			Dimensions: convertAttrs(props.Dimensions),
			Resources:  convertAttrs(props.Resources),
			Attributes: convertAttrs(props.Attributes),
		})
	}
	return result, nil
}

func parseMetaFile(path string) (*xmlProperties, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var props xmlProperties
	if err := xml.Unmarshal(data, &props); err != nil {
		// попробуем другой корневой тег
		type xmlRoot struct {
			Properties xmlProperties `xml:"Properties"`
		}
		var root xmlRoot
		if err2 := xml.Unmarshal(data, &root); err2 != nil {
			return nil, err
		}
		props = root.Properties
	}
	return &props, nil
}

func convertAttrs(xmlAttrs []xmlAttribute) []Attribute {
	var result []Attribute
	for _, a := range xmlAttrs {
		attr := Attribute{
			Name:    a.Name,
			Synonym: a.Synonym.Content,
			Type:    parseType(a.Type.Types),
		}
		result = append(result, attr)
	}
	return result
}

func parseType(types []string) FieldType {
	if len(types) == 0 {
		return FieldType{Primary: "string"}
	}
	if len(types) > 1 {
		return FieldType{Composite: true, AllTypes: types}
	}
	t := types[0]
	ft := FieldType{Primary: t}
	// Извлечь имя объекта из ссылки (CatalogRef.X, cfg:CatalogRef.X, DocumentRef.X)
	bare := strings.TrimPrefix(t, "cfg:")
	if strings.Contains(bare, ".") && !strings.HasPrefix(bare, "xs:") {
		parts := strings.SplitN(bare, ".", 2)
		if len(parts) == 2 {
			ft.RefObject = parts[1]
		}
	}
	return ft
}

// collectSkipped помечает все подкаталоги раздела как пропущенные (не
// конвертируемые в прикладные объекты). Используется для служебных разделов
// выгрузки 1С (подсистемы, роли, картинки, общие формы и т.п.).
func collectSkipped(subDir, kind string, dump *ConfigDump) {
	objects, _ := os.ReadDir(subDir)
	for _, obj := range objects {
		if obj.IsDir() {
			dump.SkippedDirs = append(dump.SkippedDirs, SkippedItem{Kind: kind, Name: obj.Name()})
		}
	}
}

// scanForms ищет управляемые формы объекта в каталоге <ownerDir>/Forms/<X>/Ext/Form.xml.
// Возвращает источники форм для последующего импорта через onec_forms.
func scanForms(ownerDir, entity string) []FormSource {
	formsDir := filepath.Join(ownerDir, "Forms")
	entries, err := os.ReadDir(formsDir)
	if err != nil {
		return nil
	}
	var out []FormSource
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		extDir := filepath.Join(formsDir, e.Name(), "Ext")
		if _, err := os.Stat(filepath.Join(extDir, "Form.xml")); err != nil {
			continue
		}
		out = append(out, FormSource{Entity: entity, FormName: e.Name(), ExtDir: extDir})
	}
	return out
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

func parseEnumerations(dir string) ([]*EnumMeta, error) {
	var result []*EnumMeta
	for _, name := range objectNames(dir) {
		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.enumObject() != nil {
			enumObj := v8.enumObject()
			em := &EnumMeta{Name: orDefault(enumObj.Props.Name, name)}
			for _, v := range enumObj.ChildObjects.Values {
				em.Values = append(em.Values, v.Props.Name)
			}
			result = append(result, em)
			continue
		}
		result = append(result, &EnumMeta{Name: name})
	}
	return result, nil
}

func parseConstants(dir string) ([]*ConstantMeta, error) {
	var result []*ConstantMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.Constant != nil {
			result = append(result, &ConstantMeta{
				Name: orDefault(v8.Constant.Props.Name, name),
				Type: parseType(v8.Constant.Props.Type.Types),
			})
			continue
		}
		result = append(result, &ConstantMeta{Name: name, Type: FieldType{Primary: "string"}})
	}
	return result, nil
}

func parseInfoRegisters(dir string) ([]*InfoRegMeta, error) {
	var result []*InfoRegMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.InfoReg != nil {
			obj := v8.InfoReg
			result = append(result, &InfoRegMeta{
				Name:       orDefault(obj.Props.Name, name),
				Dimensions: convertV83Attrs(obj.ChildObjects.Dimensions),
				Resources:  convertV83Attrs(obj.ChildObjects.Resources),
				Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
			})
			continue
		}
		result = append(result, &InfoRegMeta{Name: name})
	}
	return result, nil
}

func parseAccountingRegisters(dir string) ([]*AccountRegMeta, error) {
	var result []*AccountRegMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.AcctReg != nil {
			obj := v8.AcctReg
			result = append(result, &AccountRegMeta{
				Name:       orDefault(obj.Props.Name, name),
				Dimensions: convertV83Attrs(obj.ChildObjects.Dimensions),
				Resources:  convertV83Attrs(obj.ChildObjects.Resources),
				Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
			})
			continue
		}
		result = append(result, &AccountRegMeta{Name: name})
	}
	return result, nil
}

func parseChartsOfAccounts(dir string) ([]*ChartOfAccountsMeta, error) {
	var result []*ChartOfAccountsMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.ChartOfAccounts != nil {
			obj := v8.ChartOfAccounts
			result = append(result, &ChartOfAccountsMeta{
				Name:       orDefault(obj.Props.Name, name),
				Attributes: convertV83Attrs(obj.ChildObjects.Attributes),
			})
			continue
		}
		result = append(result, &ChartOfAccountsMeta{Name: name})
	}
	return result, nil
}

func parseScheduledJobs(dir string) ([]*ScheduledJobMeta, error) {
	var result []*ScheduledJobMeta
	for _, name := range objectNames(dir) {

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.ScheduledJob != nil {
			p := v8.ScheduledJob.Props
			result = append(result, &ScheduledJobMeta{
				Name:     orDefault(p.Name, name),
				Schedule: p.Schedule,
				Handler:  p.MethodName,
			})
			continue
		}
		result = append(result, &ScheduledJobMeta{Name: name})
	}
	return result, nil
}

func parseCommonModules(dir string) ([]*ModuleMeta, error) {
	var result []*ModuleMeta
	for _, name := range objectNames(dir) {
		mod := &ModuleMeta{Name: name}

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil {
			for _, obj := range []*xmlV8Obj{v8.Catalog, v8.Task, v8.DataProcessor, v8.BusinessProcess} {
				if obj != nil {
					mod.Name = orDefault(obj.Props.Name, name)
					break
				}
			}
		}

		bslPath := filepath.Join(dir, name, "Ext", "Module.bsl")
		if data, err := os.ReadFile(bslPath); err == nil {
			mod.Source = string(data)
		} else {
			bslPath = filepath.Join(dir, name, "Module.bsl")
			if data, err := os.ReadFile(bslPath); err == nil {
				mod.Source = string(data)
			}
		}

		result = append(result, mod)
	}
	return result, nil
}

func parseDataProcessors(dir string) ([]*ProcessorMeta, error) {
	var result []*ProcessorMeta
	for _, name := range objectNames(dir) {
		proc := &ProcessorMeta{Name: name}

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil {
			obj := v8.DataProcessor
			if obj == nil {
				obj = v8.Catalog
			}
			if obj != nil {
				proc.Name = orDefault(obj.Props.Name, name)
				proc.Attributes = convertV83Attrs(obj.ChildObjects.Attributes)
			}
		} else {
			metaFile := filepath.Join(dir, name, "Ext", "Metadata.xml")
			if _, serr := os.Stat(metaFile); os.IsNotExist(serr) {
				metaFile = filepath.Join(dir, name, "Metadata.xml")
			}
			if props, perr := parseMetaFile(metaFile); perr == nil {
				proc.Name = orDefault(props.Name, name)
				proc.Synonym = props.Synonym.Content
				proc.Attributes = convertAttrs(props.Attributes)
			}
		}

		bslPath := filepath.Join(dir, name, "Ext", "ObjectModule.bsl")
		if data, err := os.ReadFile(bslPath); err == nil {
			proc.Source = string(data)
		} else {
			bslPath = filepath.Join(dir, name, "ObjectModule.bsl")
			if data, err := os.ReadFile(bslPath); err == nil {
				proc.Source = string(data)
			}
		}

		proc.Forms = scanForms(filepath.Join(dir, name), proc.Name)
		result = append(result, proc)
	}
	return result, nil
}
