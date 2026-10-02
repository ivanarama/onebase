package navigation

import (
	"fmt"
	"strings"
)

// ApplyDelta returns a new tree. Unknown references are stale rules; structural
// violations invalidate the entire layer. It never broadens the previous layer's
// targets: only existing items can be moved, renamed or made visible.
func ApplyDelta(base Tree, delta Delta, layer Layer) (Tree, []Diagnostic, error) {
	if err := delta.Validate(layer); err != nil {
		return Tree{}, nil, err
	}
	work, err := readLayout(base)
	if err != nil {
		return Tree{}, nil, err
	}
	hash, err := base.Hash()
	if err != nil {
		return Tree{}, nil, err
	}
	var diagnostics []Diagnostic
	if hash != delta.BaseHash {
		diagnostics = append(diagnostics, Diagnostic{Code: "base-changed", Message: "navigation base changed", Warning: true})
	}
	hidden := map[string]bool{}
	stale := func(id string) {
		diagnostics = append(diagnostics, Diagnostic{Code: "stale", Node: id, Message: "navigation reference no longer available", Warning: true})
	}
	for _, op := range delta.Ops {
		if op.Op == "add_section" || op.Op == "add_group" {
			if work.nodes[op.ID] != nil {
				return Tree{}, nil, fmt.Errorf("navigation: duplicate custom ID")
			}
			kind := sectionNode
			if op.Op == "add_group" {
				kind = groupNode
			}
			valid, err := work.destination(kind, op.Parent, op.After, op.ID)
			if err != nil {
				return Tree{}, nil, err
			}
			if !valid {
				stale(op.ID)
				continue
			}
			work.nodes[op.ID] = &layoutNode{kind: kind, title: *op.Title}
			work.insert(op.ID, op.Parent, op.After)
			continue
		}
		n := work.nodes[op.Node]
		if n == nil {
			stale(op.Node)
			continue
		}
		switch op.Op {
		case "rename":
			if n.kind != itemNode && strings.TrimSpace(*op.Title) == "" {
				return Tree{}, nil, fmt.Errorf("navigation: container title is empty")
			}
			n.title, n.titles = *op.Title, nil
		case "set_icon":
			n.icon = *op.Icon
		case "move":
			valid, err := work.destination(n.kind, op.Parent, op.After, op.Node)
			if err != nil {
				return Tree{}, nil, err
			}
			if !valid {
				stale(op.Node)
				continue
			}
			work.detach(op.Node)
			work.insert(op.Node, op.Parent, op.After)
		case "hide":
			hidden[op.Node] = true
		case "show":
			delete(hidden, op.Node)
		case "remove_custom":
			work.remove(op.Node)
		}
	}
	// Visibility is resolved after structural operations: moving a child out of a
	// hidden folder is explicit, while showing its ID cannot resurrect a hidden
	// ancestor. Map order cannot affect the resulting sibling lists.
	for id := range hidden {
		work.remove(id)
	}
	if err := work.checkLimits(); err != nil {
		return Tree{}, nil, err
	}
	return work.tree(base.Context), diagnostics, nil
}

// Compose applies independent stored layers. A corrupt layer is skipped in its
// entirety; the next layer still runs against the safe resulting previous tree.
// Diagnostics carry codes and stable IDs, never the stored JSON or its titles.
// A nil slice denotes an absent key; a present empty JSON value is corrupt.
func Compose(base Tree, adminRaw, userRaw []byte) (Tree, []Diagnostic) {
	result := base
	var diagnostics []Diagnostic
	for _, input := range []struct {
		layer Layer
		raw   []byte
	}{{AdminLayer, adminRaw}, {UserLayer, userRaw}} {
		if input.raw == nil {
			continue
		}
		delta, err := DecodeDelta(input.raw, input.layer)
		var next Tree
		var current []Diagnostic
		if err == nil {
			next, current, err = ApplyDelta(result, delta, input.layer)
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "invalid-layer", Node: string(input.layer), Message: "stored navigation layer is invalid", Warning: true})
			continue
		}
		result = next
		diagnostics = append(diagnostics, current...)
	}
	return result, diagnostics
}
