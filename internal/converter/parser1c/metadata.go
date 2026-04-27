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
	Name    string   `xml:"Properties>Name"`
	Synonym xmlLang  `xml:"Properties>Synonym"`
	Type    xmlType  `xml:"Properties>Type"`
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
	Catalog  *xmlV8Obj `xml:"Catalog"`
	Document *xmlV8Obj `xml:"Document"`
	AccReg   *xmlV8Obj `xml:"AccumulationRegister"`
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
	if root.Catalog == nil && root.Document == nil && root.AccReg == nil {
		return nil, nil
	}
	return &root, nil
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

		case "ConfigDumpInfo.xml", "config.xml":
			// служебный файл

		default:
			// пропускаем неизвестные разделы
			objects, _ := os.ReadDir(subDir)
			for _, obj := range objects {
				if obj.IsDir() {
					dump.SkippedDirs = append(dump.SkippedDirs, SkippedItem{Kind: kind, Name: obj.Name()})
				}
			}
		}
	}

	return dump, nil
}

func parseCatalogs(dir string) ([]*CatalogMeta, error) {
	var result []*CatalogMeta
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

		if v8, _ := parseV83File(filepath.Join(dir, name+".xml")); v8 != nil && v8.Catalog != nil {
			result = append(result, &CatalogMeta{
				Name:       orDefault(v8.Catalog.Props.Name, name),
				Attributes: convertV83Attrs(v8.Catalog.ChildObjects.Attributes),
			})
			continue
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
		result = append(result, &CatalogMeta{
			Name:       orDefault(props.Name, name),
			Synonym:    props.Synonym.Content,
			Attributes: convertAttrs(props.Attributes),
		})
	}
	return result, nil
}

func parseDocuments(dir string) ([]*DocumentMeta, error) {
	var result []*DocumentMeta
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

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
		result = append(result, doc)
	}
	return result, nil
}

func parseRegisters(dir string) ([]*RegisterMeta, error) {
	var result []*RegisterMeta
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

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

func parseType(types []string) FieldType1C {
	if len(types) == 0 {
		return FieldType1C{Primary: "string"}
	}
	if len(types) > 1 {
		return FieldType1C{Composite: true, AllTypes: types}
	}
	t := types[0]
	ft := FieldType1C{Primary: t}
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

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
