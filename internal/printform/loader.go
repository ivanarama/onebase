package printform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile parses a single YAML print form file.
func LoadFile(path string) (*PrintForm, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("printform: read %s: %w", path, err)
	}
	var pf PrintForm
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("printform: parse %s: %w", path, err)
	}
	if pf.Name == "" {
		pf.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	return &pf, nil
}

// LoadDir loads all *.yaml files from the given directory as print forms.
// Returns nil, nil if the directory does not exist.
func LoadDir(dir string) ([]*PrintForm, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("printform: readdir %s: %w", dir, err)
	}
	var forms []*PrintForm
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		pf, err := LoadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		forms = append(forms, pf)
	}
	return forms, nil
}
