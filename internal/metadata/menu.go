package metadata

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Menu is an optional semantic layout. Membership remains defined by contents
// (or global home_page.nav). Arrays define presentation order.
type Menu struct {
	Sections []MenuSection `yaml:"sections" json:"sections"`
}

// Unlike legacy metadata, new menus must not silently ignore unsupported
// nesting or URL fields. This strict boundary applies only to the menu block.
func (m *Menu) UnmarshalYAML(node *yaml.Node) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	type plain Menu
	var decoded plain
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*m = Menu(decoded)
	return nil
}

type MenuSection struct {
	ID     string            `yaml:"id" json:"id"`
	Title  string            `yaml:"title" json:"title"`
	Titles map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon   string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	Items  []MenuItem        `yaml:"items,omitempty" json:"items,omitempty"`
	Groups []MenuGroup       `yaml:"groups,omitempty" json:"groups,omitempty"`
}

type MenuGroup struct {
	ID     string            `yaml:"id" json:"id"`
	Title  string            `yaml:"title" json:"title"`
	Titles map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon   string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	Items  []MenuItem        `yaml:"items,omitempty" json:"items,omitempty"`
}

type MenuItem struct {
	ID     string            `yaml:"id" json:"id"`
	Target string            `yaml:"target" json:"target"`
	Title  string            `yaml:"title,omitempty" json:"title,omitempty"`
	Titles map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon   string            `yaml:"icon,omitempty" json:"icon,omitempty"`
}
