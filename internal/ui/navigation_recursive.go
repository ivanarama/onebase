package ui

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/navigation"
)

type navTreeNode struct {
	ID, DOMID, Icon, Label, URL, CollapseKey string
	Nodes                                    []navTreeNode
}

type NavigationPreviewNode struct {
	ID    string                  `json:"id"`
	Title string                  `json:"title,omitempty"`
	Label string                  `json:"label,omitempty"`
	URL   string                  `json:"url,omitempty"`
	Icon  string                  `json:"icon,omitempty"`
	Nodes []NavigationPreviewNode `json:"nodes,omitempty"`
}

func recursiveNavigationPreview(input []navTreeNode) []NavigationPreviewNode {
	var output []NavigationPreviewNode
	for _, n := range input {
		output = append(output, NavigationPreviewNode{ID: n.ID, Title: n.Label, Label: n.Label, URL: n.URL, Icon: n.Icon, Nodes: recursiveNavigationPreview(n.Nodes)})
	}
	return output
}

func menuRolesAllowed(r *http.Request, clauses [][]string) bool {
	user := auth.UserFromContext(r.Context())
	return navigation.AllowsRoles(clauses, homeNavRoles(r), user == nil || user.IsAdmin)
}

// Both original role clauses and current folder placement constrain visibility.
// An editor may retain its own empty folders, but never a role-forbidden folder.
func (s *Server) projectRecursiveNavigation(r *http.Request, tree navigation.Tree, allowed map[string]bool, acl bool, keep map[string]string) navigation.Tree {
	lang := s.resolveLang(r)
	// Empty personal descendants alone must not expose a forbidden inherited
	// ancestor. Meaningful content propagates independently from editor retention.
	var nodes func([]navigation.Node) ([]navigation.Node, bool)
	nodes = func(input []navigation.Node) ([]navigation.Node, bool) {
		var out []navigation.Node
		meaningful := false
		for _, n := range input {
			if !menuRolesAllowed(r, n.RoleClauses) {
				continue
			}
			childMeaning := false
			if n.Target != "" {
				if allowed != nil && !allowed[n.Target] {
					continue
				}
				if acl && !s.navigationItemVisible(r, n.Object, navigation.ResolveItem(n.Item, lang, "", func(key string) string { return s.tr(lang, key) }, false), true, false) {
					continue
				}
			} else {
				n.Nodes, childMeaning = nodes(n.Nodes)
				retainEmpty := acl && keep != nil && (strings.HasPrefix(n.ID, "usr:") || keep[n.ID] != "")
				if !childMeaning && !retainEmpty {
					continue
				}
			}
			meaningful = meaningful || n.Target != "" || childMeaning || !strings.HasPrefix(n.ID, "usr:")
			out = append(out, n)
		}
		return out, meaningful
	}
	result := navigation.Tree{Version: tree.Version, Context: tree.Context, Sections: []navigation.Section{}}
	for _, section := range tree.Sections {
		if !menuRolesAllowed(r, section.RoleClauses) {
			continue
		}
		var meaningful bool
		section.Nodes, meaningful = nodes(section.Nodes)
		if meaningful || acl && keep != nil && (strings.HasPrefix(section.ID, "usr:") || keep[section.ID] != "") {
			result.Sections = append(result.Sections, section)
		}
	}
	return result
}

func (s *Server) recursiveNavigationGroups(r *http.Request, tree navigation.Tree, sub string) []navGroup {
	tree = s.projectRecursiveNavigation(r, tree, nil, true, nil)
	lang := s.resolveLang(r)
	key := func(id string) string { return "navtree:" + navDOMID(currentUserLogin(r)+"\x00"+tree.Context, id) }
	var nodes func([]navigation.Node) []navTreeNode
	nodes = func(input []navigation.Node) []navTreeNode {
		var out []navTreeNode
		for _, n := range input {
			next := navTreeNode{ID: n.ID, DOMID: navDOMID(tree.Context, n.ID), Icon: n.Icon, Label: navigation.DisplayTitle(n.Title, n.Titles, lang), CollapseKey: key(n.ID)}
			if n.Target != "" {
				resolved := navigation.ResolveItem(n.Item, lang, sub, func(key string) string { return s.tr(lang, key) }, false)
				next.Label, next.URL = resolved.Label, resolved.URL
			} else {
				next.Nodes = nodes(n.Nodes)
			}
			out = append(out, next)
		}
		return out
	}
	var out []navGroup
	for _, section := range tree.Sections {
		out = append(out, navGroup{ID: section.ID, DOMID: navDOMID(tree.Context, section.ID), Icon: section.Icon, Kind: navigation.DisplayTitle(section.Title, section.Titles, lang), Open: true, Nodes: nodes(section.Nodes), Recursive: true, CollapseKey: key(section.ID)})
	}
	return out
}

func allocateRecursiveNavigationContainers(base, effective navigation.Tree, desired *navigation.Tree, layer navigation.Layer) error {
	existing := map[string]string{}
	var collect func([]navigation.Node)
	collect = func(input []navigation.Node) {
		for _, n := range input {
			if n.Target == "" {
				existing[n.ID] = "group"
				collect(n.Nodes)
			}
		}
	}
	for _, tree := range []navigation.Tree{base, effective} {
		for _, section := range tree.Sections {
			existing[section.ID] = "section"
			collect(section.Nodes)
		}
	}
	seen := map[string]bool{}
	allocate := func(id *string, kind string) error {
		if seen[*id] {
			return fmt.Errorf("duplicate container")
		}
		seen[*id] = true
		if old, ok := existing[*id]; ok {
			if old != kind {
				return fmt.Errorf("changed container kind")
			}
			return nil
		}
		if !strings.HasPrefix(*id, "new:") || len(*id) <= 4 || len(*id) > 128 {
			return fmt.Errorf("unexpected container ID")
		}
		var err error
		*id, err = navigation.NewCustomID(layer)
		return err
	}
	var walk func([]navigation.Node, int) error
	walk = func(input []navigation.Node, depth int) error {
		if depth > navigation.MaxDepth+1 {
			return fmt.Errorf("excessive depth")
		}
		for i := range input {
			n := &input[i]
			if n.Target != "" {
				continue
			}
			if err := allocate(&n.ID, "group"); err != nil {
				return err
			}
			if err := walk(n.Nodes, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range desired.Sections {
		section := &desired.Sections[i]
		if err := allocate(&section.ID, "section"); err != nil {
			return err
		}
		if err := walk(section.Nodes, 2); err != nil {
			return err
		}
	}
	return nil
}
