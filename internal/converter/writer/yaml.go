package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
	"gopkg.in/yaml.v3"
)

type yamlField struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type yamlTablePart struct {
	Name   string      `yaml:"name"`
	Fields []yamlField `yaml:"fields"`
}

type yamlCatalog struct {
	Name   string      `yaml:"name"`
	Fields []yamlField `yaml:"fields"`
}

type yamlDocument struct {
	Name       string          `yaml:"name"`
	Fields     []yamlField     `yaml:"fields"`
	TableParts []yamlTablePart `yaml:"tableparts,omitempty"`
}

type yamlRegister struct {
	Name       string      `yaml:"name"`
	Dimensions []yamlField `yaml:"dimensions"`
	Resources  []yamlField `yaml:"resources"`
	Attributes []yamlField `yaml:"attributes,omitempty"`
}

// WriteCatalogs записывает справочники в out/catalogs/*.yaml.
func WriteCatalogs(cats []*parser1c.CatalogMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "catalogs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, cat := range cats {
		obj := yamlCatalog{
			Name:   cat.Name,
			Fields: convertFields(cat.Attributes, notes),
		}
		if err := writeYAML(filepath.Join(dir, fileName(cat.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Catalogs++
	}
	return nil
}

// WriteDocuments записывает документы в out/documents/*.yaml.
func WriteDocuments(docs []*parser1c.DocumentMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, doc := range docs {
		obj := yamlDocument{
			Name:   doc.Name,
			Fields: convertFields(doc.Attributes, notes),
		}
		for _, ts := range doc.TabularSections {
			obj.TableParts = append(obj.TableParts, yamlTablePart{
				Name:   ts.Name,
				Fields: convertFields(ts.Attributes, notes),
			})
		}
		if err := writeYAML(filepath.Join(dir, fileName(doc.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Documents++
	}
	return nil
}

// WriteRegisters записывает регистры накопления в out/registers/*.yaml.
func WriteRegisters(regs []*parser1c.RegisterMeta, outDir string, notes *ConversionReport) error {
	dir := filepath.Join(outDir, "registers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, reg := range regs {
		obj := yamlRegister{
			Name:       reg.Name,
			Dimensions: convertFields(reg.Dimensions, notes),
			Resources:  convertFields(reg.Resources, notes),
			Attributes: convertFields(reg.Attributes, notes),
		}
		if err := writeYAML(filepath.Join(dir, fileName(reg.Name)+".yaml"), obj); err != nil {
			return err
		}
		notes.Registers++
	}
	return nil
}

func convertFields(attrs []parser1c.Attribute, notes *ConversionReport) []yamlField {
	var fields []yamlField
	for _, a := range attrs {
		t, note := parser1c.MapType(a.Type)
		f := yamlField{Name: a.Name, Type: t}
		fields = append(fields, f)
		if note != "" {
			notes.TypeWarnings = append(notes.TypeWarnings, fmt.Sprintf("%s.%s: %s", "field", a.Name, note))
		}
	}
	return fields
}

func writeYAML(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	return enc.Encode(v)
}

// fileName преобразует имя объекта 1С в имя файла (lowercase, без пробелов).
func fileName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}
