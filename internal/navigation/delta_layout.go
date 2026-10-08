package navigation

import (
	"fmt"
	"slices"
	"unicode/utf8"
)

type nodeKind uint8

const (
	sectionNode nodeKind = iota
	groupNode
	itemNode
)

type layoutNode struct {
	kind          nodeKind
	parent        string
	titleExplicit bool
	title         string
	titles        map[string]string
	icon          string
	item          Item
}

type siblingList struct {
	parent string
	kind   nodeKind
}

type layout struct {
	nodes map[string]*layoutNode
	order map[siblingList][]string
}

func readLayout(tree Tree) (*layout, error) {
	if tree.Version != 1 {
		return nil, fmt.Errorf("navigation: invalid tree version")
	}
	l := &layout{nodes: map[string]*layoutNode{}, order: map[siblingList][]string{}}
	add := func(id string, node *layoutNode) error {
		if !validDeltaID(id) || l.nodes[id] != nil || !utf8.ValidString(node.title) || node.icon != "" && !deltaIcon.MatchString(node.icon) {
			return fmt.Errorf("navigation: invalid or duplicate tree node")
		}
		for lang, title := range node.titles {
			if !utf8.ValidString(lang) || !utf8.ValidString(title) {
				return fmt.Errorf("navigation: invalid tree encoding")
			}
		}
		l.nodes[id] = node
		key := siblingList{node.parent, node.kind}
		l.order[key] = append(l.order[key], id)
		return nil
	}
	item := func(i Item, parent string) error {
		return add(i.ID, &layoutNode{kind: itemNode, parent: parent, title: i.Title, titles: copyTitles(i.Titles), icon: i.Icon, item: cloneItem(i)})
	}
	for _, s := range tree.Sections {
		if err := add(s.ID, &layoutNode{kind: sectionNode, titleExplicit: s.TitleExplicit, title: s.Title, titles: copyTitles(s.Titles), icon: s.Icon}); err != nil {
			return nil, err
		}
		for _, i := range s.Items {
			if err := item(i, s.ID); err != nil {
				return nil, err
			}
		}
		for _, g := range s.Groups {
			if err := add(g.ID, &layoutNode{kind: groupNode, parent: s.ID, title: g.Title, titles: copyTitles(g.Titles), icon: g.Icon}); err != nil {
				return nil, err
			}
			for _, i := range g.Items {
				if err := item(i, g.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	return l, l.checkLimits()
}

func (l *layout) checkLimits() error {
	counts := [3]int{}
	for _, n := range l.nodes {
		counts[n.kind]++
	}
	if counts[sectionNode] > MaxSections || counts[groupNode] > MaxGroups || counts[itemNode] > MaxItems {
		return fmt.Errorf("navigation: merged tree exceeds node limits")
	}
	return nil
}

func (l *layout) list(parent string, kind nodeKind) []string {
	return l.order[siblingList{parent, kind}]
}

func (l *layout) detach(id string) {
	n := l.nodes[id]
	key := siblingList{n.parent, n.kind}
	index := slices.Index(l.order[key], id)
	if index >= 0 {
		l.order[key] = slices.Delete(l.order[key], index, index+1)
	}
}

func (l *layout) insert(id, parent, after string) {
	n := l.nodes[id]
	n.parent = parent
	key := siblingList{parent, n.kind}
	index := 0
	if after != "" {
		index = slices.Index(l.order[key], after) + 1
	}
	l.order[key] = slices.Insert(l.order[key], index, id)
}

func (l *layout) remove(id string) {
	n := l.nodes[id]
	if n == nil {
		return
	}
	// Children have at most two levels; copy lists before mutating them.
	for _, kind := range []nodeKind{groupNode, itemNode} {
		for _, child := range slices.Clone(l.list(id, kind)) {
			l.remove(child)
		}
		delete(l.order, siblingList{id, kind})
	}
	l.detach(id)
	delete(l.nodes, id)
}

func (l *layout) tree(context string) Tree {
	result := Tree{Version: 1, Context: context, Sections: []Section{}}
	items := func(parent string) []Item {
		var result []Item
		for _, id := range l.list(parent, itemNode) {
			n := l.nodes[id]
			i := cloneItem(n.item)
			i.ID, i.Title, i.Titles, i.Icon = id, n.title, copyTitles(n.titles), n.icon
			result = append(result, i)
		}
		return result
	}
	for _, id := range l.list("", sectionNode) {
		n := l.nodes[id]
		s := Section{ID: id, TitleExplicit: n.titleExplicit, Title: n.title, Titles: copyTitles(n.titles), Icon: n.icon, Items: items(id)}
		for _, gid := range l.list(id, groupNode) {
			g := l.nodes[gid]
			s.Groups = append(s.Groups, Group{ID: gid, Title: g.title, Titles: copyTitles(g.titles), Icon: g.icon, Items: items(gid)})
		}
		result.Sections = append(result.Sections, s)
	}
	return result
}

func (l *layout) destination(kind nodeKind, parent, after, self string) (bool, error) {
	if self != "" && (parent == self || after == self) {
		return false, fmt.Errorf("navigation: cyclic move")
	}
	if kind == sectionNode {
		if parent != "" {
			return false, fmt.Errorf("navigation: section must be at root")
		}
	} else {
		if parent == "" {
			return false, fmt.Errorf("navigation: non-section at root")
		}
		p := l.nodes[parent]
		if p == nil {
			return false, nil
		}
		if kind == groupNode && p.kind != sectionNode || kind == itemNode && p.kind != sectionNode && p.kind != groupNode {
			return false, fmt.Errorf("navigation: invalid parent or excessive depth")
		}
	}
	if after != "" {
		a := l.nodes[after]
		if a == nil || a.parent != parent || a.kind != kind {
			return false, nil
		}
	}
	return true, nil
}
