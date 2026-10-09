package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"maps"
	"net/http"
	"sort"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type navItem struct {
	ID, DOMID, Icon, Label, URL string
}

type navFolder struct {
	ID, DOMID, Icon, Kind string
	Items                 []navItem
}

type navGroup struct {
	ID, DOMID, Icon, Kind string
	LegacyTitle           string // one-time migration of existing collapse preferences
	Items                 []navItem
	Groups                []navFolder
	Open                  bool
}

func sortNavItems(items []navItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Label != items[j].Label {
			return items[i].Label < items[j].Label
		}
		return items[i].URL < items[j].URL
	})
}

// A route context is distinct from tree identity: a subsystem named "global"
// still needs its query parameter and must not share the home menu's IDs.
func navContext(sub string) string {
	if sub == "" {
		return "global"
	}
	return "subsystem:" + sub
}

func navDOMID(context, id string) string {
	sum := sha256.Sum256([]byte(context + "\x00" + id))
	return "nav-" + hex.EncodeToString(sum[:])
}

func (s *Server) buildNav(r *http.Request, sub string) []navGroup {
	if sub == "" {
		if hp := s.reg.HomePage(); hp != nil {
			contents := hp.Nav
			// nav_by_role: у пользователя роль из блока — меню его ролей вместо
			// общего nav; пустой состав — пустое меню, а не «все объекты».
			if byRole, ok := hp.NavForRoles(homeNavRoles(r)); ok {
				if byRole.IsEmpty() {
					return nil
				}
				contents = byRole
			}
			return s.buildNavigation(r, hp.Menu, contents, true, "")
		}
		return s.buildFlatNav(r)
	}
	if current := s.reg.GetSubsystem(sub); current != nil {
		return s.buildNavForSubsystem(r, current, sub)
	}
	return s.buildFlatNav(r)
}

// homeNavRoles — роли, по которым выбирается меню «Главной». Администратор
// (и открытый деплой без пользователя) видит общий nav.
func homeNavRoles(r *http.Request) []string {
	u := auth.UserFromContext(r.Context())
	if u == nil || u.IsAdmin {
		return nil
	}
	names := make([]string, 0, len(u.Roles))
	for _, role := range u.Roles {
		if role != nil {
			names = append(names, role.Name)
		}
	}
	return names
}

func (s *Server) buildNavForSubsystem(r *http.Request, sub *metadata.Subsystem, name string) []navGroup {
	return s.buildNavigation(r, sub.Menu, &sub.Contents, false, name)
}

func (s *Server) buildFlatNav(r *http.Request) []navGroup {
	return s.buildNavigation(r, nil, nil, true, "")
}

// NavigationObjects keeps raw metadata titles separate from translations;
// choosing the Russian display name here would corrupt other-language fallback.
// The configurator uses this same projection for its permitted object palette.
func NavigationObjects(reg *runtime.Registry) []navigation.Object {
	var objects []navigation.Object
	add := func(kind, name, title string, titles map[string]string) {
		objects = append(objects, navigation.Object{Target: navigation.Target{Kind: kind, Name: name}, Title: title, Titles: titles})
	}
	for _, e := range reg.Entities() {
		add(string(e.Kind), e.Name, e.DisplayName(""), e.Titles)
	}
	for _, o := range reg.Registers() {
		for _, view := range []string{"movements", "balances"} {
			objects = append(objects, navigation.Object{Target: navigation.Target{Kind: "register", Name: o.Name, View: view}, Title: o.DisplayName(""), Titles: o.Titles})
		}
	}
	for _, o := range reg.InfoRegisters() {
		add("inforeg", o.Name, o.DisplayName(""), o.Titles)
		objects[len(objects)-1].Periodic = o.Periodic
	}
	for _, o := range reg.Reports() {
		add("report", o.Name, o.DisplayName(""), o.Titles)
		objects[len(objects)-1].External = o.External
	}
	for _, o := range reg.Processors() {
		add("processor", o.Name, o.DisplayName(""), o.Titles)
		objects[len(objects)-1].External, objects[len(objects)-1].Trusted = o.External, o.Trusted
	}
	for _, o := range reg.Journals() {
		add("journal", o.Name, o.DisplayName(""), o.Titles)
	}
	for _, o := range reg.Pages() {
		add("page", o.Name, o.DisplayName(""), o.Titles)
	}
	if len(reg.Constants()) > 0 {
		add("system", "constants", "Константы", nil)
	}
	return objects
}

func (s *Server) configurationNavigation(menu *metadata.Menu, contents *metadata.SubsystemContents, global bool, sub string) (navigation.Tree, bool) {
	context := navContext(sub)
	scope := navigation.NewScope(NavigationObjects(s.reg), contents, global)
	tree, diagnostics := navigation.Normalize(context, menu, scope)
	for _, diagnostic := range diagnostics {
		if !diagnostic.Warning {
			// Never expose a partially validated tree. The check command diagnoses
			// invalid layouts; runtime retains the safe membership-based fallback.
			menu = nil
			tree, _ = navigation.Normalize(context, nil, scope)
			break
		}
	}
	return tree, menu != nil
}

// The configurator preview has no store; it remains a configuration-only view.
// Database failures and corrupt stored layers retain the safe previous layout.
func (s *Server) adminNavigationLayer(r *http.Request, base navigation.Tree) (navigation.Tree, []navigation.Diagnostic) {
	if s.store == nil {
		return base, nil
	}
	setting, err := s.store.GetNavigationSettings(r.Context(), storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: base.Context})
	if err != nil {
		slog.Warn("navigation settings unavailable", "context", base.Context)
		return base, []navigation.Diagnostic{{Code: "unavailable-layer", Node: "admin", Warning: true}}
	}
	var raw []byte
	if setting.Exists {
		raw = []byte(setting.Raw)
	}
	tree, diagnostics := navigation.Compose(base, raw, nil)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "invalid-layer" {
			slog.Warn("invalid navigation settings skipped", "context", base.Context, "layer", "admin")
		}
	}
	return tree, diagnostics
}

func (s *Server) buildNavigation(r *http.Request, menu *metadata.Menu, contents *metadata.SubsystemContents, global bool, sub string) []navGroup {
	base, configured := s.configurationNavigation(menu, contents, global, sub)
	tree, _ := s.adminNavigationLayer(r, base)
	tree = s.personalNavigationLayer(r, tree)
	return s.navigationGroups(r, tree, base, configured, global && contents.IsEmpty(), sub)
}

func (s *Server) personalNavigationLayer(r *http.Request, base navigation.Tree) navigation.Tree {
	login := currentUserLogin(r)
	if s.store == nil || login == "" {
		return base
	}
	setting, err := s.store.GetNavigationSettings(r.Context(), storage.NavigationSettingsScope{Layer: navigation.UserLayer, Context: base.Context, Login: login})
	if err != nil {
		slog.Warn("personal navigation unavailable", "context", base.Context)
		return base
	}
	var raw []byte
	if setting.Exists {
		raw = []byte(setting.Raw)
	}
	tree, diagnostics := navigation.Compose(base, nil, raw)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "invalid-layer" {
			slog.Warn("invalid navigation settings skipped", "context", base.Context, "layer", "user")
		}
	}
	return tree
}

func (s *Server) navigationGroups(r *http.Request, tree, base navigation.Tree, configured, flat bool, sub string) []navGroup {
	context := base.Context
	baseHash, _ := base.Hash()
	treeHash, _ := tree.Hash()
	semantic := configured || baseHash != treeHash
	originals := make(map[string]navigation.Section, len(base.Sections))
	for _, section := range base.Sections {
		originals[section.ID] = section
	}
	lang := s.resolveLang(r)
	translate := func(key string) string { return s.tr(lang, key) }
	items := func(input []navigation.Item) []navItem {
		var output []navItem
		for _, item := range input {
			resolved := navigation.ResolveItem(item, lang, sub, translate, flat)
			if !semantic {
				resolved.URL = item.Object.Target.LegacyURL(sub)
			}
			if !s.navigationItemVisible(r, item.Object, resolved, semantic, flat) {
				continue
			}
			output = append(output, navItem{ID: item.ID, DOMID: navDOMID(context, item.ID), Icon: item.Icon, Label: resolved.Label, URL: resolved.URL})
		}
		if !semantic && flat {
			sortNavItems(output)
		}
		return output
	}
	var output []navGroup
	for _, section := range tree.Sections {
		group := navGroup{ID: section.ID, DOMID: navDOMID(context, section.ID), Icon: section.Icon,
			Kind: navigation.DisplayTitle(section.Title, section.Titles, lang), Items: items(section.Items), Open: semantic}
		original, existed := originals[section.ID]
		if !configured && existed && !section.TitleExplicit && section.Title == original.Title && maps.Equal(section.Titles, original.Titles) {
			group.Kind = translate(section.Title)
			if section.ID == "cfg:legacy-system" {
				group.Kind = section.Title // exact legacy heading, including other UI languages
			}
			group.LegacyTitle = group.Kind
			if !semantic {
				group.Open = section.ID == "cfg:legacy-catalog" || section.ID == "cfg:legacy-document" || section.ID == "cfg:legacy-page"
			}
		}
		for _, folder := range section.Groups {
			visible := items(folder.Items)
			if len(visible) != 0 {
				group.Groups = append(group.Groups, navFolder{ID: folder.ID, DOMID: navDOMID(context, folder.ID), Icon: folder.Icon,
					Kind: navigation.DisplayTitle(folder.Title, folder.Titles, lang), Items: visible})
			}
		}
		if len(group.Items) != 0 || len(group.Groups) != 0 {
			output = append(output, group)
		}
	}
	return output
}

// NavigationPreviewItem is an already resolved, permission-filtered menu entry.
type NavigationPreviewItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
	Icon  string `json:"icon,omitempty"`
}

type NavigationPreviewGroup struct {
	ID    string                  `json:"id"`
	Title string                  `json:"title"`
	Icon  string                  `json:"icon,omitempty"`
	Items []NavigationPreviewItem `json:"items"`
}

type NavigationPreviewSection struct {
	ID     string                   `json:"id"`
	Title  string                   `json:"title"`
	Icon   string                   `json:"icon,omitempty"`
	Items  []NavigationPreviewItem  `json:"items"`
	Groups []NavigationPreviewGroup `json:"groups"`
}

// AdminNavigationPreview renders a configurator draft through the production
// resolver and RBAC path, without opening a database or starting an application.
// Its caller must enforce configurator administrator authentication; this is
// deliberately an administrator preview, not a user's effective navigation.
func AdminNavigationPreview(reg *runtime.Registry, menu *metadata.Menu, contents *metadata.SubsystemContents, global bool, sub, lang string, bundle *i18n.Bundle) []NavigationPreviewSection {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{IsAdmin: true, Lang: lang}))
	s := &Server{reg: reg, cfg: Config{Bundle: bundle, Lang: lang}}
	return navigationPreview(s.buildNavigation(r, menu, contents, global, sub))
}

func navigationPreview(groups []navGroup) []NavigationPreviewSection {
	items := func(input []navItem) []NavigationPreviewItem {
		out := make([]NavigationPreviewItem, 0, len(input))
		for _, item := range input {
			out = append(out, NavigationPreviewItem{ID: item.ID, Label: item.Label, URL: item.URL, Icon: item.Icon})
		}
		return out
	}
	out := []NavigationPreviewSection{}
	for _, section := range groups {
		next := NavigationPreviewSection{ID: section.ID, Title: section.Kind, Icon: section.Icon, Items: items(section.Items), Groups: []NavigationPreviewGroup{}}
		for _, group := range section.Groups {
			next.Groups = append(next.Groups, NavigationPreviewGroup{ID: group.ID, Title: group.Kind, Icon: group.Icon, Items: items(group.Items)})
		}
		out = append(out, next)
	}
	return out
}

func (s *Server) navigationItemVisible(r *http.Request, object navigation.Object, resolved navigation.ResolvedItem, semantic, flat bool) bool {
	switch resolved.Kind {
	case "system":
		return true // membership already restricts this to the existing constants route
	case "page":
		page := s.reg.GetPage(resolved.Name)
		return page != nil && s.canSeePage(r, page)
	case "journal":
		// Preserve legacy journal links. Semantic menus follow the same document
		// access rule as subsystem visibility; journal routes still filter rows.
		user := auth.UserFromContext(r.Context())
		if !semantic || user == nil || user.IsAdmin {
			return true
		}
		if journal := s.reg.GetJournal(resolved.Name); journal != nil {
			for _, name := range journal.Documents {
				if entity := s.reg.GetEntity(name); entity != nil && s.can(r, string(entity.Kind), entity.Name, "read") {
					return true
				}
			}
		}
		return false
	case "processor":
		// Existing flat navigation hides untrusted externals; scoped legacy
		// navigation retains its old links. The execution handler is unchanged.
		if (semantic || flat) && object.External && !object.Trusted && !s.isAdmin(r) {
			return false
		}
	}
	return s.can(r, resolved.Kind, resolved.Name, resolved.Action)
}
