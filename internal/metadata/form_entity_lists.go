package metadata

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// FormEntityListSpec is a server-owned read-only projection, never a form table.
type FormEntityListSpec struct {
	Entity     string                    `yaml:"entity,omitempty" json:"entity,omitempty"`
	Filter     []FormEntityListCondition `yaml:"filter,omitempty" json:"filter,omitempty"`
	OrderBy    []string                  `yaml:"order_by,omitempty" json:"order_by,omitempty"`
	Limit      int                       `yaml:"limit,omitempty" json:"limit,omitempty"`
	Columns    []string                  `yaml:"columns,omitempty" json:"columns,omitempty"`
	OnRowClick string                    `yaml:"on_row_click,omitempty" json:"on_row_click,omitempty"`
}
type FormEntityListCondition struct {
	Field  string     `yaml:"field"`
	Op     string     `yaml:"op"`
	Source string     `yaml:"source,omitempty"`
	Value  *yaml.Node `yaml:"value,omitempty"`
}

func (c *FormEntityListCondition) UnmarshalYAML(n *yaml.Node) error {
	type plain FormEntityListCondition
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "field", "op", "source", "value":
		default:
			return fmt.Errorf("unknown list condition key %q", n.Content[i].Value)
		}
	}
	var p plain
	// yaml.Node pointers require decoding the node itself, including explicit null.
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "value" {
			copy := *n.Content[i+1]
			p.Value = &copy
		}
	}
	var wire struct{ Field, Op, Source string }
	if err := n.Decode(&wire); err != nil {
		return err
	}
	p.Field = wire.Field
	p.Op = wire.Op
	p.Source = wire.Source
	*c = FormEntityListCondition(p)
	return nil
}
func EntityListField(e *Entity, name string) *Field {
	if e == nil {
		return nil
	}
	var found *Field
	for i := range e.Fields {
		if strings.EqualFold(e.Fields[i].Name, name) {
			if found != nil {
				return nil
			}
			found = &e.Fields[i]
		}
	}
	return found
}
func EntityListSourceField(owner *Entity, form *FormModule, path string) *Field {
	parts := strings.Split(path, ".")
	if len(parts) != 2 {
		return nil
	}
	if strings.EqualFold(parts[0], "Объект") {
		return EntityListField(owner, parts[1])
	}
	if !strings.EqualFold(parts[0], "Форма") || form == nil {
		return nil
	}
	var found *Field
	for _, a := range form.Attributes {
		if a == nil || !strings.EqualFold(a.Name, parts[1]) {
			continue
		}
		if found != nil {
			return nil
		}
		f := Field{Name: a.Name}
		t := strings.ToLower(a.TypeRef)
		switch {
		case strings.HasPrefix(t, "string"):
			f.Type = FieldTypeString
		case strings.HasPrefix(t, "decimal"), t == "number":
			f.Type = FieldTypeNumber
		case t == "boolean", t == "bool":
			f.Type = FieldTypeBool
		case t == "date", t == "datetime":
			f.Type = FieldTypeDate
		case strings.HasPrefix(t, "catalogref."), strings.HasPrefix(t, "documentref."):
			f.RefEntity = strings.SplitN(a.TypeRef, ".", 2)[1]
			f.Type = FieldType("reference:" + f.RefEntity)
		default:
			return nil
		}
		found = &f
	}
	return found
}

// EntityListLiteral checks YAML scalar types without coercing false/zero to empty.
func EntityListLiteral(f *Field, n *yaml.Node) (any, error) {
	if f == nil || n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" || strings.TrimSpace(n.Value) == "" {
		return nil, fmt.Errorf("nonempty scalar literal required")
	}
	if f.RefEntity != "" {
		return nil, fmt.Errorf("reference literals are unsupported")
	}
	switch f.Type {
	case FieldTypeBool:
		if n.Tag == "!!bool" {
			var v bool
			err := n.Decode(&v)
			return v, err
		}
	case FieldTypeNumber:
		if n.Tag == "!!int" || n.Tag == "!!float" {
			v, err := decimal.NewFromString(n.Value)
			return v, err
		}
	case FieldTypeString:
		if n.Tag == "!!str" {
			return n.Value, nil
		}
	case FieldTypeDate:
		if n.Tag == "!!str" || n.Tag == "!!timestamp" {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
				if parsed, err := time.ParseInLocation(layout, n.Value, time.Local); err == nil && !parsed.IsZero() {
					return parsed, nil
				}
			}
			return nil, fmt.Errorf("invalid date literal")
		}
	}
	return nil, fmt.Errorf("literal incompatible with %s", f.Type)
}
func ValidateFormEntityLists(owner *Entity, form *FormModule, entities []*Entity) error {
	if form == nil {
		return nil
	}
	count := 0
	var problem error
	tables, err := FormTableDefinitions(form, owner.TableParts)
	if err != nil {
		return err
	}
	form.Walk(func(el *FormElement) bool {
		if el.Kind != FormElementEntityList {
			return true
		}
		count++
		if problem != nil {
			return false
		}
		fail := func(s string) { problem = fmt.Errorf("list %q: %s", el.Name, s) }
		if form.Kind != "object" && form.Kind != "" {
			fail("only object forms are supported")
			return false
		}
		if el.EntityList == nil {
			fail("specification required")
			return false
		}
		for _, field := range owner.Fields {
			if strings.EqualFold(field.Name, "_entity_list_state") {
				fail("reserved list transport field name")
				return false
			}
		}
		for _, attr := range form.Attributes {
			if attr != nil && strings.EqualFold(attr.Name, "_entity_list_state") {
				fail("reserved list transport attribute name")
				return false
			}
		}
		sp := el.EntityList
		if el.ID == "" || el.Name == "" {
			fail("stable id and name required")
			return false
		}
		form.Walk(func(other *FormElement) bool {
			if other != el && (strings.EqualFold(other.ID, el.ID) || strings.EqualFold(other.Name, el.Name)) {
				fail("ambiguous id/name")
			}
			return true
		})
		for _, t := range tables {
			if strings.EqualFold(t.Name, el.Name) || strings.EqualFold(t.Name, el.ID) {
				fail("collision with form table")
			}
		}
		var target *Entity
		for _, e := range entities {
			if strings.EqualFold(e.Name, sp.Entity) {
				if target != nil {
					fail("ambiguous entity")
					return false
				}
				target = e
			}
		}
		if target == nil || (target.Kind != KindCatalog && target.Kind != KindDocument) {
			fail("document or catalog required")
			return false
		}
		if sp.Limit < 0 || sp.Limit > 100 {
			fail("limit must be 1..100")
			return false
		}
		if len(sp.Columns) == 0 || len(sp.Filter) > 8 || len(sp.OrderBy) > 4 {
			fail("columns required, maximum 8 filters and 4 order keys")
			return false
		}
		if sp.OnRowClick != "" && sp.OnRowClick != "нет" && sp.OnRowClick != "открыть" {
			fail("invalid on_row_click")
		}
		unique := func(names []string) bool {
			for i, n := range names {
				for _, prev := range names[:i] {
					if strings.EqualFold(prev, n) {
						return false
					}
				}
			}
			return true
		}
		if !unique(sp.Columns) {
			fail("duplicate columns")
		}
		for _, name := range sp.Columns {
			if EntityListField(target, name) == nil {
				fail("unknown or ambiguous column " + name)
			}
		}
		order := sp.OrderBy
		if len(order) == 0 {
			order = target.OrderBy
		}
		var orderNames []string
		for _, spec := range order {
			parts := strings.Fields(spec)
			if len(parts) < 1 || len(parts) > 2 || (len(parts) == 2 && !strings.EqualFold(parts[1], "asc") && !strings.EqualFold(parts[1], "desc")) {
				fail("invalid order")
				continue
			}
			orderNames = append(orderNames, parts[0])
			if strings.EqualFold(parts[0], "id") && len(parts) > 1 && !strings.EqualFold(parts[1], "asc") {
				fail("id tie breaker must be ascending")
			}
			if EntityListField(target, parts[0]) == nil && !strings.EqualFold(parts[0], "id") {
				fail("unknown order field")
			}
		}
		if !unique(orderNames) {
			fail("duplicate order keys")
		}
		var fields []string
		for _, c := range sp.Filter {
			fields = append(fields, c.Field)
			f := EntityListField(target, c.Field)
			if f == nil || c.Op != "eq" || (c.Source == "") == (c.Value == nil) {
				fail("invalid eq filter")
				continue
			}
			if c.Value != nil {
				if _, err := EntityListLiteral(f, c.Value); err != nil {
					fail(err.Error())
				}
				continue
			}
			parts := strings.Split(c.Source, ".")
			if len(parts) == 2 && strings.EqualFold(parts[0], "Форма") && EntityListField(owner, parts[1]) != nil {
				fail("ambiguous form/object source " + c.Source)
			}
			source := EntityListSourceField(owner, form, c.Source)
			if source == nil || !strings.EqualFold(source.RefEntity, f.RefEntity) || (source.RefEntity == "" && source.Type != f.Type) {
				fail("incompatible source " + c.Source)
			}
		}
		if !unique(fields) {
			fail("duplicate filters")
		}
		return true
	})
	if count > 8 {
		return fmt.Errorf("maximum eight entity lists per form")
	}
	return problem
}

// SourceControls exposes paths, never field/operator/order authority, to the browser.
func (s *FormEntityListSpec) SourceControls() map[string]string {
	out := map[string]string{}
	if s != nil {
		for _, c := range s.Filter {
			if c.Source != "" {
				parts := strings.Split(c.Source, ".")
				if len(parts) == 2 {
					out[c.Source] = parts[1]
				}
			}
		}
	}
	return out
}

// UnmarshalYAML keeps legacy permissiveness for old element kinds, while the
// new list grammar is closed so a typo cannot silently widen its query.
func (el *FormElement) UnmarshalYAML(n *yaml.Node) error {
	type plain FormElement
	var decoded plain
	if err := n.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Kind == FormElementEntityList {
		allowed := map[string]bool{}
		var keys func(reflect.Type)
		keys = func(t reflect.Type) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				tag := f.Tag.Get("yaml")
				if strings.Contains(tag, ",inline") {
					ft := f.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					keys(ft)
					continue
				}
				name := strings.Split(tag, ",")[0]
				if name != "" && name != "-" {
					allowed[name] = true
				}
			}
		}
		keys(reflect.TypeOf(FormElement{}))
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, v := n.Content[i].Value, n.Content[i+1]
			if !allowed[key] {
				return fmt.Errorf("unknown entity list key %q", key)
			}
			if key == "limit" {
				var limit int
				if v.Tag != "!!int" || v.Decode(&limit) != nil || limit < 1 || limit > 100 {
					return fmt.Errorf("list limit must be 1..100")
				}
			}
		}
	}
	if decoded.Kind != FormElementEntityList {
		decoded.EntityList = nil
	}
	*el = FormElement(decoded)
	return nil
}

func (c FormEntityListCondition) MarshalJSON() ([]byte, error) {
	out := map[string]any{"field": c.Field, "op": c.Op}
	if c.Source != "" {
		out["source"] = c.Source
	}
	if c.Value != nil {
		var v any
		if err := c.Value.Decode(&v); err != nil {
			return nil, err
		}
		out["value"] = v
	}
	return json.Marshal(out)
}
func (c *FormEntityListCondition) UnmarshalJSON(b []byte) error {
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return err
	}
	if len(n.Content) != 1 {
		return fmt.Errorf("list condition required")
	}
	return c.UnmarshalYAML(n.Content[0])
}
