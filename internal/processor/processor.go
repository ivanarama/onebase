package processor

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Param struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Label string `yaml:"label"`
}

type Processor struct {
	Name   string  `yaml:"name"`
	Title  string  `yaml:"title"`
	Params []Param `yaml:"params"`
}

func (p Param) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

func LoadFile(path string) (*Processor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var proc Processor
	if err := yaml.Unmarshal(data, &proc); err != nil {
		return nil, err
	}
	return &proc, nil
}

func LoadDir(dir string) ([]*Processor, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var procs []*Processor
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		proc, err := LoadFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		procs = append(procs, proc)
	}
	return procs, nil
}
