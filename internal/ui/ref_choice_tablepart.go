package ui

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

func formChoiceElementTarget(owner *metadata.Entity, form *metadata.FormModule, el *metadata.FormElement) string {
	if field, tp := metadata.FormChoiceTPField(owner, form, el); tp != "" {
		if field != nil {
			return field.RefEntity
		}
		return ""
	}
	return formChoiceRefEntity(owner, form, el.DataPath)
}
func findTPChoiceElement(owner *metadata.Entity, form *metadata.FormModule, identity string) *metadata.FormElement {
	var found *metadata.FormElement
	count := 0
	form.Walk(func(el *metadata.FormElement) bool {
		_, tp := metadata.FormChoiceTPField(owner, form, el)
		if tp != "" && identity == tp+"."+el.ID {
			found = el
			count++
		}
		return true
	})
	if count != 1 {
		return nil
	}
	return found
}

type tpChoiceRender struct {
	Contexts map[string]string
	Options  map[string][][]map[string]any
}

func (s *Server) applyTPChoiceFilters(ctx context.Context, owner *metadata.Entity, form *metadata.FormModule, kind string, data map[string]any) {
	all := make(map[string]*tpChoiceRender)
	tableRows, _ := data["TablePartRows"].(map[string][]map[string]any)
	refs, _ := data["TPRefOptions"].(map[string]map[string][]map[string]any)
	form.Walk(func(el *metadata.FormElement) bool {
		field, tp := metadata.FormChoiceTPField(owner, form, el)
		if field == nil || tp == "" || el.ID == "" || len(el.ChoiceFilter) == 0 {
			return true
		}
		target := s.reg.GetEntity(field.RefEntity)
		if target == nil {
			return true
		}
		render := all[tp]
		if render == nil {
			render = &tpChoiceRender{Contexts: map[string]string{}, Options: map[string][][]map[string]any{}}
			all[tp] = render
		}
		controls := choiceSourceControls(el)
		// Preserve the declared path as the predicate key while publishing the
		// canonical DOM control name for row, object and form sources alike.
		for path, name := range controls {
			source, ok := metadata.ParseFormChoiceSource(path)
			if !ok {
				continue
			}
			switch {
			case strings.EqualFold(source.Root, "Объект"):
				if field, exists := entityFieldByName(owner, name); exists {
					controls[path] = field.Name
				}
			case strings.EqualFold(source.Root, "Форма"):
				if attr := formAttributeByName(form, name); attr != nil {
					controls[path] = attr.Name
				}
			case strings.EqualFold(source.Root, tp):
				for _, tablePart := range owner.TableParts {
					if tablePart.Name != tp {
						continue
					}
					for _, field := range tablePart.Fields {
						if strings.EqualFold(field.Name, name) {
							controls[path] = field.Name
							break
						}
					}
				}
			}
		}
		encoded, err := json.Marshal(managedChoiceContext{FormEntity: owner.Name, FormKind: kind, Form: form.Name, Element: tp + "." + el.ID, TablePart: tp, Sources: controls})
		if err == nil {
			render.Contexts[field.Name] = string(encoded)
		}
		// Shared reference arrays supply labels only. They must not carry an
		// unfiltered first page into a filtered column's dropdown.
		if refs != nil && refs[tp] != nil {
			refs[tp][field.Name] = nil
		}
		for _, row := range tableRows[tp] {
			sources := map[string]string{}
			for path := range controls {
				source, ok := metadata.ParseFormChoiceSource(path)
				if !ok {
					continue
				}
				values := data["Values"]
				if strings.EqualFold(source.Root, tp) {
					values = row
				}
				sources[path] = formValueForPath(values, source.Root+"."+source.Field)
			}
			selected := refValueString(row[field.Name])
			ownerID, ownerAsked := "", false
			if hf, ok := ownerHolderField(owner, target.Owner); ok {
				ownerID, ownerAsked = formValueForPath(data["Values"], "Объект."+hf.Name), true
			}
			options, err := s.initialChoiceOptions(ctx, owner, form, target, el, sources, selected, ownerID, ownerAsked)
			if err != nil {
				options = nil
			}
			render.Options[field.Name] = append(render.Options[field.Name], options)
			if refs != nil && refs[tp] != nil {
				refs[tp][field.Name] = s.appendSelectedRefOptions(ctx, refs[tp][field.Name], target, []string{selected})
			}
		}
		return true
	})
	data["TPChoices"] = all
}
