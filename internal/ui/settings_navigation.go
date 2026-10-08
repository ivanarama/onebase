package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/storage"
)

const maxNavigationFormBytes = 2 << 20

// navigationEditorRequest carries a desired layout, never a SQL key or a delta.
// The authenticated handler selects the scope and derives changes on the server.
type navigationEditorRequest struct {
	Subsystem    string          `json:"subsystem"`
	Revision     string          `json:"revision"`
	Desired      navigation.Tree `json:"desired"`
	BaseRevision string          `json:"base_revision"`
	Renamed      []string        `json:"renamed"`
}

type navigationEditorState struct {
	Base          navigation.Tree
	Desired       navigation.Tree
	Setting       storage.NavigationSettings
	Diagnostics   []navigation.Diagnostic
	Configured    bool
	Flat          bool
	Personal      bool
	BaseRevision  string
	Renamed       []string
	Origins       map[string]string
	configuration navigation.Tree
	layerBase     navigation.Tree
	effective     navigation.Tree
	previous      navigation.Delta
}

func (s *Server) navigationEditorState(r *http.Request, sub string) (navigationEditorState, error) {
	var state navigationEditorState
	if sub != "" {
		current := s.reg.GetSubsystem(sub)
		if current == nil {
			return state, fmt.Errorf("unknown subsystem")
		}
		state.Base, state.Configured = s.configurationNavigation(current.Menu, &current.Contents, false, sub)
	} else if hp := s.reg.HomePage(); hp != nil {
		state.Base, state.Configured = s.configurationNavigation(hp.Menu, hp.Nav, true, "")
		state.Flat = hp.Nav.IsEmpty()
	} else {
		state.Base, state.Configured = s.configurationNavigation(nil, nil, true, "")
		state.Flat = true
	}
	var err error
	state.Setting, err = s.store.GetNavigationSettings(r.Context(), storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: state.Base.Context})
	if err != nil {
		return state, err
	}
	var raw []byte
	if state.Setting.Exists {
		raw = []byte(state.Setting.Raw)
	}
	state.Desired, state.Diagnostics = navigation.Compose(state.Base, raw, nil)
	return state, nil
}

func (s *Server) navigationAdminAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return false
	}
	if s.store == nil {
		http.Error(w, s.tr(s.resolveLang(r), "Не удалось загрузить настройку меню"), http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) adminNavigation(w http.ResponseWriter, r *http.Request) {
	if !s.navigationAdminAllowed(w, r) {
		return
	}
	sub := r.URL.Query().Get("subsystem")
	state, err := s.navigationEditorState(r, sub)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Не удалось загрузить настройку меню")
		return
	}
	s.renderNavigationEditor(w, r, state, sub, "", http.StatusOK)
}

func (s *Server) renderNavigationEditor(w http.ResponseWriter, r *http.Request, state navigationEditorState, sub, message string, status int) {
	lang := s.resolveLang(r)
	path, ownedPrefix := "/ui/admin/navigation", "adm:"
	title, description := "Настройка приложения", "Общая настройка базы. Изменения меню видны всем пользователям с учётом их прав."
	resetConfirm, resetLabel := "Сбросить общую настройку к конфигурации?", "Сбросить к конфигурации"
	if state.Personal {
		path, ownedPrefix = "/ui/settings/navigation", "usr:"
		title, description = "Мои настройки", "Личная настройка меню. Неизменённые узлы наследуют общую настройку базы."
		resetConfirm, resetLabel = "Сбросить личную настройку к общей?", "Сбросить к общей настройке"
	}
	labels := map[string]string{}
	for key, value := range map[string]string{
		"up": "Выше", "down": "Ниже", "in": "Внутрь папки", "out": "Из папки",
		"hide": "Скрыть", "remove": "Удалить", "restore": "Вернуть", "title": "Основное название", "icon": "Иконка",
		"parent": "Расположение", "changed": "Меню изменено", "newSection": "Новый раздел", "newGroup": "Новая папка",
		"error": "Не удалось получить предпросмотр меню", "unsaved": "Изменения меню не сохранены",
		"reset": "Сбросить общую настройку к конфигурации?", "empty": "Добавьте раздел и верните в него объекты из палитры",
		"conflict":  "Меню изменилось в другой вкладке. Загрузите актуальную версию и повторите изменения.",
		"saveError": "Не удалось сохранить настройку меню", "reload": "Загрузить актуальную версию",
		"reloadConfirm": "Загрузить актуальную версию и отбросить несохранённые изменения?",
	} {
		labels[key] = s.tr(lang, value)
	}
	labels["reset"] = s.tr(lang, resetConfirm)
	for key, value := range map[string]string{"configuration": "Конфигурация", "common": "Общая настройка", "personal": "Моя настройка"} {
		labels[key] = s.tr(lang, value)
	}
	bootstrap := map[string]any{
		"base": state.Base, "desired": state.Desired, "subsystem": sub,
		"revision": state.Setting.Revision, "labels": labels, "icons": LucideNames(),
		"preview":  s.editorNavigationPreview(r, state, sub),
		"personal": state.Personal, "ownedPrefix": ownedPrefix, "path": path,
	}
	if state.Personal {
		bootstrap["baseRevision"], bootstrap["renamed"], bootstrap["origins"] = state.BaseRevision, state.Renamed, state.Origins
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.render(w, r, "page-navigation-settings", map[string]any{
		"Nav": s.buildNav(r, sub), "Subsystems": s.visibleSubsystems(r), "CurrentSubsystem": sub, "HideHome": s.hideGlobalHome(),
		"NavigationEditor": bootstrap, "NavigationRevision": state.Setting.Revision,
		"NavigationSubsystem": sub, "NavigationDiagnostics": state.Diagnostics,
		"NavigationMessage": message, "NavigationSaved": r.URL.Query().Get("saved") != "",
		"NavigationPersonal": state.Personal, "NavigationPath": path, "NavigationTitle": title,
		"NavigationDescription": description, "NavigationResetConfirm": resetConfirm, "NavigationResetLabel": resetLabel,
		"NavigationBaseRevision": state.BaseRevision,
	})
}

func (s *Server) editorNavigationPreview(r *http.Request, state navigationEditorState, sub string) []NavigationPreviewSection {
	base, desired := state.Base, state.Desired
	if state.Personal {
		base, desired = state.configuration, state.effective
	}
	return navigationPreview(s.navigationGroups(r, desired, base, state.Configured, state.Flat, sub))
}

func (s *Server) navigationEditorError(w http.ResponseWriter, r *http.Request, status int, key string) {
	http.Error(w, s.tr(s.resolveLang(r), key), status)
}

// Fields are taken only from the POST body. URL query parameters, login and SQL
// key never choose a storage scope. MaxBytesReader bounds form and JSON parsing.
func readNavigationEditorRequest(w http.ResponseWriter, r *http.Request, treeRequired bool) (navigationEditorRequest, error) {
	return readNavigationEditorRequestMode(w, r, treeRequired, false)
}

func readNavigationEditorRequestMode(w http.ResponseWriter, r *http.Request, treeRequired, personal bool) (navigationEditorRequest, error) {
	var input navigationEditorRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxNavigationFormBytes)
	if err := r.ParseForm(); err != nil {
		return input, err
	}
	for key, values := range r.PostForm {
		allowed := key == "subsystem" || key == "revision" || key == "desired" || personal && treeRequired && (key == "base_revision" || key == "renamed")
		if len(values) != 1 || !allowed {
			return input, fmt.Errorf("unexpected form field")
		}
	}
	input.Subsystem, input.Revision = r.PostForm.Get("subsystem"), r.PostForm.Get("revision")
	if _, ok := r.PostForm["revision"]; !ok {
		return input, fmt.Errorf("missing revision")
	}
	if !treeRequired {
		return input, nil
	}
	if personal {
		input.BaseRevision = r.PostForm.Get("base_revision")
		if input.BaseRevision == "" || !utf8.ValidString(r.PostForm.Get("renamed")) {
			return input, fmt.Errorf("missing base revision or invalid rename intent")
		}
		decoder := json.NewDecoder(strings.NewReader(r.PostForm.Get("renamed")))
		if err := decoder.Decode(&input.Renamed); err != nil {
			return input, err
		}
		if input.Renamed == nil || len(input.Renamed) > navigation.MaxOperations {
			return input, fmt.Errorf("invalid rename intent")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return input, fmt.Errorf("trailing rename JSON")
		}
	}
	raw := r.PostForm.Get("desired")
	if !utf8.ValidString(raw) {
		return input, fmt.Errorf("invalid UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input.Desired); err != nil {
		return input, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input, fmt.Errorf("trailing JSON")
	}
	return input, nil
}

// New containers use temporary new:* IDs in the form. Only the server creates
// persistent adm: UUIDs. Existing identities and all item targets remain fixed.
func allocateNavigationContainers(base, effective navigation.Tree, desired *navigation.Tree) error {
	return allocateNavigationContainersForLayer(base, effective, desired, navigation.AdminLayer)
}

func allocateNavigationContainersForLayer(base, effective navigation.Tree, desired *navigation.Tree, layer navigation.Layer) error {
	existing := map[string]string{}
	for _, tree := range []navigation.Tree{base, effective} {
		for _, section := range tree.Sections {
			existing[section.ID] = "section"
			for _, group := range section.Groups {
				existing[group.ID] = "group"
			}
		}
	}
	seen := map[string]bool{}
	allocate := func(id *string, kind string) error {
		if seen[*id] {
			return fmt.Errorf("duplicate container")
		}
		seen[*id] = true
		if previous, ok := existing[*id]; ok {
			if previous != kind {
				return fmt.Errorf("changed container kind")
			}
			return nil
		}
		if !strings.HasPrefix(*id, "new:") || len(*id) > 128 || len(*id) <= 4 {
			return fmt.Errorf("unexpected container ID")
		}
		var err error
		*id, err = navigation.NewCustomID(layer)
		return err
	}
	for i := range desired.Sections {
		if err := allocate(&desired.Sections[i].ID, "section"); err != nil {
			return err
		}
		for j := range desired.Sections[i].Groups {
			if err := allocate(&desired.Sections[i].Groups[j].ID, "group"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) adminNavigationWrite(w http.ResponseWriter, r *http.Request, preview bool) {
	if !s.navigationAdminAllowed(w, r) {
		return
	}
	input, err := readNavigationEditorRequest(w, r, true)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	state, err := s.navigationEditorState(r, input.Subsystem)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Не удалось загрузить настройку меню")
		return
	}
	// Check the revision before validating identities against the current layer:
	// a stale tab may contain a custom node another administrator has removed.
	if !preview && input.Revision != state.Setting.Revision {
		s.navigationEditorConflict(w, r, input.Subsystem)
		return
	}
	// An existing custom node is validated against the previous stored layer;
	// the final diff still uses configuration as its authoritative base.
	if err := allocateNavigationContainers(state.Base, state.Desired, &input.Desired); err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	delta, err := navigation.Diff(state.Base, input.Desired, navigation.AdminLayer)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	desired, diagnostics, err := navigation.ApplyDelta(state.Base, delta, navigation.AdminLayer)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	if preview {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"preview":     navigationPreview(s.navigationGroups(r, desired, state.Base, state.Configured, state.Flat, input.Subsystem)),
			"diagnostics": diagnostics,
		})
		return
	}
	next, err := s.store.SaveNavigationSettings(r.Context(), storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: state.Base.Context}, state.Base, delta, input.Revision)
	if errors.Is(err, storage.ErrVersionConflict) {
		s.navigationEditorConflict(w, r, input.Subsystem)
		return
	}
	if err != nil {
		s.navigationEditorError(w, r, http.StatusInternalServerError, "Не удалось сохранить настройку меню")
		return
	}
	s.auditNavigation(r, "navigation.admin.save", state.Base.Context, state.Setting.Revision, next.Revision, len(delta.Ops))
	s.navigationEditorRedirect(w, r, input.Subsystem)
}

func (s *Server) adminNavigationSave(w http.ResponseWriter, r *http.Request) {
	s.adminNavigationWrite(w, r, false)
}

func (s *Server) adminNavigationPreview(w http.ResponseWriter, r *http.Request) {
	s.adminNavigationWrite(w, r, true)
}

func (s *Server) adminNavigationReset(w http.ResponseWriter, r *http.Request) {
	if !s.navigationAdminAllowed(w, r) {
		return
	}
	input, err := readNavigationEditorRequest(w, r, false)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Некорректная структура меню")
		return
	}
	state, err := s.navigationEditorState(r, input.Subsystem)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusBadRequest, "Не удалось загрузить настройку меню")
		return
	}
	next, err := s.store.DeleteNavigationSettings(r.Context(), storage.NavigationSettingsScope{Layer: navigation.AdminLayer, Context: state.Base.Context}, input.Revision)
	if errors.Is(err, storage.ErrVersionConflict) {
		s.navigationEditorConflict(w, r, input.Subsystem)
		return
	}
	if err != nil {
		s.navigationEditorError(w, r, http.StatusInternalServerError, "Не удалось сохранить настройку меню")
		return
	}
	s.auditNavigation(r, "navigation.admin.reset", state.Base.Context, state.Setting.Revision, next.Revision, 0)
	s.navigationEditorRedirect(w, r, input.Subsystem)
}

func (s *Server) navigationEditorConflict(w http.ResponseWriter, r *http.Request, sub string) {
	state, err := s.navigationEditorState(r, sub)
	if err != nil {
		s.navigationEditorError(w, r, http.StatusServiceUnavailable, "Не удалось загрузить настройку меню")
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revision": state.Setting.Revision,
			"preview":  navigationPreview(s.navigationGroups(r, state.Desired, state.Base, state.Configured, state.Flat, sub)),
		})
		return
	}
	s.renderNavigationEditor(w, r, state, sub, s.tr(s.resolveLang(r), "Меню изменилось в другой вкладке. Загрузите актуальную версию и повторите изменения."), http.StatusConflict)
}

func (s *Server) navigationEditorRedirect(w http.ResponseWriter, r *http.Request, sub string) {
	query := url.Values{"saved": {"1"}}
	if sub != "" {
		query.Set("subsystem", sub)
	}
	http.Redirect(w, r, "/ui/admin/navigation?"+query.Encode(), http.StatusSeeOther)
}

func (s *Server) auditNavigation(r *http.Request, action, context, before, after string, count int) {
	entry := &storage.AuditEntry{Action: action, EntityKind: "navigation", EntityName: context,
		OldValue: map[string]any{"revision": before}, NewValue: map[string]any{"revision": after, "operations": count}, IP: r.RemoteAddr}
	if user := auth.UserFromContext(r.Context()); user != nil {
		entry.UserID, entry.UserLogin = user.ID, user.Login
	}
	if err := s.store.Log(r.Context(), entry); err != nil {
		slog.Warn("navigation audit unavailable", "context", context, "action", action)
	}
}
