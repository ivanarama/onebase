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

type rawTablePart struct {
	Name   string     `yaml:"name"`
	Fields []rawField `yaml:"fields"`
}

type rawEntity struct {
	Name       string         `yaml:"name"`
	Fields     []rawField     `yaml:"fields"`
	TableParts []rawTablePart `yaml:"tableparts"`
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
		e.Fields = append(e.Fields, parseField(rf))
	}
	for _, rtp := range raw.TableParts {
		tp := TablePart{Name: rtp.Name}
		for _, rf := range rtp.Fields {
			tp.Fields = append(tp.Fields, parseField(rf))
		}
		e.TableParts = append(e.TableParts, tp)
	}
	return e, nil
}

type rawRegister struct {
	Name       string     `yaml:"name"`
	Dimensions []rawField `yaml:"dimensions"`
	Resources  []rawField `yaml:"resources"`
	Attributes []rawField `yaml:"attributes"`
}

func LoadRegisterFile(path string) (*Register, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawRegister
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	reg := &Register{Name: raw.Name}
	for _, rf := range raw.Dimensions {
		reg.Dimensions = append(reg.Dimensions, parseField(rf))
	}
	for _, rf := range raw.Resources {
		reg.Resources = append(reg.Resources, parseField(rf))
	}
	for _, rf := range raw.Attributes {
		reg.Attributes = append(reg.Attributes, parseField(rf))
	}
	return reg, nil
}

func parseField(rf rawField) Field {
	f := Field{Name: rf.Name, Type: FieldType(rf.Type)}
	if strings.HasPrefix(rf.Type, "reference:") {
		f.RefEntity = strings.TrimPrefix(rf.Type, "reference:")
	}
	return f
}
