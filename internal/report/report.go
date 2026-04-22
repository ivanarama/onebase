package report

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Param struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`    // string, date, number, select
	Label   string   `yaml:"label"`   // display label; falls back to Name
	Options []string `yaml:"options"` // for type: select
}

type Report struct {
	Name   string  `yaml:"name"`
	Title  string  `yaml:"title"`
	Params []Param `yaml:"params"`
	Query  string  `yaml:"query"`
}

func (p *Param) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

func LoadFile(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
