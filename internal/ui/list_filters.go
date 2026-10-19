package ui

import (
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Панель «Отбор» списка с основным набором полей (list_filters сущности):
// показываются основные поля, добавленные пользователем кнопкой «+ Отбор»
// (запоминаются за ним, POST /ui/_list-filters) и поля, по которым отбор уже
// включён, — чтобы действующий фильтр не прятался. Без list_filters панель,
// как раньше, из всех реквизитов.

type listFilterChoice struct {
	Field   metadata.Field
	Checked bool
}

func listFilterData(data any) (*metadata.Entity, map[string]any) {
	m, _ := data.(map[string]any)
	ent, _ := m["Entity"].(*metadata.Entity)
	return ent, m
}

// listFilterPanelFields — поля панели «Отбор» в порядке показа.
func listFilterPanelFields(data any) []metadata.Field {
	ent, m := listFilterData(data)
	if ent == nil {
		return nil
	}
	if len(ent.ListFilters) == 0 {
		return ent.Fields
	}
	shown := map[string]bool{}
	for _, name := range ent.ListFilters {
		shown[name] = true
	}
	extra, _ := m["ListFilterExtra"].([]string)
	for _, name := range extra {
		shown[name] = true
	}
	if params, ok := m["Params"].(storage.ListParams); ok {
		for name := range params.Filters {
			shown[name] = true
		}
	}
	out := make([]metadata.Field, 0, len(shown))
	for _, name := range ent.ListFilters {
		if f := listFilterField(ent, name); f != nil {
			out = append(out, *f)
		}
	}
	basic := map[string]bool{}
	for _, name := range ent.ListFilters {
		basic[name] = true
	}
	for _, f := range ent.Fields {
		if shown[f.Name] && !basic[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

// listFilterChoices — реквизиты для окна «+ Отбор»: всё, кроме основного
// набора и признака активности; отмечены добавленные пользователем.
func listFilterChoices(data any) []listFilterChoice {
	ent, m := listFilterData(data)
	if ent == nil || len(ent.ListFilters) == 0 {
		return nil
	}
	basic := map[string]bool{}
	for _, name := range ent.ListFilters {
		basic[name] = true
	}
	checked := map[string]bool{}
	extra, _ := m["ListFilterExtra"].([]string)
	for _, name := range extra {
		checked[name] = true
	}
	var out []listFilterChoice
	for _, f := range ent.Fields {
		if basic[f.Name] || (ent.Activity != nil && f.Name == ent.Activity.Field) {
			continue
		}
		out = append(out, listFilterChoice{Field: f, Checked: checked[f.Name]})
	}
	return out
}

func listFilterField(ent *metadata.Entity, name string) *metadata.Field {
	for i := range ent.Fields {
		if ent.Fields[i].Name == name {
			return &ent.Fields[i]
		}
	}
	return nil
}

// listFilterExtra — добавленные текущим пользователем поля отбора (только при
// list_filters у сущности).
func (s *Server) listFilterExtra(r *http.Request, ent *metadata.Entity) []string {
	if ent == nil || len(ent.ListFilters) == 0 || s.store == nil {
		return nil
	}
	return s.store.GetListFilterFields(r.Context(), ent.Name, currentUserLogin(r))
}

// listFilterFieldsSave — POST /ui/_list-filters: entity, field (повторяется),
// back (адрес списка для возврата). Поля проверяются по метаданным.
func (s *Server) listFilterFieldsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ent := s.reg.GetEntity(strings.TrimSpace(r.FormValue("entity")))
	if ent == nil || len(ent.ListFilters) == 0 || !s.can(r, string(ent.Kind), ent.Name, "read") {
		http.Error(w, "список не найден", http.StatusNotFound)
		return
	}
	allowed := map[string]bool{}
	for _, c := range listFilterChoices(map[string]any{"Entity": ent}) {
		allowed[c.Field.Name] = true
	}
	var fields []string
	seen := map[string]bool{}
	for _, name := range r.Form["field"] {
		name = strings.TrimSpace(name)
		if !allowed[name] {
			http.Error(w, "неизвестное поле отбора: "+name, http.StatusBadRequest)
			return
		}
		if !seen[name] {
			seen[name] = true
			fields = append(fields, name)
		}
	}
	if err := s.store.SaveListFilterFields(r.Context(), ent.Name, currentUserLogin(r), fields); err != nil {
		s.serverError(w, r, err)
		return
	}
	back := strings.TrimSpace(r.FormValue("back"))
	if !strings.HasPrefix(back, "/ui/") || strings.HasPrefix(back, "//") || strings.Contains(back, "\\") {
		back = "/ui/" + strings.ToLower(string(ent.Kind)) + "/" + strings.ToLower(ent.Name)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
