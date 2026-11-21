package report

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

// Drilldown selects one output cell, or a labelled row action when Field is
// empty. Dynamic types must be declared in Targets; data/settings cannot
// introduce arbitrary entity names or URLs.
type Drilldown struct {
	Field     string                     `yaml:"field,omitempty"`
	Title     string                     `yaml:"title,omitempty"`
	IDField   string                     `yaml:"id_field"`
	TypeField string                     `yaml:"type_field,omitempty"`
	Target    *DrilldownTarget           `yaml:"target,omitempty"`
	Targets   map[string]DrilldownTarget `yaml:"targets,omitempty"`
}

type DrilldownTarget struct {
	Kind   string `yaml:"kind"` // entity | image | attachment
	Entity string `yaml:"entity,omitempty"`
}

func (d *DrilldownTarget) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("drilldown target requires a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k != "kind" && k != "entity" {
			return fmt.Errorf("unknown drilldown target key %q", k)
		}
	}
	type plain DrilldownTarget
	return n.Decode((*plain)(d))
}

func (d Drilldown) SelectTarget(selector string) (DrilldownTarget, bool) {
	if d.Target != nil && d.TypeField == "" && len(d.Targets) == 0 {
		return *d.Target, true
	}
	if d.Target != nil || d.TypeField == "" {
		return DrilldownTarget{}, false
	}
	target, ok := d.Targets[selector]
	return target, ok
}

// ValidateDrilldowns only validates metadata; it never reads records.
func ValidateDrilldowns(r *Report, lookup interface{ GetEntity(string) *metadata.Entity }) error {
	if len(r.Drilldowns) > 64 {
		return fmt.Errorf("at most 64 report drilldowns are allowed")
	}
	fields := map[string]bool{}
	validate := func(t DrilldownTarget) error {
		switch t.Kind {
		case "entity":
			if lookup == nil || t.Entity == "" {
				return fmt.Errorf("entity drilldown requires known metadata")
			}
			e := lookup.GetEntity(t.Entity)
			if e == nil || (e.Kind != metadata.KindCatalog && e.Kind != metadata.KindDocument) {
				return fmt.Errorf("unknown entity drilldown target %q", t.Entity)
			}
		case "image", "attachment":
			if t.Entity != "" {
				return fmt.Errorf("binary drilldown must use stored owner rights, not an entity override")
			}
		default:
			return fmt.Errorf("unsupported drilldown kind %q", t.Kind)
		}
		return nil
	}
	for i, d := range r.Drilldowns {
		if strings.TrimSpace(d.IDField) == "" || (d.Field == "" && strings.TrimSpace(d.Title) == "") {
			return fmt.Errorf("drilldown %d requires id_field and a row title or field", i)
		}
		if d.Field != "" {
			k := strings.ToLower(d.Field)
			if fields[k] {
				return fmt.Errorf("duplicate cell drilldown %q", d.Field)
			}
			fields[k] = true
		}
		if d.Target != nil {
			if d.TypeField != "" || len(d.Targets) > 0 {
				return fmt.Errorf("choose a fixed target or type_field/targets")
			}
			if err := validate(*d.Target); err != nil {
				return err
			}
		} else {
			if d.TypeField == "" || len(d.Targets) == 0 || len(d.Targets) > 64 {
				return fmt.Errorf("dynamic drilldown requires type_field and 1..64 targets")
			}
			for key, target := range d.Targets {
				if key == "" {
					return fmt.Errorf("empty drilldown type selector")
				}
				if err := validate(target); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
