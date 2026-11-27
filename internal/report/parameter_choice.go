package report

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ValidateParamChoices rejects ignored/mistyped metadata and dependency cycles.
func ValidateParamChoices(rep *Report, entities []*metadata.Entity) error {
	byEntity := map[string]*metadata.Entity{}
	for _, e := range entities {
		byEntity[strings.ToLower(e.Name)] = e
	}
	byParam := map[string]Param{}
	for _, p := range rep.Params {
		byParam[p.Name] = p
	}
	field := func(e *metadata.Entity, name string) *metadata.Field {
		for i := range e.Fields {
			if strings.EqualFold(e.Fields[i].Name, name) {
				return &e.Fields[i]
			}
		}
		return nil
	}
	for _, p := range rep.Params {
		if len(p.ChoiceFilter) == 0 && p.ChoiceLabelSuffix == "" {
			continue
		}
		if !strings.HasPrefix(p.Type, "reference:") {
			return fmt.Errorf("parameter %s: choice metadata requires reference type", p.Name)
		}
		e := byEntity[strings.ToLower(strings.TrimPrefix(p.Type, "reference:"))]
		if e == nil || e.Kind != metadata.KindCatalog {
			return fmt.Errorf("parameter %s: choice target must be a catalog", p.Name)
		}
		for _, c := range p.ChoiceFilter {
			f := field(e, c.Field)
			src, ok := byParam[c.Param]
			if f == nil || f.RefEntity == "" || !ok || !strings.EqualFold(src.Type, "reference:"+f.RefEntity) {
				return fmt.Errorf("parameter %s: invalid choice field/parameter %s/%s", p.Name, c.Field, c.Param)
			}
		}
		seen := map[string]bool{}
		if len(p.ChoiceFilter) > 8 {
			return fmt.Errorf("parameter %s: maximum is 8 choice conditions", p.Name)
		}
		for _, c := range p.ChoiceFilter {
			key := strings.ToLower(c.Field)
			if seen[key] {
				return fmt.Errorf("parameter %s: duplicate choice field %s", p.Name, c.Field)
			}
			seen[key] = true
		}
		if p.ChoiceLabelSuffix != "" {
			f := field(e, p.ChoiceLabelSuffix)
			if f == nil || f.RefEntity == "" {
				return fmt.Errorf("parameter %s: choice_label_suffix must name a reference field", p.Name)
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("report choice dependency cycle: %s", name)
		}
		if done[name] {
			return nil
		}
		visiting[name] = true
		for _, c := range byParam[name].ChoiceFilter {
			if err := visit(c.Param); err != nil {
				return err
			}
		}
		visiting[name] = false
		done[name] = true
		return nil
	}
	for name := range byParam {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
