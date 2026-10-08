package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
)

// The authenticated login and parsed, permission-checked context alone choose
// the personal key. No login, layer, SQL key or raw delta is accepted from UI.
func (s *Server) personalNavigationAllowed(w http.ResponseWriter, r *http.Request, sub string) bool {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.Login == "" {
		s.renderForbidden(w, r)
		return false
	}
	if s.store == nil {
		s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
		return false
	}
	if sub != "" {
		current := s.reg.GetSubsystem(sub)
		if current == nil {
			s.navigationEditorError(w, r, http.StatusBadRequest, "Не удалось загрузить настройку меню")
			return false
		}
		if !user.IsAdmin && !s.subsystemVisible(r, user, current) {
			s.renderForbidden(w, r)
			return false
		}
	}
	return true
}

func personalNavigationScope(r *http.Request, context string) storage.NavigationSettingsScope {
	return storage.NavigationSettingsScope{Layer: navigation.UserLayer, Login: currentUserLogin(r), Context: context}
}

// The full trees stay on the server. Base/Desired are the permitted editing
// surface, while runtime/preview retain the original full configuration base.
func (s *Server) personalNavigationState(r *http.Request, sub string) (navigationEditorState, error) {
	state, err := s.navigationEditorState(r, sub)
	if err != nil {
		return state, err
	}
	state.Personal = true
	state.configuration, state.layerBase = state.Base, state.Desired
	adminSetting := state.Setting
	state.Setting, err = s.store.GetNavigationSettings(r.Context(), personalNavigationScope(r, state.Base.Context))
	if err != nil {
		return state, err
	}
	var raw []byte
	if state.Setting.Exists {
		raw = []byte(state.Setting.Raw)
	}
	var diagnostics []navigation.Diagnostic
	state.effective, diagnostics = navigation.Compose(state.layerBase, nil, raw)
	state.Diagnostics = append(state.Diagnostics, diagnostics...)
	// IDs in stale rules can name now-forbidden metadata. Only generic codes
	// and counts are shown, never raw operations or diagnostic node identities.
	for i := range state.Diagnostics {
		if state.Diagnostics[i].Code != "invalid-layer" {
			state.Diagnostics[i].Node = ""
		}
		state.Diagnostics[i].Message = ""
	}
	state.Base = s.projectPersonalNavigation(r, state.layerBase, state, nil)
	baseNodes := navigationEditorNodes(state.Base)
	state.Desired = s.projectPersonalNavigation(r, state.effective, state, baseNodes)
	fullHash, _ := state.layerBase.Hash()
	visibleHash, _ := state.Base.Hash()
	revision := sha256.Sum256([]byte(fullHash + ":" + visibleHash))
	state.BaseRevision = hex.EncodeToString(revision[:])
	state.Renamed = []string{}
	state.Origins = map[string]string{}
	desiredNodes := navigationEditorNodes(state.Desired)
	inheritedNodes := navigationEditorNodes(state.layerBase)
	visibleNodes := navigationEditorNodes(state.Base)
	for id, title := range desiredNodes {
		if _, inherited := inheritedNodes[id]; inherited {
			visibleNodes[id] = title
		}
	}
	for id := range visibleNodes {
		state.Origins[id] = "configuration"
	}
	mark := func(setting storage.NavigationSettings, layer navigation.Layer, base navigation.Tree, source string) navigation.Delta {
		if !setting.Exists {
			return navigation.Delta{}
		}
		delta, err := navigation.DecodeDelta([]byte(setting.Raw), layer)
		if err != nil {
			return navigation.Delta{}
		}
		if _, _, err := navigation.ApplyDelta(base, delta, layer); err != nil {
			return navigation.Delta{}
		}
		for _, op := range delta.Ops {
			id := op.Node
			if op.ID != "" {
				id = op.ID
			}
			if _, visible := visibleNodes[id]; visible {
				state.Origins[id] = source
			}
		}
		return delta
	}
	mark(adminSetting, navigation.AdminLayer, state.configuration, "common")
	state.previous = mark(state.Setting, navigation.UserLayer, state.layerBase, "personal")
	for id := range desiredNodes {
		if strings.HasPrefix(id, "usr:") {
			state.Origins[id] = "personal"
		}
	}
	for _, op := range state.previous.Ops {
		if op.Op == "rename" {
			if _, visible := desiredNodes[op.Node]; visible {
				if _, inherited := inheritedNodes[op.Node]; inherited {
					state.Renamed = append(state.Renamed, op.Node)
				}
			}
		}
	}
	return state, nil
}

// Keep empty personal folders, and inherited folders emptied by personal moves.
// Prune inherited ancestors containing only forbidden objects from the palette.
func (s *Server) projectPersonalNavigation(r *http.Request, tree navigation.Tree, state navigationEditorState, keep map[string]string) navigation.Tree {
	baseHash, _ := state.configuration.Hash()
	treeHash, _ := tree.Hash()
	semantic := state.Configured || baseHash != treeHash
	lang := s.resolveLang(r)
	empty := map[string]bool{}
	for index, source := range []navigation.Tree{state.configuration, state.layerBase} {
		for _, section := range source.Sections {
			if len(section.Items) == 0 && len(section.Groups) == 0 && (index == 0 || strings.HasPrefix(section.ID, "adm:")) {
				empty[section.ID] = true
			}
			for _, group := range section.Groups {
				if len(group.Items) == 0 && (index == 0 || strings.HasPrefix(group.ID, "adm:")) {
					empty[group.ID] = true
				}
			}
		}
	}
	items := func(input []navigation.Item) []navigation.Item {
		var result []navigation.Item
		for _, item := range input {
			resolved := navigation.ResolveItem(item, lang, "", func(key string) string { return s.tr(lang, key) }, state.Flat)
			if s.navigationItemVisible(r, item.Object, resolved, semantic, state.Flat) {
				result = append(result, item)
			}
		}
		return result
	}
	result := navigation.Tree{Version: tree.Version, Context: tree.Context, Sections: []navigation.Section{}}
	for _, section := range tree.Sections {
		section.Items = items(section.Items)
		keepSection := len(section.Items) > 0 || strings.HasPrefix(section.ID, "usr:") || keep[section.ID] != "" || empty[section.ID]
		var groups []navigation.Group
		for _, group := range section.Groups {
			group.Items = items(group.Items)
			if len(group.Items) > 0 || strings.HasPrefix(group.ID, "usr:") || keep[group.ID] != "" || empty[group.ID] {
				groups = append(groups, group)
				// An empty personal child must not expose a closed inherited
				// parent. Its full intent remains server-side for regrant.
				if len(group.Items) > 0 || !strings.HasPrefix(group.ID, "usr:") {
					keepSection = true
				}
			}
		}
		section.Groups = groups
		if keepSection {
			result.Sections = append(result.Sections, section)
		}
	}
	return result
}

// ID->editable title is also an inventory for rename intent. Objects and
// targets remain authoritative in navigation.Diff, never copied from clients.
func navigationEditorNodes(tree navigation.Tree) map[string]string {
	result := map[string]string{}
	for _, section := range tree.Sections {
		result[section.ID] = section.Title
		for _, item := range section.Items {
			result[item.ID] = item.Title
		}
		for _, group := range section.Groups {
			result[group.ID] = group.Title
			for _, item := range group.Items {
				result[item.ID] = item.Title
			}
		}
	}
	return result
}

type navigationEditorLocation struct {
	parent string
	kind   string
}

func navigationEditorLocations(tree navigation.Tree) (map[string]navigationEditorLocation, map[navigationEditorLocation][]string) {
	nodes := map[string]navigationEditorLocation{}
	siblings := map[navigationEditorLocation][]string{}
	add := func(id, parent, kind string) {
		location := navigationEditorLocation{parent, kind}
		nodes[id] = location
		siblings[location] = append(siblings[location], id)
	}
	for _, section := range tree.Sections {
		add(section.ID, "", "section")
		for _, item := range section.Items {
			add(item.ID, section.ID, "item")
		}
		for _, group := range section.Groups {
			add(group.ID, section.ID, "group")
			for _, item := range group.Items {
				add(item.ID, group.ID, "item")
			}
		}
	}
	return nodes, siblings
}

// Track structural dependencies without applying visibility early: ApplyDelta
// defers hide until all moves, so a private child can still leave a hidden parent.
func navigationEditorTrackLocation(nodes map[string]navigationEditorLocation, op navigation.Operation) {
	id, location := op.Node, nodes[op.Node]
	switch op.Op {
	case "add_section", "add_group":
		id, location.kind = op.ID, strings.TrimPrefix(op.Op, "add_")
	case "move":
		if location.kind == "" {
			return
		}
	case "remove_custom":
		removed := map[string]bool{op.Node: true}
		for changed := true; changed; {
			changed = false
			for id, current := range nodes {
				if removed[id] || removed[current.parent] {
					removed[id] = true
					delete(nodes, id)
					changed = true
				}
			}
		}
		return
	default:
		return
	}
	parent, present := nodes[op.Parent]
	validParent := location.kind == "section" && op.Parent == "" || present && (location.kind == "group" && parent.kind == "section" || location.kind == "item" && (parent.kind == "section" || parent.kind == "group"))
	location.parent = op.Parent
	if validParent && (op.After == "" || op.After != id && nodes[op.After] == location) {
		nodes[id] = location
	}
}

// Keep a closed node's destination even when its old visible predecessor moved
// away. The nearest surviving old sibling, or the front, is a valid fallback.
func personalNavigationAfter(op navigation.Operation, nodes map[string]navigationEditorLocation, previous map[navigationEditorLocation][]string) navigation.Operation {
	if op.Op != "move" || op.After == "" {
		return op
	}
	location, present := nodes[op.Node]
	if !present {
		return op
	}
	location.parent = op.Parent
	if nodes[op.After] == location {
		return op
	}
	op.After = ""
	siblings := previous[location]
	for i, id := range siblings {
		if id != op.Node {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if nodes[siblings[j]] == location {
				op.After = siblings[j]
				break
			}
		}
		break
	}
	return op
}

// A visible personal placement can keep an inherited container whose original
// contents are all forbidden. Diff needs that known container, but its original
// ancestors and their metadata must never be added to the public palette.
func personalNavigationVisibleDelta(state navigationEditorState, desired navigation.Tree, editable map[string]string) (navigation.Delta, error) {
	baseSections := map[string]navigation.Section{}
	baseGroups := map[string]navigation.Group{}
	for _, section := range state.Base.Sections {
		baseSections[section.ID] = section
		for _, group := range section.Groups {
			baseGroups[group.ID] = group
		}
	}
	current := navigationEditorNodes(state.Desired)
	base := navigation.Tree{Version: state.Base.Version, Context: state.Base.Context, Sections: []navigation.Section{}}
	wanted := desired
	wanted.Sections = append([]navigation.Section(nil), desired.Sections...)
	extra := map[string]bool{}
	for _, full := range state.layerBase.Sections {
		section, inBase := baseSections[full.ID]
		_, inDesired := current[full.ID]
		needed := inBase || inDesired
		for _, group := range full.Groups {
			if _, visible := current[group.ID]; visible {
				needed = true
			}
		}
		if !needed {
			continue
		}
		if !inBase {
			section = full
			section.Items = nil
		}
		section.Groups = nil
		for _, fullGroup := range full.Groups {
			group, visible := baseGroups[fullGroup.ID]
			if !visible {
				if _, visible = current[fullGroup.ID]; !visible {
					continue
				}
				group = fullGroup
				group.Items = nil
			}
			section.Groups = append(section.Groups, group)
		}
		base.Sections = append(base.Sections, section)
		if _, visible := editable[full.ID]; !visible {
			// Keep this internal ancestor in wanted so Diff emits a hide for
			// an omitted editable group, rather than hiding its unseen parent.
			extra[full.ID] = true
			ancestor := full
			ancestor.Items, ancestor.Groups = nil, nil
			wanted.Sections = append(wanted.Sections, ancestor)
		}
	}
	delta, err := navigation.Diff(base, wanted, navigation.UserLayer)
	if err != nil {
		return navigation.Delta{}, err
	}
	ops := delta.Ops[:0]
	for _, op := range delta.Ops {
		if !extra[op.Node] {
			ops = append(ops, op)
		}
	}
	delta.Ops = ops
	return delta, nil
}

// Replace editable intent while preserving valid previous intent for nodes
// currently outside RBAC. Re-diffing the full result removes stale references
// without turning the permitted projection into a persisted full snapshot.
func personalNavigationDelta(state navigationEditorState, input navigationEditorRequest) (navigation.Delta, navigation.Tree, error) {
	editable := navigationEditorNodes(state.Base)
	for id, title := range navigationEditorNodes(state.Desired) {
		editable[id] = title
	}
	visible, err := personalNavigationVisibleDelta(state, input.Desired, editable)
	if err != nil {
		return navigation.Delta{}, navigation.Tree{}, err
	}
	known := navigationEditorNodes(state.layerBase)
	for id, title := range navigationEditorNodes(state.effective) {
		known[id] = title
	}
	hash, _ := state.layerBase.Hash()
	combined := navigation.Delta{Version: 1, BaseHash: hash, Ops: []navigation.Operation{}}
	locations, _ := navigationEditorLocations(state.layerBase)
	_, previousSiblings := navigationEditorLocations(state.effective)
	currentLocations, currentSiblings := navigationEditorLocations(state.Desired)
	wantedLocations, wantedSiblings := navigationEditorLocations(input.Desired)
	predecessors := func(siblings map[navigationEditorLocation][]string) map[string]string {
		result := map[string]string{}
		for _, ids := range siblings {
			previous := ""
			for _, id := range ids {
				result[id], previous = previous, id
			}
		}
		return result
	}
	currentAfter, wantedAfter := predecessors(currentSiblings), predecessors(wantedSiblings)
	retainedMoves := map[string]navigation.Operation{}
	for _, op := range state.previous.Ops {
		current, present := currentLocations[op.Node]
		wanted, remains := wantedLocations[op.Node]
		_, anchorEditable := editable[op.After]
		if op.Op == "move" && op.After != "" && !anchorEditable && present && remains && current == wanted && current.parent == op.Parent && currentAfter[op.Node] == wantedAfter[op.Node] {
			// An unchanged visible placement still follows its closed common
			// sibling. The anchor is checked against the authoritative tree,
			// never reintroduced into the client editing surface.
			retainedMoves[op.Node] = op
		}
	}
	for _, op := range visible.Ops {
		if prior, present := retainedMoves[op.Node]; op.Op == "move" && present {
			if locations[prior.After] == wantedLocations[op.Node] {
				op.After = prior.After
			}
			delete(retainedMoves, op.Node)
		}
		combined.Ops = append(combined.Ops, op)
		navigationEditorTrackLocation(locations, op)
	}
	// Diff may omit a move whose only difference was a now-closed sibling.
	// Preserve such intent only while that sibling still has this destination.
	for _, prior := range state.previous.Ops {
		if op, present := retainedMoves[prior.Node]; present && locations[op.After] == wantedLocations[op.Node] {
			combined.Ops = append(combined.Ops, op)
			navigationEditorTrackLocation(locations, op)
			delete(retainedMoves, prior.Node)
		}
	}
	privateRenames := map[string]string{}
	for _, op := range state.previous.Ops {
		id := op.Node
		if op.ID != "" {
			id = op.ID
		}
		_, canEdit := editable[id]
		_, stillKnown := known[id]
		if !canEdit && stillKnown {
			op = personalNavigationAfter(op, locations, previousSiblings)
			combined.Ops = append(combined.Ops, op)
			navigationEditorTrackLocation(locations, op)
			if op.Op == "rename" {
				privateRenames[id] = *op.Title
			}
		}
	}
	desired, _, err := navigation.ApplyDelta(state.layerBase, combined, navigation.UserLayer)
	if err != nil {
		return navigation.Delta{}, navigation.Tree{}, err
	}
	delta, err := navigation.Diff(state.layerBase, desired, navigation.UserLayer)
	if err != nil {
		return navigation.Delta{}, navigation.Tree{}, err
	}
	// Explicitly typing the inherited title is still a personal override, even
	// when the current common title is equal. Preserve it across future renames.
	wanted := navigationEditorNodes(input.Desired)
	baseNodes := navigationEditorNodes(state.layerBase)
	seen := map[string]bool{}
	for _, id := range input.Renamed {
		_, inherited := baseNodes[id]
		title, present := wanted[id]
		if seen[id] || !inherited || !present {
			return navigation.Delta{}, navigation.Tree{}, errors.New("invalid rename intent")
		}
		seen[id] = true
		privateRenames[id] = title
	}
	for _, op := range delta.Ops {
		if op.Op == "rename" {
			delete(privateRenames, op.Node)
		}
	}
	ids := make([]string, 0, len(privateRenames))
	fullNodes := navigationEditorNodes(desired)
	for id := range privateRenames {
		if _, present := fullNodes[id]; present {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		title := privateRenames[id]
		delta.Ops = append(delta.Ops, navigation.Operation{Op: "rename", Node: id, Title: &title})
	}
	desired, _, err = navigation.ApplyDelta(state.layerBase, delta, navigation.UserLayer)
	if err == nil {
		_, err = navigation.EncodeDelta(delta, navigation.UserLayer)
	}
	return delta, desired, err
}

func (s *Server) personalNavigation(w http.ResponseWriter, r *http.Request) {
	sub := r.URL.Query().Get("subsystem")
	if !s.personalNavigationAllowed(w, r, sub) {
		return
	}
	state, err := s.personalNavigationState(r, sub)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
		return
	}
	s.renderNavigationEditor(w, r, state, sub, "", http.StatusOK)
}

func (s *Server) personalNavigationWrite(w http.ResponseWriter, r *http.Request, preview bool) {
	if !s.personalNavigationAllowed(w, r, "") {
		return
	}
	input, err := readNavigationEditorRequestMode(w, r, true, true)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	if !s.personalNavigationAllowed(w, r, input.Subsystem) {
		return
	}
	state, err := s.personalNavigationState(r, input.Subsystem)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
		return
	}
	if input.Revision != state.Setting.Revision || input.BaseRevision != state.BaseRevision {
		s.personalNavigationConflict(w, r, state, input.Subsystem)
		return
	}
	if err := allocateNavigationContainersForLayer(state.Base, state.Desired, &input.Desired, navigation.UserLayer); err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	delta, desired, err := personalNavigationDelta(state, input)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	if preview {
		state.effective = desired
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"preview": s.editorNavigationPreview(r, state, input.Subsystem)})
		return
	}
	next, err := s.store.SaveNavigationSettings(r.Context(), personalNavigationScope(r, state.Base.Context), state.layerBase, delta, input.Revision)
	if errors.Is(err, storage.ErrVersionConflict) {
		fresh, err := s.personalNavigationState(r, input.Subsystem)
		if err != nil {
			s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
			return
		}
		s.personalNavigationConflict(w, r, fresh, input.Subsystem)
		return
	}
	if err != nil {
		s.navigationEditorError(w, r, http.StatusInternalServerError, "Не удалось сохранить настройку меню")
		return
	}
	s.auditNavigation(r, "navigation.user.save", state.Base.Context, state.Setting.Revision, next.Revision, len(delta.Ops))
	s.personalNavigationRedirect(w, r, input.Subsystem)
}

func (s *Server) personalNavigationSave(w http.ResponseWriter, r *http.Request) {
	s.personalNavigationWrite(w, r, false)
}

func (s *Server) personalNavigationPreview(w http.ResponseWriter, r *http.Request) {
	s.personalNavigationWrite(w, r, true)
}

func (s *Server) personalNavigationReset(w http.ResponseWriter, r *http.Request) {
	if !s.personalNavigationAllowed(w, r, "") {
		return
	}
	input, err := readNavigationEditorRequestMode(w, r, false, true)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	if !s.personalNavigationAllowed(w, r, input.Subsystem) {
		return
	}
	state, err := s.personalNavigationState(r, input.Subsystem)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
		return
	}
	next, err := s.store.DeleteNavigationSettings(r.Context(), personalNavigationScope(r, state.Base.Context), input.Revision)
	if errors.Is(err, storage.ErrVersionConflict) {
		s.personalNavigationConflict(w, r, state, input.Subsystem)
		return
	}
	if err != nil {
		s.navigationEditorError(w, r, http.StatusInternalServerError, "Не удалось сохранить настройку меню")
		return
	}
	s.auditNavigation(r, "navigation.user.reset", state.Base.Context, state.Setting.Revision, next.Revision, 0)
	s.personalNavigationRedirect(w, r, input.Subsystem)
}

func (s *Server) personalNavigationConflict(w http.ResponseWriter, r *http.Request, state navigationEditorState, sub string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"revision": state.Setting.Revision, "baseRevision": state.BaseRevision, "preview": s.editorNavigationPreview(r, state, sub)})
		return
	}
	s.renderNavigationEditor(w, r, state, sub, s.tr(s.resolveLang(r), "Меню изменилось в другой вкладке. Загрузите актуальную версию и повторите изменения."), http.StatusConflict)
}

func (s *Server) personalNavigationRedirect(w http.ResponseWriter, r *http.Request, sub string) {
	query := url.Values{"saved": {"1"}}
	if sub != "" {
		query.Set("subsystem", sub)
	}
	http.Redirect(w, r, "/ui/settings/navigation?"+query.Encode(), http.StatusSeeOther)
}
