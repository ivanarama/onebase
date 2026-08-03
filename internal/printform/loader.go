package printform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DSLPrintForm describes a print form implemented as a DSL module (.os file).
type DSLPrintForm struct {
	Name       string          // form name (filename without extension)
	Document   string          // entity name (extracted from first comment line or directory)
	Source     string          // raw .os source code
	Layout     *LayoutTemplate // associated макет template (optional)
	LayoutPath string          // path to .layout.yaml file (empty if no layout)
}

// LayoutForm — декларативная печатная форма: standalone *.layout.yaml без парного
// .os (план 64, этап 3). Рендерится движком BuildSheet по Layout.Binding.
// Document берётся из поля document макета или из имени папки-сущности.
type LayoutForm struct {
	Name     string          // имя формы (имя файла без .layout.yaml)
	Document string          // имя сущности
	Layout   *LayoutTemplate // макет v2 + binding
	Path     string          // путь к .layout.yaml
}

// LoadFile parses a single YAML print form file.
func LoadFile(path string) (*PrintForm, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("printform: read %s: %w", path, err)
	}
	pf, err := ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("printform: parse %s: %w", path, err)
	}
	if pf.Name == "" {
		pf.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	return pf, nil
}

// ParseBytes разбирает YAML печатной формы из памяти (без файла). Нужна для
// внешних форм, которые хранятся в БД (см. internal/extform). Имя формы здесь
// не подставляется из имени файла — вызывающий обязан задать его сам, если в
// YAML поле name пустое.
func ParseBytes(data []byte) (*PrintForm, error) {
	var pf PrintForm
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, err
	}
	return &pf, nil
}

// LoadDir loads all *.yaml, *.os and standalone *.layout.yaml files from the given
// directory as print forms. For subdirectories, .os files inside are associated
// with the subdirectory name as the Document; standalone *.layout.yaml (without a
// paired .os) becomes a declarative LayoutForm (план 64, этап 3).
// Returns nil values if the directory does not exist.
func LoadDir(dir string) ([]*PrintForm, []*DSLPrintForm, []*LayoutForm, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("printform: readdir %s: %w", dir, err)
	}
	var forms []*PrintForm
	var dslForms []*DSLPrintForm
	var layoutForms []*LayoutForm
	for _, item := range items {
		name := item.Name()
		if item.IsDir() {
			df, lf, err := loadSubdir(dir, name)
			if err != nil {
				return nil, nil, nil, err
			}
			dslForms = append(dslForms, df...)
			layoutForms = append(layoutForms, lf...)
			continue
		}
		switch {
		case strings.HasSuffix(name, ".layout.yaml"):
			// Парный с .os макет уже подхватывается через loadLayoutForFile —
			// здесь обрабатываем только standalone (без одноимённого .os).
			osPath := filepath.Join(dir, strings.TrimSuffix(name, ".layout.yaml")+".os")
			if _, err := os.Stat(osPath); err == nil {
				continue // парный — пропускаем (загрузится вместе с .os)
			}
			lf, err := loadLayoutForm(filepath.Join(dir, name), "")
			if err != nil {
				return nil, nil, nil, err
			}
			layoutForms = append(layoutForms, lf)
		case strings.HasSuffix(name, ".yaml"):
			pf, err := LoadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, nil, nil, err
			}
			forms = append(forms, pf)
		case strings.HasSuffix(name, ".os"):
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, nil, nil, fmt.Errorf("printform: read %s: %w", name, err)
			}
			src := string(data)
			docName := extractDocument(src, "")
			lt2, ltPath2 := loadLayoutForFile(filepath.Join(dir, name))
			dslForms = append(dslForms, &DSLPrintForm{
				Name:       strings.TrimSuffix(name, ".os"),
				Document:   docName,
				Source:     src,
				Layout:     lt2,
				LayoutPath: ltPath2,
			})
		}
	}
	return forms, dslForms, layoutForms, nil
}

// loadSubdir загружает .os формы и standalone .layout.yaml из подпапки сущности
// (имя папки = Document по умолчанию).
func loadSubdir(dir, folder string) ([]*DSLPrintForm, []*LayoutForm, error) {
	subItems, err := os.ReadDir(filepath.Join(dir, folder))
	if err != nil {
		return nil, nil, nil
	}
	var dslForms []*DSLPrintForm
	var layoutForms []*LayoutForm
	for _, si := range subItems {
		if si.IsDir() {
			continue
		}
		siName := si.Name()
		switch {
		case strings.HasSuffix(siName, ".os"):
			data, err := os.ReadFile(filepath.Join(dir, folder, siName))
			if err != nil {
				return nil, nil, fmt.Errorf("printform: read %s/%s: %w", folder, siName, err)
			}
			src := string(data)
			docName := extractDocument(src, folder)
			lt, ltPath := loadLayoutForFile(filepath.Join(dir, folder, siName))
			dslForms = append(dslForms, &DSLPrintForm{
				Name:       strings.TrimSuffix(siName, ".os"),
				Document:   docName,
				Source:     src,
				Layout:     lt,
				LayoutPath: ltPath,
			})
		case strings.HasSuffix(siName, ".layout.yaml"):
			osPath := filepath.Join(dir, folder, strings.TrimSuffix(siName, ".layout.yaml")+".os")
			if _, err := os.Stat(osPath); err == nil {
				continue // парный с .os
			}
			lf, err := loadLayoutForm(filepath.Join(dir, folder, siName), folder)
			if err != nil {
				return nil, nil, err
			}
			layoutForms = append(layoutForms, lf)
		}
	}
	return dslForms, layoutForms, nil
}

// loadLayoutForm загружает standalone .layout.yaml как декларативную форму.
// Document берётся из поля document макета, иначе из folderDoc (имя папки).
func loadLayoutForm(path, folderDoc string) (*LayoutForm, error) {
	lt, err := LoadLayout(path)
	if err != nil {
		return nil, err
	}
	doc := lt.Document
	if doc == "" {
		doc = folderDoc
	}
	return &LayoutForm{
		Name:     strings.TrimSuffix(filepath.Base(path), ".layout.yaml"),
		Document: doc,
		Layout:   lt,
		Path:     path,
	}, nil
}

// extractDocument tries to extract the entity name from the first comment line
// like "// Документ: Счёт" or "// Document: Invoice". Falls back to folderName.
func extractDocument(src, folderName string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "//") {
			break
		}
		comment := strings.TrimSpace(strings.TrimPrefix(line, "//"))
		for _, prefix := range []string{"Документ:", "Document:", "документ:"} {
			if strings.HasPrefix(comment, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(comment, prefix))
			}
		}
	}
	return folderName
}

// loadLayoutForFile tries to find and load a .layout.yaml file matching the given .os file.
func loadLayoutForFile(osPath string) (*LayoutTemplate, string) {
	layoutPath := FindLayoutFile(osPath)
	if layoutPath == "" {
		return nil, ""
	}
	lt, err := LoadLayout(layoutPath)
	if err != nil {
		return nil, ""
	}
	return lt, layoutPath
}
