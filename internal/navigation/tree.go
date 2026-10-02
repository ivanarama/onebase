package navigation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

const (
	MaxSections = 100
	MaxGroups   = 500
	MaxItems    = 5000
)

// Object is a metadata projection, independent of HTTP, SQL and user access.
type Object struct {
	Target   Target            `json:"target"`
	Title    string            `json:"title"`
	Titles   map[string]string `json:"titles,omitempty"`
	Periodic bool              `json:"periodic,omitempty"`
	External bool              `json:"external,omitempty"`
	Trusted  bool              `json:"trusted,omitempty"`
}

// Label shares target-specific suffixes across metadata preview and rendering.
// Global flat navigation also marks external reports/processors.
func (o Object) Label(lang string, translate func(string) string, flat bool) string {
	label := DisplayTitle(o.Title, o.Titles, lang)
	if o.Target.Kind == "system" {
		label = translate(label)
	}
	suffix := ""
	switch {
	case o.Target.View == "movements":
		suffix = "движения"
	case o.Target.View == "balances":
		suffix = "остатки"
	case o.Periodic:
		suffix = "периодический"
	case flat && o.External && o.Target.Kind == "report":
		suffix = "внешний"
	case flat && o.External && o.Target.Kind == "processor":
		suffix = "внешняя"
	}
	if suffix != "" {
		label += " (" + translate(suffix) + ")"
	}
	return label
}

type ResolvedItem struct {
	ID, Label, URL, Kind, Name, Action string
}

// ResolveItem turns validated metadata into presentation and an access request.
// The caller must filter that request with current RBAC before showing the URL.
func ResolveItem(item Item, lang, context string, translate func(string) string, flat bool) ResolvedItem {
	label := DisplayTitle(item.Title, item.Titles, lang)
	if label == "" {
		label = item.Object.Label(lang, translate, flat)
	}
	t := item.Object.Target
	return ResolvedItem{ID: item.ID, Label: label, URL: t.URL(context), Kind: t.Kind, Name: t.Name, Action: t.Action()}
}

// Scope contains the permitted metadata targets before per-request RBAC.
// Legacy section and object ordering are preserved in Sections.
type Scope struct {
	Sections []Section
	objects  map[string]Object
}

type Tree struct {
	Version  int       `json:"version"`
	Context  string    `json:"context"`
	Sections []Section `json:"sections"`
}

type Section struct {
	ID     string            `json:"id"`
	Title  string            `json:"title"`
	Titles map[string]string `json:"titles,omitempty"`
	Icon   string            `json:"icon,omitempty"`
	Items  []Item            `json:"items,omitempty"`
	Groups []Group           `json:"groups,omitempty"`
}

type Group struct {
	ID     string            `json:"id"`
	Title  string            `json:"title"`
	Titles map[string]string `json:"titles,omitempty"`
	Icon   string            `json:"icon,omitempty"`
	Items  []Item            `json:"items,omitempty"`
}

type Item struct {
	ID     string            `json:"id"`
	Target string            `json:"target"`
	Title  string            `json:"title"`
	Titles map[string]string `json:"titles,omitempty"`
	Icon   string            `json:"icon,omitempty"`
	Object Object            `json:"object"`
}

type Diagnostic struct {
	Code    string
	Node    string
	Message string
	Warning bool
}

var nodeID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Normalize validates and resolves a layout without mutating metadata. An
// absent menu projects the legacy sections; an explicit menu appends unplaced
// allowed targets to the reserved section cfg:other.
func Normalize(context string, menu *metadata.Menu, scope Scope) (Tree, []Diagnostic) {
	tree := Tree{Version: 1, Context: context}
	if menu == nil {
		tree.Sections = cloneSections(scope.Sections)
		return tree, nil
	}
	var diagnostics []Diagnostic
	add := func(code, node, message string, warning bool) {
		diagnostics = append(diagnostics, Diagnostic{code, node, message, warning})
	}
	ids := map[string]bool{}
	placed := map[string]bool{}
	groups, count := 0, 0
	checkID := func(id string) {
		if !nodeID.MatchString(id) {
			add("navigation.id", id, "id должен соответствовать [a-z][a-z0-9-]{0,62}", false)
		}
		if id == "other" {
			add("navigation.reserved-id", id, "id other зарезервирован для неразмещённых объектов", false)
		}
		if ids[id] {
			add("navigation.duplicate-id", id, "повторяющийся id "+id, false)
		}
		ids[id] = true
	}
	items := func(input []metadata.MenuItem) []Item {
		var output []Item
		seen := map[string]bool{}
		for _, item := range input {
			count++
			checkID(item.ID)
			target, err := ParseTarget(item.Target)
			if err != nil {
				add("navigation.target", item.ID, err.Error(), false)
				continue
			}
			object, ok := scope.objects[target.key()]
			if !ok {
				add("navigation.membership", item.ID, fmt.Sprintf("target %q отсутствует в допустимом наборе contents/nav", item.Target), false)
				continue
			}
			if seen[target.key()] {
				add("navigation.duplicate-target", item.ID, "повтор target в одном родителе: "+item.Target, true)
			}
			seen[target.key()], placed[target.key()] = true, true
			output = append(output, Item{ID: "cfg:" + item.ID, Target: object.Target.String(), Title: item.Title,
				Titles: copyTitles(item.Titles), Icon: metadata.NormalizeIconName(item.Icon), Object: cloneObject(object)})
		}
		return output
	}
	for _, section := range menu.Sections {
		checkID(section.ID)
		if strings.TrimSpace(section.Title) == "" {
			add("navigation.title", section.ID, "section требует title", false)
		}
		s := Section{ID: "cfg:" + section.ID, Title: section.Title, Titles: copyTitles(section.Titles), Icon: metadata.NormalizeIconName(section.Icon), Items: items(section.Items)}
		for _, group := range section.Groups {
			groups++
			checkID(group.ID)
			if strings.TrimSpace(group.Title) == "" {
				add("navigation.title", group.ID, "group требует title", false)
			}
			s.Groups = append(s.Groups, Group{ID: "cfg:" + group.ID, Title: group.Title, Titles: copyTitles(group.Titles), Icon: metadata.NormalizeIconName(group.Icon), Items: items(group.Items)})
		}
		tree.Sections = append(tree.Sections, s)
	}
	other := Section{ID: "cfg:other", Title: "Другое", Titles: map[string]string{"en": "Other"}}
	for _, section := range scope.Sections {
		for _, item := range section.Items {
			if !placed[item.Object.Target.key()] {
				other.Items = append(other.Items, cloneItem(item))
				count++
			}
		}
	}
	if len(other.Items) > 0 {
		tree.Sections = append(tree.Sections, other)
	}
	if len(tree.Sections) > MaxSections || groups > MaxGroups || count > MaxItems {
		add("navigation.limit", "menu", "превышен предел: 100 sections, 500 groups, 5000 items", false)
	}
	return tree, diagnostics
}

// CanonicalJSON is versioned UTF-8 JSON. encoding/json sorts map keys while
// preserving array order, so translations never introduce hash nondeterminism.
func (t Tree) CanonicalJSON() ([]byte, error) { return json.Marshal(t) }

func (t Tree) Hash() (string, error) {
	data, err := t.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func targetID(target Target) string {
	sum := sha256.Sum256([]byte(target.key()))
	return "cfg:target-" + hex.EncodeToString(sum[:])
}

func copyTitles(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for k, v := range input {
		output[k] = v
	}
	return output
}

func cloneObject(o Object) Object { o.Titles = copyTitles(o.Titles); return o }
func cloneItem(i Item) Item {
	i.Titles = copyTitles(i.Titles)
	i.Object = cloneObject(i.Object)
	return i
}
func cloneSections(input []Section) []Section {
	var output []Section
	for _, s := range input {
		s.Titles = copyTitles(s.Titles)
		var items []Item
		for _, i := range s.Items {
			items = append(items, cloneItem(i))
		}
		s.Items = items
		output = append(output, s)
	}
	return output
}

// DisplayTitle uses metadata's exact-language/title fallback policy.
func DisplayTitle(title string, titles map[string]string, lang string) string {
	if v := titles[lang]; lang != "" && v != "" {
		return v
	}
	return title
}
