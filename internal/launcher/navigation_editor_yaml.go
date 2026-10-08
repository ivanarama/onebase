package launcher

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// The menu editor changes one mapping value, not the surrounding subsystem or
// dashboard. Reusing menu nodes by ID also retains comments during moves.
func setNavigationMenuYAML(doc *yaml.Node, menu *metadata.Menu) error {
	old, index, err := yamlMapField(doc, "menu")
	if err != nil {
		return err
	}
	inherited, err := yamlMapHasMergedField(doc, "menu")
	if err != nil {
		return err
	}
	if old == nil && inherited {
		return fmt.Errorf("menu унаследовано через YAML merge; используйте редактор YAML")
	}
	// Shared aliases can make a menu-only edit change unrelated fields. Retain
	// the original file and let its author edit that graph explicitly in YAML.
	var check func(*yaml.Node) error
	check = func(n *yaml.Node) error {
		if n == nil {
			return nil
		}
		if n.Kind == yaml.AliasNode || n.Anchor != "" || isYAMLMergeKey(n) {
			return fmt.Errorf("menu использует YAML anchor/alias/merge; используйте редактор YAML")
		}
		for _, child := range n.Content {
			if err := check(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(old); err != nil {
		return err
	}
	var fresh yaml.Node
	if err := fresh.Encode(menu); err != nil {
		return err
	}
	if old == nil {
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "menu"}, &fresh)
		return nil
	}
	byID := map[string]*yaml.Node{}
	var collect func(*yaml.Node, navigationYAMLRole)
	collect = func(n *yaml.Node, role navigationYAMLRole) {
		if n == nil || role == navigationYAMLValue {
			return
		}
		if id := navigationYAMLNodeID(n, role); id != "" {
			byID[id] = n
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				collect(n.Content[i+1], navigationYAMLChildRole(role, n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for _, child := range n.Content {
				collect(child, role)
			}
		}
	}
	collect(old, navigationYAMLMenu)
	doc.Content[index+1] = reconcileNavigationYAML(old, &fresh, byID, navigationYAMLMenu)
	return nil
}

// Only typed positions carry menu identity. In particular, titles.id is an
// Indonesian translation, even when its value equals a section/group/item ID.
type navigationYAMLRole uint8

const (
	navigationYAMLValue navigationYAMLRole = iota
	navigationYAMLMenu
	navigationYAMLSection
	navigationYAMLGroup
	navigationYAMLItem
)

func navigationYAMLChildRole(parent navigationYAMLRole, field string) navigationYAMLRole {
	switch {
	case parent == navigationYAMLMenu && field == "sections":
		return navigationYAMLSection
	case parent == navigationYAMLSection && field == "groups":
		return navigationYAMLGroup
	case (parent == navigationYAMLSection || parent == navigationYAMLGroup) && field == "items":
		return navigationYAMLItem
	default:
		return navigationYAMLValue
	}
}

func navigationYAMLNodeID(n *yaml.Node, role navigationYAMLRole) string {
	if n == nil || n.Kind != yaml.MappingNode || (role != navigationYAMLSection && role != navigationYAMLGroup && role != navigationYAMLItem) {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "id" && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value
		}
	}
	return ""
}

func reconcileNavigationYAML(old, fresh *yaml.Node, byID map[string]*yaml.Node, role navigationYAMLRole) *yaml.Node {
	if id := navigationYAMLNodeID(fresh, role); id != "" {
		old = byID[id]
	}
	if old == nil {
		if fresh.Kind != yaml.MappingNode && fresh.Kind != yaml.SequenceNode {
			return fresh
		}
		copy := *fresh
		copy.Content = nil
		old = &copy
	}
	if old.Kind != fresh.Kind || fresh.Kind == yaml.ScalarNode {
		replaceYAMLNode(old, fresh)
		return old
	}
	switch fresh.Kind {
	case yaml.MappingNode:
		fields := map[string]int{}
		for i := 0; i+1 < len(old.Content); i += 2 {
			fields[old.Content[i].Value] = i
		}
		content := make([]*yaml.Node, 0, len(fresh.Content))
		for i := 0; i+1 < len(fresh.Content); i += 2 {
			key, value := fresh.Content[i], fresh.Content[i+1]
			var previous *yaml.Node
			if j, ok := fields[key.Value]; ok {
				key, previous = old.Content[j], old.Content[j+1]
			}
			content = append(content, key, reconcileNavigationYAML(previous, value, byID, navigationYAMLChildRole(role, key.Value)))
		}
		old.Content = content
	case yaml.SequenceNode:
		content := make([]*yaml.Node, 0, len(fresh.Content))
		for _, value := range fresh.Content {
			content = append(content, reconcileNavigationYAML(nil, value, byID, role))
		}
		old.Content = content
	default:
		replaceYAMLNode(old, fresh)
	}
	return old
}
