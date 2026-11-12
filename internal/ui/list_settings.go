package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// «Настройка списка» документов и справочников за пользователем: какие
// колонки показывать, в каком порядке и какой ширины. Набор колонок — тот же,
// что и без настройки (resolveListColumns); настройка только выбирает и
// упорядочивает их. Хранение — _settings, как у журналов.

const maxListSettingsBytes = 32 * 1024

var errListSettingsTooLarge = errors.New("настройка списка слишком велика")

type ListUserSettings struct {
	Columns []ListColumnSetting `json:"columns,omitempty"`
}

type ListColumnSetting struct {
	Field   string `json:"field"`
	Visible bool   `json:"visible"`
	// Width — ширина колонки в px; 0 — по содержимому.
	Width int `json:"width,omitempty"`
}

// listColumnUI — колонка для окна настройки и для отрисовки списка.
type listColumnUI struct {
	Field   metadata.Field
	Visible bool
	Width   int
}

const (
	listColumnMinWidth = 40
	listColumnMaxWidth = 1200
)

func parseListSettings(raw string) (*ListUserSettings, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var st ListUserSettings
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// listSettingsColumns — все колонки списка в порядке пользователя (известные
// настройке — первыми, в её порядке; новые реквизиты — в конце, видимыми).
// Поля, которых больше нет, отбрасываются.
func listSettingsColumns(entity *metadata.Entity, st *ListUserSettings) []listColumnUI {
	base := resolveListColumns(entity)
	byName := make(map[string]metadata.Field, len(base))
	for _, f := range base {
		byName[strings.ToLower(f.Name)] = f
	}
	out := make([]listColumnUI, 0, len(base))
	seen := map[string]bool{}
	if st != nil {
		for _, c := range st.Columns {
			key := strings.ToLower(strings.TrimSpace(c.Field))
			f, ok := byName[key]
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			w := c.Width
			if w != 0 && w < listColumnMinWidth {
				w = listColumnMinWidth
			}
			if w > listColumnMaxWidth {
				w = listColumnMaxWidth
			}
			out = append(out, listColumnUI{Field: f, Visible: c.Visible, Width: w})
		}
	}
	for _, f := range base {
		if !seen[strings.ToLower(f.Name)] {
			out = append(out, listColumnUI{Field: f, Visible: true})
		}
	}
	// Совсем пустой список колонок бесполезен — тогда показываем первую.
	any := false
	for _, c := range out {
		any = any || c.Visible
	}
	if !any && len(out) > 0 {
		out[0].Visible = true
	}
	return out
}

func visibleListColumns(cols []listColumnUI) []listColumnUI {
	out := make([]listColumnUI, 0, len(cols))
	for _, c := range cols {
		if c.Visible {
			out = append(out, c)
		}
	}
	return out
}

// canonicalListSettingsJSON — настройка, приведённая к колонкам списка.
func canonicalListSettingsJSON(entity *metadata.Entity, st *ListUserSettings) string {
	cols := listSettingsColumns(entity, st)
	canon := ListUserSettings{Columns: make([]ListColumnSetting, 0, len(cols))}
	for _, c := range cols {
		canon.Columns = append(canon.Columns, ListColumnSetting{Field: c.Field.Name, Visible: c.Visible, Width: c.Width})
	}
	b, _ := json.Marshal(canon)
	return string(b)
}

func (s *Server) loadListSettings(r *http.Request, entity *metadata.Entity) *ListUserSettings {
	if s.store == nil || entity == nil {
		return nil
	}
	raw, err := s.store.GetListUserSettings(r.Context(), string(entity.Kind), entity.Name, currentUserLogin(r))
	if err != nil || raw == "" {
		return nil
	}
	st, err := parseListSettings(raw)
	if err != nil {
		return nil
	}
	return st
}

// listSettingsData — данные шаблона списка: колонки к показу и окно настройки.
func (s *Server) listSettingsData(r *http.Request, entity *metadata.Entity, data map[string]any) {
	st := s.loadListSettings(r, entity)
	cols := listSettingsColumns(entity, st)
	data["ListColumns"] = visibleListColumns(cols)
	data["ListSettingsColumns"] = cols
	data["ListSettingsJSON"] = canonicalListSettingsJSON(entity, st)
	data["ListSettingsActive"] = st != nil
	data["ListSettingsURL"] = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + url.PathEscape(strings.ToLower(entity.Name)) + "/list-settings"
}

func listReturnURL(r *http.Request, entity *metadata.Entity) string {
	fallback := "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + url.PathEscape(strings.ToLower(entity.Name))
	raw := strings.TrimSpace(r.FormValue("__return"))
	if raw == "" {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/ui/") {
		return fallback
	}
	return u.RequestURI()
}

// listSettingsSave сохраняет настройку. Запрос fetch (перетаскивание ширины)
// получает 204, обычная форма — переход обратно к списку.
func (s *Server) listSettingsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	raw := r.FormValue("__list_settings")
	if len(raw) > maxListSettingsBytes {
		http.Error(w, s.errText(r, errListSettingsTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	st, err := parseListSettings(raw)
	if err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveListUserSettings(r.Context(), string(entity.Kind), entity.Name, currentUserLogin(r), canonicalListSettingsJSON(entity, st)); err != nil {
		s.serverError(w, r, err)
		return
	}
	if r.Header.Get("X-Requested-With") == "obListSettings" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, listReturnURL(r, entity), http.StatusSeeOther)
}

func (s *Server) listSettingsReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteListUserSettings(r.Context(), string(entity.Kind), entity.Name, currentUserLogin(r)); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, listReturnURL(r, entity), http.StatusSeeOther)
}

// listColumnFields — реквизиты колонок (для шаблона: дерево и строки списка).
func listColumnFields(cols []listColumnUI) []metadata.Field {
	out := make([]metadata.Field, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Field)
	}
	return out
}

// listDefaultColumnWidth — ширина колонки без заданной, когда у других она
// задана и таблица раскладывается фиксированно.
const listDefaultColumnWidth = 150

// listColumnsFixed — у какой-то показанной колонки задана ширина: таблица
// раскладывается фиксированно, иначе ширины — по содержимому, как раньше.
func listColumnsFixed(cols []listColumnUI) bool {
	for _, c := range cols {
		if c.Width > 0 {
			return true
		}
	}
	return false
}

func listColumnWidth(c listColumnUI) int {
	if c.Width > 0 {
		return c.Width
	}
	return listDefaultColumnWidth
}

// listTableWidth — ширина таблицы при фиксированной раскладке: сумма колонок
// и служебных (✓ документа, «Группа» поиска, кнопка «Открыть»).
func listTableWidth(cols []listColumnUI, document, group bool) int {
	w := 90
	if document {
		w += 36
	}
	if group {
		w += 160
	}
	for _, c := range cols {
		w += listColumnWidth(c)
	}
	return w
}
