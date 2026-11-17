package navigation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Node is an ordered command or folder. Target is empty only for folders.
// RoleClauses are inherited from configuration, never supplied by a delta.
type Node struct {
	Item
	Nodes []Node `json:"nodes,omitempty"`
}

func usesRecursiveMenu(menu *metadata.Menu) bool {
	if menu != nil {
		for _, s := range menu.Sections {
			if s.Nodes != nil || len(s.Roles) > 0 {
				return true
			}
		}
	}
	return false
}

func cloneClauses(input [][]string) [][]string {
	var result [][]string
	for _, clause := range input {
		result = append(result, slices.Clone(clause))
	}
	return result
}

func withRoles(parent [][]string, roles []string) [][]string {
	result := cloneClauses(parent)
	if len(roles) > 0 {
		result = append(result, slices.Clone(roles))
	}
	return result
}

// AllowsRoles evaluates AND between ancestors and OR within each role list.
func AllowsRoles(clauses [][]string, roles []string, admin bool) bool {
	if admin {
		return true
	}
	for _, clause := range clauses {
		match := false
		for _, role := range roles {
			if slices.Contains(clause, role) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	return true
}

func CloneNodes(input []Node) []Node {
	if input == nil {
		return nil
	}
	output := make([]Node, 0, len(input))
	for _, n := range input {
		n.Item = cloneItem(n.Item)
		n.Nodes = CloneNodes(n.Nodes)
		output = append(output, n)
	}
	return output
}

// WalkItems visits every command in either version, preserving presentation order.
func WalkItems(tree Tree, visit func(Item)) {
	var nodes func([]Node)
	nodes = func(input []Node) {
		for _, n := range input {
			if n.Target != "" {
				visit(n.Item)
			} else {
				nodes(n.Nodes)
			}
		}
	}
	for _, s := range tree.Sections {
		for _, i := range s.Items {
			visit(i)
		}
		for _, g := range s.Groups {
			for _, i := range g.Items {
				visit(i)
			}
		}
		nodes(s.Nodes)
	}
}

func normalizeRecursive(context string, menu *metadata.Menu, scope Scope) (Tree, []Diagnostic) {
	tree := Tree{Version: 2, Context: context}
	var diagnostics []Diagnostic
	add := func(code, id, message string, warning bool) {
		diagnostics = append(diagnostics, Diagnostic{Code: code, Node: id, Message: message, Warning: warning})
	}
	ids, placed := map[string]bool{}, map[string]bool{}
	folders, commands := 0, 0
	check := func(id string) {
		if !nodeID.MatchString(id) {
			add("navigation.id", id, "invalid menu ID", false)
		}
		if id == "other" {
			add("navigation.reserved-id", id, "reserved menu ID", false)
		}
		if ids[id] {
			add("navigation.duplicate-id", id, "duplicate menu ID", false)
		}
		ids[id] = true
	}
	var children func([]metadata.MenuNode, int, [][]string) []Node
	children = func(input []metadata.MenuNode, depth int, inherited [][]string) []Node {
		if depth > MaxDepth+1 {
			add("navigation.depth", "menu", "menu exceeds folder depth", false)
			return nil
		}
		var result []Node
		seen := map[string]bool{}
		for _, raw := range input {
			check(raw.ID)
			n := Node{Item: Item{ID: "cfg:" + raw.ID, Title: raw.Title, Titles: copyTitles(raw.Titles), Icon: metadata.NormalizeIconName(raw.Icon), RoleClauses: withRoles(inherited, raw.Roles)}}
			for _, role := range raw.Roles {
				if strings.TrimSpace(role) == "" {
					add("navigation.roles", raw.ID, "empty menu role", false)
				}
			}
			if raw.Target != "" {
				commands++
				if raw.Nodes != nil {
					add("navigation.shape", raw.ID, "command cannot have nodes", false)
				}
				target, err := ParseTarget(raw.Target)
				if err != nil {
					add("navigation.target", raw.ID, err.Error(), false)
					continue
				}
				object, ok := scope.objects[target.key()]
				if !ok {
					add("navigation.membership", raw.ID, fmt.Sprintf("target %q absent from contents/nav", raw.Target), false)
					continue
				}
				if raw.Variant != "" && (target.Kind != "report" || !slices.Contains(object.Variants, raw.Variant)) {
					add("navigation.variant", raw.ID, "variant must name an existing report variant", false)
					continue
				}
				binding := target.key() + "\x00" + raw.Variant
				if seen[binding] {
					add("navigation.duplicate-target", raw.ID, "duplicate target in one parent", true)
				}
				seen[binding], placed[target.key()] = true, true
				n.Target, n.Variant, n.Object = object.Target.String(), raw.Variant, cloneObject(object)
			} else {
				if raw.Variant != "" {
					add("navigation.variant", raw.ID, "variant requires a report command", false)
				}
				folders++
				if raw.Nodes == nil {
					add("navigation.shape", raw.ID, "folder requires nodes", false)
				}
				if depth > MaxDepth {
					add("navigation.depth", raw.ID, "menu exceeds folder depth", false)
					continue
				}
				if strings.TrimSpace(raw.Title) == "" {
					add("navigation.title", raw.ID, "folder requires title", false)
				}
				n.Nodes = children(raw.Nodes, depth+1, n.RoleClauses)
			}
			result = append(result, n)
			if folders > MaxGroups || commands > MaxItems {
				add("navigation.limit", "menu", "menu exceeds node limits", false)
				return result
			}
		}
		return result
	}
	for _, raw := range menu.Sections {
		check(raw.ID)
		if strings.TrimSpace(raw.Title) == "" {
			add("navigation.title", raw.ID, "section requires title", false)
		}
		if raw.Nodes != nil && (raw.Items != nil || raw.Groups != nil) {
			add("navigation.shape", raw.ID, "cannot mix nodes and items/groups", false)
		}
		input := raw.Nodes
		if input == nil {
			input = legacyMenuNodes(raw.Items)
			for _, g := range raw.Groups {
				input = append(input, metadata.MenuNode{ID: g.ID, Title: g.Title, Titles: g.Titles, Icon: g.Icon, Nodes: legacyMenuNodes(g.Items)})
			}
		}
		for _, role := range raw.Roles {
			if strings.TrimSpace(role) == "" {
				add("navigation.roles", raw.ID, "empty menu role", false)
			}
		}
		clauses := withRoles(nil, raw.Roles)
		section := Section{ID: "cfg:" + raw.ID, Title: raw.Title, Titles: copyTitles(raw.Titles), Icon: metadata.NormalizeIconName(raw.Icon), RoleClauses: clauses}
		section.Nodes = children(input, 2, clauses)
		tree.Sections = append(tree.Sections, section)
	}
	other := Section{ID: "cfg:other", Title: "Другое", Titles: map[string]string{"en": "Other"}}
	for _, section := range scope.Sections {
		for _, item := range section.Items {
			if !placed[item.Object.Target.key()] {
				other.Nodes = append(other.Nodes, Node{Item: cloneItem(item)})
				commands++
			}
		}
	}
	if len(other.Nodes) > 0 {
		tree.Sections = append(tree.Sections, other)
	}
	if len(tree.Sections) > MaxSections || folders > MaxGroups || commands > MaxItems {
		add("navigation.limit", "menu", "menu exceeds node limits", false)
	}
	return tree, diagnostics
}

func legacyMenuNodes(items []metadata.MenuItem) []metadata.MenuNode {
	output := make([]metadata.MenuNode, 0, len(items))
	for _, i := range items {
		output = append(output, metadata.MenuNode{ID: i.ID, Target: i.Target, Variant: i.Variant, Title: i.Title, Titles: i.Titles, Icon: i.Icon})
	}
	return output
}

// RecursiveVisibleDiff keeps unexposed original ancestors only on the server.
// This permits a visible child to move without hiding its unseen siblings.
func RecursiveVisibleDiff(full, visible, current, desired Tree, layer Layer) (Delta, error) {
	source, err := readLayout(full)
	if err != nil {
		return Delta{}, err
	}
	shown, err := readLayout(visible)
	if err != nil {
		return Delta{}, err
	}
	now, err := readLayout(current)
	if err != nil {
		return Delta{}, err
	}
	wanted, err := readLayout(desired)
	if err != nil {
		return Delta{}, err
	}
	included, editable := map[string]bool{}, map[string]bool{}
	for id := range shown.nodes {
		editable[id] = true
		included[id] = true
	}
	for id := range now.nodes {
		editable[id] = true
		if n := source.nodes[id]; n != nil && n.kind != itemNode {
			included[id] = true
		}
	}
	for id := range included {
		for n := source.nodes[id]; n != nil && n.parent != ""; n = source.nodes[n.parent] {
			included[n.parent] = true
		}
	}
	// Removing an ancestor recursively would discard a visible moved child.
	// Prune individual inventories and filter sibling lists instead.
	for id := range source.nodes {
		if !included[id] {
			delete(source.nodes, id)
		}
	}
	for key, ids := range source.order {
		var keep []string
		for _, id := range ids {
			if source.nodes[id] != nil {
				keep = append(keep, id)
			}
		}
		source.order[key] = keep
	}
	var extra []string
	var addAncestors func(string)
	addAncestors = func(id string) {
		if id == "" || wanted.nodes[id] != nil {
			return
		}
		n := source.nodes[id]
		if n == nil {
			return
		}
		addAncestors(n.parent)
		copied := *n
		copied.item = cloneItem(n.item)
		wanted.nodes[id] = &copied
		wanted.insert(id, n.parent, "")
		extra = append(extra, id)
	}
	var stableIDs []string
	for id := range source.nodes {
		stableIDs = append(stableIDs, id)
	}
	slices.Sort(stableIDs)
	for _, id := range stableIDs {
		if !editable[id] {
			addAncestors(id)
		}
	}
	delta, err := Diff(source.tree(full.Context), wanted.tree(desired.Context), layer)
	if err != nil {
		return Delta{}, err
	}
	ops := delta.Ops[:0]
	for _, op := range delta.Ops {
		if !slices.Contains(extra, op.Node) {
			ops = append(ops, op)
		}
	}
	delta.Ops = ops
	return delta, nil
}
