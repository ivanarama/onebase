package metadata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const MaxMenuFolderDepth = 8

// Menu is an optional semantic layout. Membership remains defined by contents
// (or global home_page.nav). Arrays define presentation order.
type Menu struct {
	Sections []MenuSection `yaml:"sections" json:"sections"`
}

// Unlike legacy metadata, new menus must not silently ignore unsupported
// nesting or URL fields. This strict boundary applies only to the menu block.
func (m *Menu) UnmarshalYAML(node *yaml.Node) error {
	if err := boundedMenuYAML(node, 0, new(int), recursiveMenuYAML(node)); err != nil {
		return err
	}
	if err := checkMenuNodeShape(node); err != nil {
		return err
	}
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
	Nodes  []MenuNode        `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	Roles  []string          `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// MenuNode is one ordered command or folder. A command has a typed target;
// a folder has nodes. The strict menu decoder rejects ambiguous combinations.
type MenuNode struct {
	ID      string            `yaml:"id" json:"id"`
	Title   string            `yaml:"title,omitempty" json:"title,omitempty"`
	Titles  map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon    string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	Target  string            `yaml:"target,omitempty" json:"target,omitempty"`
	Variant string            `yaml:"variant,omitempty" json:"variant,omitempty"`
	// Action — что делает команда: "" открывает список/форму объекта, "new" —
	// новый документ или элемент справочника (команда 1С «Создать»).
	Action string     `yaml:"action,omitempty" json:"action,omitempty"`
	Nodes  []MenuNode `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	Roles  []string   `yaml:"roles,omitempty" json:"roles,omitempty"`
}

func menuYAMLField(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return node.Content[i+1], true
			}
		}
	}
	return nil, false
}

// Check presence before decoding: nodes:null must not disguise a mixture with
// the old fields. Bound recursive structures before typed allocation as well.
func checkMenuNodeShape(root *yaml.Node) error {
	sections, _ := menuYAMLField(root, "sections")
	if sections == nil || sections.Kind != yaml.SequenceNode {
		return nil // the strict typed decoder reports the ordinary shape error
	}
	count := 0
	var children func(*yaml.Node, int) error
	children = func(list *yaml.Node, depth int) error {
		if list.Kind != yaml.SequenceNode {
			return fmt.Errorf("menu nodes must be an array")
		}
		for _, child := range list.Content {
			count++
			if count > 5500 {
				return fmt.Errorf("menu exceeds node limits")
			}
			if child.Kind != yaml.MappingNode {
				return fmt.Errorf("menu node must be a mapping")
			}
			target, hasTarget := menuYAMLField(child, "target")
			nested, hasNodes := menuYAMLField(child, "nodes")
			if hasTarget {
				if hasNodes || strings.TrimSpace(target.Value) == "" {
					return fmt.Errorf("menu command requires a nonempty target and no nodes")
				}
				continue
			}
			if _, exists := menuYAMLField(child, "variant"); exists {
				return fmt.Errorf("menu variant requires a report command")
			}
			if _, exists := menuYAMLField(child, "action"); exists {
				return fmt.Errorf("menu action requires a command")
			}
			if !hasNodes || depth > MaxMenuFolderDepth {
				return fmt.Errorf("menu folder requires nodes and depth at most %d", MaxMenuFolderDepth)
			}
			if err := children(nested, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, section := range sections.Content {
		if nodes, exists := menuYAMLField(section, "nodes"); exists {
			_, items := menuYAMLField(section, "items")
			_, groups := menuYAMLField(section, "groups")
			if items || groups {
				return fmt.Errorf("menu section cannot mix nodes with items/groups")
			}
			if err := children(nodes, 2); err != nil {
				return err
			}
		}
	}
	return nil
}

type MenuGroup struct {
	ID     string            `yaml:"id" json:"id"`
	Title  string            `yaml:"title" json:"title"`
	Titles map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon   string            `yaml:"icon,omitempty" json:"icon,omitempty"`
	Items  []MenuItem        `yaml:"items,omitempty" json:"items,omitempty"`
}

type MenuItem struct {
	ID      string            `yaml:"id" json:"id"`
	Target  string            `yaml:"target" json:"target"`
	Variant string            `yaml:"variant,omitempty" json:"variant,omitempty"`
	Action  string            `yaml:"action,omitempty" json:"action,omitempty"` // см. MenuNode.Action
	Title   string            `yaml:"title,omitempty" json:"title,omitempty"`
	Titles  map[string]string `yaml:"titles,omitempty" json:"titles,omitempty"`
	Icon    string            `yaml:"icon,omitempty" json:"icon,omitempty"`
}

// Aliases are intentionally excluded from this bounded, identity-based format.
func boundedMenuYAML(node *yaml.Node, depth int, count *int, recursive bool) error {
	*count++
	if depth > 40 || *count > 150000 || recursive && node.Kind == yaml.AliasNode {
		return fmt.Errorf("menu aliases or excessive structure")
	}
	for _, child := range node.Content {
		if err := boundedMenuYAML(child, depth+1, count, recursive); err != nil {
			return err
		}
	}
	return nil
}

func recursiveMenuYAML(node *yaml.Node) bool {
	sections, _ := menuYAMLField(node, "sections")
	if sections == nil {
		return false
	}
	for _, section := range sections.Content {
		if _, ok := menuYAMLField(section, "nodes"); ok {
			return true
		}
		if _, ok := menuYAMLField(section, "roles"); ok {
			return true
		}
	}
	return false
}

// JSON is used by the configurator. Presence checks must agree with YAML,
// including nodes:null mixed with legacy fields, before writing configuration.
func (m *Menu) UnmarshalJSON(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 1 && recursiveMenuYAML(document.Content[0]) {
		return m.UnmarshalYAML(document.Content[0])
	}
	type plain Menu
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*m = Menu(decoded)
	return nil
}

// Preserve an explicit empty nodes list: omitempty would otherwise turn a
// saved recursive folder into invalid YAML, or an empty section into v1.
func emptyMenuNodesYAML(value any) (any, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "nodes"}, &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
	return &node, nil
}
func emptyMenuNodesJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["nodes"] = json.RawMessage("[]")
	return json.Marshal(fields)
}
func (n MenuNode) MarshalYAML() (any, error) {
	type plain MenuNode
	if n.Target == "" && len(n.Nodes) == 0 {
		return emptyMenuNodesYAML(plain(n))
	}
	return plain(n), nil
}
func (n MenuNode) MarshalJSON() ([]byte, error) {
	type plain MenuNode
	if n.Target == "" && len(n.Nodes) == 0 {
		return emptyMenuNodesJSON(plain(n))
	}
	return json.Marshal(plain(n))
}
func (s MenuSection) MarshalYAML() (any, error) {
	type plain MenuSection
	if s.Nodes != nil && len(s.Nodes) == 0 {
		return emptyMenuNodesYAML(plain(s))
	}
	return plain(s), nil
}
func (s MenuSection) MarshalJSON() ([]byte, error) {
	type plain MenuSection
	if s.Nodes != nil && len(s.Nodes) == 0 {
		return emptyMenuNodesJSON(plain(s))
	}
	return json.Marshal(plain(s))
}
