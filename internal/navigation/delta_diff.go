package navigation

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Diff computes sparse changes to the authoritative previous tree. Metadata
// objects supplied in desired are never adopted. Existing item IDs and targets
// must agree with base; a custom node can only be a section or a group.
func Diff(base, desired Tree, layer Layer) (Delta, error) {
	prefix, err := layer.prefix()
	if err != nil {
		return Delta{}, err
	}
	if desired.Context != base.Context {
		return Delta{}, fmt.Errorf("navigation: context mismatch")
	}
	work, err := readLayout(base)
	if err != nil {
		return Delta{}, err
	}
	wanted, err := readLayout(desired)
	if err != nil {
		return Delta{}, err
	}
	for id, node := range wanted.nodes {
		old := work.nodes[id]
		if old == nil {
			if node.kind == itemNode || !strings.HasPrefix(id, prefix) || strings.TrimSpace(node.title) == "" || len(node.titles) != 0 {
				return Delta{}, fmt.Errorf("navigation: new node is not a permitted custom container")
			}
			continue
		}
		if node.kind != old.kind || node.kind == itemNode && node.item.Target != old.item.Target {
			return Delta{}, fmt.Errorf("navigation: target or node kind changed")
		}
		if node.title == old.title && !maps.Equal(node.titles, old.titles) {
			return Delta{}, fmt.Errorf("navigation: localized titles are inherited")
		}
	}
	hash, err := base.Hash()
	if err != nil {
		return Delta{}, err
	}
	delta := Delta{Version: 1, BaseHash: hash, Ops: []Operation{}}
	move := func(id, parent, after string) {
		delta.Ops = append(delta.Ops, Operation{Op: "move", Node: id, Parent: parent, After: after})
		work.detach(id)
		work.insert(id, parent, after)
	}
	process := func(parent string, kind nodeKind) {
		target := wanted.list(parent, kind)
		// Reorder the nodes staying in this parent first. Keeping a longest
		// increasing subsequence produces the minimum number of sibling moves;
		// inbound/new nodes can then be placed with one operation each.
		positions := map[string]int{}
		for index, id := range work.list(parent, kind) {
			positions[id] = index
		}
		var fixed []string
		var indices []int
		for _, id := range target {
			if node := work.nodes[id]; node != nil && node.parent == parent {
				fixed = append(fixed, id)
				indices = append(indices, positions[id])
			}
		}
		keep := increasingSubsequence(fixed, indices)
		previous := ""
		for _, id := range fixed {
			if !keep[id] {
				move(id, parent, previous)
			}
			previous = id
		}
		previous = ""
		for _, id := range target {
			n := work.nodes[id]
			if n == nil {
				w := wanted.nodes[id]
				title := w.title
				op := "add_section"
				if kind == groupNode {
					op = "add_group"
				}
				delta.Ops = append(delta.Ops, Operation{Op: op, ID: id, Parent: parent, After: previous, Title: &title})
				work.nodes[id] = &layoutNode{kind: kind, title: title}
				work.insert(id, parent, previous)
			} else if n.parent != parent {
				move(id, parent, previous)
			}
			previous = id
		}
	}
	process("", sectionNode)
	for _, sid := range wanted.list("", sectionNode) {
		process(sid, groupNode)
	}
	for _, sid := range wanted.list("", sectionNode) {
		process(sid, itemNode)
		for _, gid := range wanted.list(sid, groupNode) {
			process(gid, itemNode)
		}
	}
	// Stable ID order makes identical requests byte-for-byte deterministic.
	ids := slices.Collect(maps.Keys(wanted.nodes))
	sort.Strings(ids)
	for _, id := range ids {
		w, old := wanted.nodes[id], work.nodes[id]
		if w.title != old.title {
			title := w.title
			delta.Ops = append(delta.Ops, Operation{Op: "rename", Node: id, Title: &title})
		}
		if w.icon != old.icon {
			icon := w.icon
			delta.Ops = append(delta.Ops, Operation{Op: "set_icon", Node: id, Icon: &icon})
		}
	}
	ids = slices.Collect(maps.Keys(work.nodes))
	sort.Strings(ids)
	for _, id := range ids {
		if wanted.nodes[id] != nil {
			continue
		}
		n := work.nodes[id]
		// Hiding a parent already hides its absent descendants. Children which
		// remain wanted were moved out above, so no redundant hides are stored.
		if n.parent != "" && wanted.nodes[n.parent] == nil {
			continue
		}
		op := "hide"
		if strings.HasPrefix(id, prefix) {
			op = "remove_custom"
		}
		delta.Ops = append(delta.Ops, Operation{Op: op, Node: id})
	}
	if err := delta.Validate(layer); err != nil {
		return Delta{}, err
	}
	if _, _, err := ApplyDelta(base, delta, layer); err != nil {
		return Delta{}, err
	}
	return delta, nil
}

func increasingSubsequence(ids []string, positions []int) map[string]bool {
	keep := map[string]bool{}
	var tails, ends []int
	previous := make([]int, len(positions))
	for i, position := range positions {
		index := sort.SearchInts(tails, position)
		previous[i] = -1
		if index > 0 {
			previous[i] = ends[index-1]
		}
		if index == len(tails) {
			tails = append(tails, position)
			ends = append(ends, i)
		} else {
			tails[index], ends[index] = position, i
		}
	}
	if len(ends) > 0 {
		for i := ends[len(ends)-1]; i >= 0; i = previous[i] {
			keep[ids[i]] = true
		}
	}
	return keep
}
