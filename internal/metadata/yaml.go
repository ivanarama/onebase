package metadata

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type rawField struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type rawEntity struct {
	Name   string     `yaml:"name"`
	Fields []rawField `yaml:"fields"`
}

func LoadFile(path string, kind Kind) (*Entity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawEntity
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	e := &Entity{Name: raw.Name, Kind: kind}
	for _, rf := range raw.Fields {
		f := Field{Name: rf.Name, Type: FieldType(rf.Type)}
		if strings.HasPrefix(rf.Type, "reference:") {
			f.RefEntity = strings.TrimPrefix(rf.Type, "reference:")
		}
		e.Fields = append(e.Fields, f)
	}
	return e, nil
}
