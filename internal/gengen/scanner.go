package gengen

import (
	oblog "github.com/ivantit66/onebase/internal/logging"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// ExistingManifest describes what entities already exist in a project.
type ExistingManifest struct {
	Catalogs  map[string]CatalogInfo
	Documents map[string]DocumentInfo
	Registers map[string]RegisterInfo
	Enums     map[string]EnumInfo
	DSLFiles  map[string]string // relative path → content
}

// CatalogInfo summarizes a catalog's structure.
type CatalogInfo struct {
	Name       string
	Fields     []FieldInfo
	TableParts []TablePartInfo
	Posting    bool
}

// DocumentInfo summarizes a document's structure.
type DocumentInfo struct {
	Name       string
	Fields     []FieldInfo
	TableParts []TablePartInfo
	Posting    bool
}

// RegisterInfo summarizes a register's structure.
type RegisterInfo struct {
	Name       string
	Dimensions []FieldInfo
	Resources  []FieldInfo
}

// EnumInfo summarizes an enum's values.
type EnumInfo struct {
	Name   string
	Values []string
}

// FieldInfo is a lightweight field descriptor for comparison.
type FieldInfo struct {
	Name      string
	Type      string
	RefEntity string
	EnumName  string
}

// TablePartInfo summarizes a table part.
type TablePartInfo struct {
	Name   string
	Fields []FieldInfo
}

// ScanProject reads an existing project directory and builds an ExistingManifest.
func ScanProject(dir string) (*ExistingManifest, error) {
	proj, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	defer proj.Close()

	manifest := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	// Catalogs
	for _, e := range proj.Entities {
		if e.Kind == metadata.KindCatalog {
			manifest.Catalogs[e.Name] = CatalogInfo{
				Name:       e.Name,
				Fields:     extractFields(e.Fields),
				TableParts: extractTableParts(e.TableParts),
				Posting:    e.Posting,
			}
		}
	}

	// Documents
	for _, e := range proj.Entities {
		if e.Kind == metadata.KindDocument {
			manifest.Documents[e.Name] = DocumentInfo{
				Name:       e.Name,
				Fields:     extractFields(e.Fields),
				TableParts: extractTableParts(e.TableParts),
				Posting:    e.Posting,
			}
		}
	}

	// Registers
	for _, reg := range proj.Registers {
		manifest.Registers[reg.Name] = RegisterInfo{
			Name:       reg.Name,
			Dimensions: extractFields(reg.Dimensions),
			Resources:  extractFields(reg.Resources),
		}
	}

	// Enums
	for _, enum := range proj.Enums {
		manifest.Enums[enum.Name] = EnumInfo{
			Name:   enum.Name,
			Values: append([]string(nil), enum.Values...),
		}
	}

	// DSL files
	dslDir := filepath.Join(dir, "src")
	if _, err := os.Stat(dslDir); err == nil {
		walkErr := filepath.WalkDir(dslDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".os") {
				return nil
			}
			content, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(dslDir, path)
			manifest.DSLFiles[rel] = string(content)
			return nil
		})
		if walkErr != nil {
			oblog.Component("gengen").Debug("обход каталога прерван", "err", walkErr)
		}
	}

	return manifest, nil
}

// ScanProjectFromFiles reads entity YAML files directly without full project loading.
// Useful when project.Load() fails due to missing dependencies (DB, etc.).
func ScanProjectFromFiles(dir string) (*ExistingManifest, error) {
	manifest := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	// Scan catalogs
	catalogDir := filepath.Join(dir, "catalogs")
	scanEntityDir(catalogDir, metadata.KindCatalog, func(e *metadata.Entity) {
		manifest.Catalogs[e.Name] = CatalogInfo{
			Name:       e.Name,
			Fields:     extractFields(e.Fields),
			TableParts: extractTableParts(e.TableParts),
			Posting:    e.Posting,
		}
	})

	// Scan documents
	docDir := filepath.Join(dir, "documents")
	scanEntityDir(docDir, metadata.KindDocument, func(e *metadata.Entity) {
		manifest.Documents[e.Name] = DocumentInfo{
			Name:       e.Name,
			Fields:     extractFields(e.Fields),
			TableParts: extractTableParts(e.TableParts),
			Posting:    e.Posting,
		}
	})

	// Scan enums
	enumDir := filepath.Join(dir, "enums")
	scanEnumDir(enumDir, func(name string, values []string) {
		manifest.Enums[name] = EnumInfo{
			Name:   name,
			Values: values,
		}
	})

	// Scan DSL files
	dslDir := filepath.Join(dir, "src")
	if _, err := os.Stat(dslDir); err == nil {
		walkErr := filepath.WalkDir(dslDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".os") {
				return nil
			}
			content, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(dslDir, path)
			manifest.DSLFiles[rel] = string(content)
			return nil
		})
		if walkErr != nil {
			oblog.Component("gengen").Debug("обход каталога прерван", "err", walkErr)
		}
	}

	return manifest, nil
}

// scanEntityDir reads all YAML files in a directory and calls fn for each entity.
func scanEntityDir(dir string, kind metadata.Kind, fn func(*metadata.Entity)) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		e, err := metadata.LoadFile(path, kind)
		if err != nil {
			return nil // skip invalid files
		}
		fn(e)
		return nil
	})
	if walkErr != nil {
		oblog.Component("gengen").Debug("обход каталога прерван", "err", walkErr)
	}
}

// scanEnumDir reads enum YAML files.
// Поддерживает оба формата values:
//   - старый: values: [A, B, C]          (строки)
//   - новый:  values: [{name: A, titles: {en: ...}}, ...]
//
// gengen использует только имена значений; переводы игнорируются.
func scanEnumDir(dir string, fn func(name string, values []string)) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		en, err := metadata.LoadEnumFile(path)
		if err != nil {
			return nil
		}
		if en.Name != "" {
			fn(en.Name, en.Values)
		}
		return nil
	})
	if walkErr != nil {
		oblog.Component("gengen").Debug("обход каталога прерван", "err", walkErr)
	}
}

func extractFields(fields []metadata.Field) []FieldInfo {
	out := make([]FieldInfo, 0, len(fields))
	for _, f := range fields {
		out = append(out, FieldInfo{
			Name:      f.Name,
			Type:      string(f.Type),
			RefEntity: f.RefEntity,
			EnumName:  f.EnumName,
		})
	}
	return out
}

func extractTableParts(tps []metadata.TablePart) []TablePartInfo {
	out := make([]TablePartInfo, 0, len(tps))
	for _, tp := range tps {
		out = append(out, TablePartInfo{
			Name:   tp.Name,
			Fields: extractFields(tp.Fields),
		})
	}
	return out
}
