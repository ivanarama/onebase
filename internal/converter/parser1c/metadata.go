package parser1c

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// xmlProperties — корневой элемент Metadata.xml 1С
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
	// Register attributes (extra info)
	AddAttributes []xmlAttribute `xml:"Attributes>Attribute"`
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
		metaFile := filepath.Join(dir, e.Name(), "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			// попробуем прямо Metadata.xml
			metaFile = filepath.Join(dir, e.Name(), "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			// нет файла — используем имя папки
			result = append(result, &CatalogMeta{Name: e.Name()})
			continue
		}
		cat := &CatalogMeta{
			Name:       orDefault(props.Name, e.Name()),
			Synonym:    props.Synonym.Content,
			Attributes: convertAttrs(props.Attributes),
		}
		result = append(result, cat)
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
		metaFile := filepath.Join(dir, e.Name(), "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			metaFile = filepath.Join(dir, e.Name(), "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			result = append(result, &DocumentMeta{Name: e.Name()})
			continue
		}
		doc := &DocumentMeta{
			Name:       orDefault(props.Name, e.Name()),
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
		metaFile := filepath.Join(dir, e.Name(), "Ext", "Metadata.xml")
		if _, err := os.Stat(metaFile); os.IsNotExist(err) {
			metaFile = filepath.Join(dir, e.Name(), "Metadata.xml")
		}
		props, err := parseMetaFile(metaFile)
		if err != nil {
			result = append(result, &RegisterMeta{Name: e.Name()})
			continue
		}
		reg := &RegisterMeta{
			Name:       orDefault(props.Name, e.Name()),
			Synonym:    props.Synonym.Content,
			Dimensions: convertAttrs(props.Dimensions),
			Resources:  convertAttrs(props.Resources),
			Attributes: convertAttrs(props.Attributes),
		}
		result = append(result, reg)
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
	// Извлечь имя объекта из ссылки
	if strings.Contains(t, ".") {
		parts := strings.SplitN(t, ".", 2)
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
