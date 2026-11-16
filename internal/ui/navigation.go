package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"maps"
	"net/http"
	"sort"
	"strings"

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
		base, configured, flat := s.homeNavigationLayerBase(r)
		return s.buildNavigationFromBase(r, base, configured, flat, "")
	}
	if current := s.reg.GetSubsystem(sub); current != nil {
		return s.buildNavForSubsystem(r, current, sub)
	}
	return s.buildFlatNav(r)
}

// Keep the common layout intact until its stored layer has been composed.
// A move may use a destination or predecessor excluded by the user's role.
func (s *Server) homeConfigurationNavigation(r *http.Request) (navigation.Tree, bool, bool) {
	return s.homeConfigurationNavigationForRoles(homeNavRoles(r))
}

// Include every declared role target in the authoritative layer base. A role
// switch must not turn an existing role-only item into an unknown identity.
// This complete tree stays server-side; the current role is projected later.
func (s *Server) homeNavigationLayerBase(r *http.Request) (navigation.Tree, bool, bool) {
	hp := s.reg.HomePage()
	if hp == nil || len(hp.NavByRole) == 0 {
		return s.homeConfigurationNavigation(r)
	}
	roles := make([]string, 0, len(hp.NavByRole))
	for role := range hp.NavByRole {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	base, configured, _ := s.homeConfigurationNavigationForRoles(roles)
	_, byRole := hp.NavForRoles(homeNavRoles(r))
	return base, configured, !byRole && hp.Nav.IsEmpty()
}

func (s *Server) homeConfigurationNavigationForRoles(roles []string) (navigation.Tree, bool, bool) {
	hp := s.reg.HomePage()
	if hp == nil {
		base, configured := s.configurationNavigation(nil, nil, true, "")
		return base, configured, true
	}
	base, configured := s.configurationNavigation(hp.Menu, hp.Nav, true, "")
	contents, byRole := hp.NavForRoles(roles)
	if !byRole {
		return base, configured, hp.Nav.IsEmpty()
	}
	scoped := s.roleNavigation(contents)
	placed := map[string]bool{}
	for _, section := range base.Sections {
		for _, item := range section.Items {
			placed[item.Target] = true
		}
		for _, group := range section.Groups {
			for _, item := range group.Items {
				placed[item.Target] = true
			}
		}
	}
	// Supply targets present only in nav_by_role before composing layers. Do
	// not append after composition: that would resurrect common hidden items.
	for _, section := range scoped.Sections {
		var extra []navigation.Item
		for _, item := range section.Items {
			if !placed[item.Target] {
				extra = append(extra, item)
			}
		}
		if len(extra) == 0 {
			continue
		}
		if configured {
			section = navigation.Section{ID: "cfg:other", Title: "Другое", Titles: map[string]string{"en": "Other"}}
		}
		found := false
		for i := range base.Sections {
			if base.Sections[i].ID == section.ID {
				base.Sections[i].Items = append(base.Sections[i].Items, extra...)
				found = true
				break
			}
		}
		if !found {
			section.Items = extra
			base.Sections = append(base.Sections, section)
		}
	}
	return base, configured, false
}

// navigationHomePage — страница, чей nav_by_role задаёт меню контекста:
// глобальная «Главная» или home_page подсистемы sub.
func (s *Server) navigationHomePage(sub string) *metadata.HomePage {
	if sub == "" {
		return s.reg.HomePage()
	}
	if current := s.reg.GetSubsystem(sub); current != nil {
		return current.HomePage
	}
	return nil
}

// projectRoleNavigation оставляет в дереве только объекты составов
// nav_by_role ролей пользователя (hp — «Главная» или home_page подсистемы).
// В подсистеме состав роли только сужает её меню: объекта, которого нет в
// contents подсистемы, проекция не добавляет. Нужен там, где объект роль
// читает ради ссылок (кладовщику — заказы из ордера), а в меню ему не место.
func (s *Server) projectRoleNavigation(r *http.Request, hp *metadata.HomePage, tree navigation.Tree) navigation.Tree {
	allowed := s.roleNavigationTargets(r, hp)
	if allowed == nil {
		return tree
	}
	items := func(input []navigation.Item) []navigation.Item {
		var output []navigation.Item
		for _, item := range input {
			if allowed[item.Target] {
				output = append(output, item)
			}
		}
		return output
	}
	result := navigation.Tree{Version: tree.Version, Context: tree.Context}
	for _, section := range tree.Sections {
		section.Items = items(section.Items)
		var groups []navigation.Group
		for _, group := range section.Groups {
			group.Items = items(group.Items)
			if len(group.Items) > 0 {
				groups = append(groups, group)
			}
		}
		section.Groups = groups
		if len(section.Items) > 0 || len(groups) > 0 {
			result.Sections = append(result.Sections, section)
		}
	}
	return result
}

// A nil set keeps the common menu. For the global fallback, exclude targets
// supplied only for other roles from the full server-side layer base.
func (s *Server) roleNavigationTargets(r *http.Request, hp *metadata.HomePage) map[string]bool {
	contents, byRole := hp.NavForRoles(homeNavRoles(r))
	var scoped navigation.Tree
	if byRole {
		scoped = s.roleNavigation(contents)
	} else if hp != nil && hp == s.reg.HomePage() && len(hp.NavByRole) > 0 {
		scoped, _ = s.configurationNavigation(hp.Menu, hp.Nav, true, "")
	} else {
		return nil
	}
	allowed := map[string]bool{}
	for _, section := range scoped.Sections {
		for _, item := range section.Items {
			allowed[item.Target] = true
		}
		for _, group := range section.Groups {
			for _, item := range group.Items {
				allowed[item.Target] = true
			}
		}
	}
	return allowed
}

// Resolve role contents to metadata spelling before constructing targets. The
// check command accepts these names without regard to case; tree identities and
// RBAC still use canonical names. Do not mutate the loaded role configuration.
func (s *Server) roleNavigation(contents *metadata.SubsystemContents) navigation.Tree {
	objects := NavigationObjects(s.reg)
	canonical := make(map[string]string, len(objects))
	for _, object := range objects {
		canonical[object.Target.Kind+":"+strings.ToLower(object.Target.Name)] = object.Target.Name
	}
	resolve := func(kind string, names []string) []string {
		var resolved []string
		for _, name := range names {
			if actual, ok := canonical[kind+":"+strings.ToLower(strings.TrimSpace(name))]; ok {
				resolved = append(resolved, actual)
			}
		}
		return resolved
	}
	if contents != nil {
		contents = &metadata.SubsystemContents{
			Catalogs: resolve("catalog", contents.Catalogs), Documents: resolve("document", contents.Documents),
			Registers: resolve("register", contents.Registers), InfoRegs: resolve("inforeg", contents.InfoRegs),
			Reports: resolve("report", contents.Reports), Processors: resolve("processor", contents.Processors),
			Journals: resolve("journal", contents.Journals), Pages: resolve("page", contents.Pages),
		}
	}
	scope := navigation.NewScope(objects, contents, false)
	tree, _ := navigation.Normalize(navContext(""), nil, scope)
	return tree
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

func (s *Server) adminNavigationValidationBase(base navigation.Tree) navigation.Tree {
	hp := s.reg.HomePage()
	if base.Context != navContext("") || hp == nil || len(hp.NavByRole) == 0 {
		return base
	}
	// Administrators save against the common nav, independent of current user
	// roles. The full application base continues to include every role target.
	validationBase, _, _ := s.homeConfigurationNavigationForRoles(nil)
	return validationBase
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
	tree, diagnostics := navigation.ComposeWithAdminBase(base, s.adminNavigationValidationBase(base), raw, nil)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "invalid-layer" {
			slog.Warn("invalid navigation settings skipped", "context", base.Context, "layer", "admin")
		}
	}
	return tree, diagnostics
}

func (s *Server) buildNavigation(r *http.Request, menu *metadata.Menu, contents *metadata.SubsystemContents, global bool, sub string) []navGroup {
	base, configured := s.configurationNavigation(menu, contents, global, sub)
	return s.buildNavigationFromBase(r, base, configured, global && contents.IsEmpty(), sub)
}

func (s *Server) buildNavigationFromBase(r *http.Request, base navigation.Tree, configured, flat bool, sub string) []navGroup {
	tree, _ := s.adminNavigationLayer(r, base)
	tree = s.personalNavigationLayer(r, tree)
	hp := s.navigationHomePage(sub)
	base = s.projectRoleNavigation(r, hp, base)
	tree = s.projectRoleNavigation(r, hp, tree)
	return s.navigationGroups(r, tree, base, configured, flat, sub)
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
		// The only system target is the constants page, which is admin-only.
		return s.isAdmin(r)
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
