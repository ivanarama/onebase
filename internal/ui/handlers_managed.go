package ui

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// pickManagedForm возвращает первую managed-форму нужного Kind из Entity.Forms
// или nil, если такой нет. Используется в рантайме для опционального
// переключения с авто-генерации на ручную форму из forms/<entity>/*.form.yaml.
//
// kind: "object" — карточка элемента/документа, "list" — форма списка,
// "choice" — форма выбора, "" — любая (берётся первая managed).
func pickManagedForm(entity *metadata.Entity, kind string) *metadata.FormModule {
	if entity == nil {
		return nil
	}
	kindLower := strings.ToLower(kind)
	for _, fm := range entity.Forms {
		if fm == nil || !fm.IsManaged() {
			continue
		}
		if kindLower == "" || strings.ToLower(fm.Kind) == kindLower {
			return fm
		}
	}
	return nil
}

// renderEntityForm — единая точка рендера формы объекта/документа.
// Если для Entity есть managed-форма с подходящим Kind — рендерит
// page-managed-form с теми же data + "Form": managed-форма.
// Иначе — текущий page-form (auto-generated).
//
// Это даёт пользователю опциональность: создание .form.yaml в проекте
// автоматически активирует managed-рендер для выбранной сущности; без
// .form.yaml продолжает работать существующая авто-форма без изменений.
func (s *Server) renderEntityForm(w http.ResponseWriter, r *http.Request, kind string, data map[string]any) {
	entity, _ := data["Entity"].(*metadata.Entity)
	managed := pickManagedForm(entity, kind)
	if managed != nil {
		data["Form"] = managed
		// Списки значений (СписокВыбора) объявлены на элементах формы, а не на
		// полях сущности, поэтому собираем их из самой managed-формы. Единая
		// точка покрывает все пути рендера (new/edit/повторный показ с ошибкой).
		data["ChoiceOptions"] = loadChoiceOptions(managed, s.resolveLang(r))
		s.prepareManagedFormData(data, managed)
		s.render(w, r, "page-managed-form", data)
		return
	}
	s.render(w, r, "page-form", data)
}

func (s *Server) prepareManagedFormData(data map[string]any, form *metadata.FormModule) {
	if form == nil || data == nil {
		return
	}
	if css := formConditionalCSS(form); css != "" {
		data["FormConditionalCSS"] = template.CSS(css)
	}
	rows, _ := data["TablePartRows"].(map[string][]map[string]any)
	if len(rows) == 0 || len(form.Conditional) == 0 || s.interp == nil {
		return
	}
	warnings := applyManagedFormConditionalStyles(form, rows, managedFormHeaderValues(data["Values"]), newInterpEvaluator(s.interp))
	if len(warnings) > 0 {
		data["FormWarnings"] = appendManagedFormWarnings(data["FormWarnings"], warnings)
	}
}

func managedFormHeaderValues(v any) map[string]any {
	out := map[string]any{}
	switch m := v.(type) {
	case map[string]string:
		for k, val := range m {
			out[k] = val
		}
	case map[string]any:
		for k, val := range m {
			out[k] = val
		}
	}
	return out
}

func appendManagedFormWarnings(existing any, warnings []string) []string {
	out, _ := existing.([]string)
	out = append(out, warnings...)
	return out
}
