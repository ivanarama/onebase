package metadata

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type rawField struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title"`
	Label  string            `yaml:"label"`
	Titles map[string]string `yaml:"titles"`
	Type   string            `yaml:"type"`
	// AllowInlineCreate — pointer to differ unset from explicit false. nil
	// means «keep context default» (true в шапке, false в ТЧ).
	AllowInlineCreate *bool `yaml:"allow_inline_create"`
}

type rawTablePart struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title"`
	Titles map[string]string `yaml:"titles"`
	Fields []rawField        `yaml:"fields"`
}

type rawNumerator struct {
	Prefix string `yaml:"prefix"`
	Length int    `yaml:"length"`
	Period string `yaml:"period"`
	Scope  string `yaml:"scope"`
}

type rawPredefined struct {
	Name   string                 `yaml:"name"`
	Fields map[string]interface{} `yaml:"fields"`
}

type rawActivity struct {
	Field          string `yaml:"field"`
	DefaultScope   string `yaml:"default_scope"`
	HideFromChoice *bool  `yaml:"hide_from_choice"`
}

type rawIndex struct {
	Fields []string `yaml:"fields"`
	Unique bool     `yaml:"unique"`
}

type rawEntity struct {
	Name          string            `yaml:"name"`
	Title         string            `yaml:"title"`
	Description   string            `yaml:"description"`
	Titles        map[string]string `yaml:"titles"`
	Fields        []rawField        `yaml:"fields"`
	TableParts    []rawTablePart    `yaml:"tableparts"`
	Indexes       []rawIndex        `yaml:"indexes"`
	Posting       bool              `yaml:"posting"`
	Numerator     *rawNumerator     `yaml:"numerator"`
	Predefined    []rawPredefined   `yaml:"predefined"`
	Hierarchical  bool              `yaml:"hierarchical"`
	HierarchyKind string            `yaml:"hierarchy_kind"`
	ListForm      []string          `yaml:"list_form"`
	ItemForm      []string          `yaml:"item_form"`
	BasedOn       []string          `yaml:"based_on"`
	Activity      *rawActivity      `yaml:"activity"`
	ListMode      string            `yaml:"list_mode"`
	TileView      *rawTileView      `yaml:"tile_view"`
}

type rawTileView struct {
	Image    string    `yaml:"image"`
	Title    string    `yaml:"title"`
	Subtitle string    `yaml:"subtitle"`
	Fields   *[]string `yaml:"fields"`
}

func LoadFile(path string, kind Kind) (*Entity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawEntity
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	e := &Entity{Name: raw.Name, Title: raw.Title, Description: raw.Description, Titles: raw.Titles, Kind: kind, Posting: raw.Posting, Hierarchical: raw.Hierarchical}
	if raw.Hierarchical {
		e.HierarchyKind = raw.HierarchyKind
		if e.HierarchyKind == "" {
			e.HierarchyKind = "folders_and_items"
		}
	}
	e.ListForm = raw.ListForm
	e.ItemForm = raw.ItemForm
	e.BasedOn = raw.BasedOn
	if raw.Activity != nil {
		hide := true
		if raw.Activity.HideFromChoice != nil {
			hide = *raw.Activity.HideFromChoice
		}
		scope := strings.ToLower(strings.TrimSpace(raw.Activity.DefaultScope))
		if scope == "" {
			scope = ActivityScopeActive
		}
		e.Activity = &ActivityConfig{
			Field:          strings.TrimSpace(raw.Activity.Field),
			DefaultScope:   scope,
			HideFromChoice: hide,
		}
	}
	// Нормализуем: «Feed», «FEED», « feed » → «feed». Иначе resolveListMode
	// (сравнение с точной строкой "feed") молча откатывался бы на постранично.
	e.ListMode = strings.ToLower(strings.TrimSpace(raw.ListMode))
	if raw.TileView != nil {
		e.TileView = &TileView{
			Image:    strings.TrimSpace(raw.TileView.Image),
			Title:    strings.TrimSpace(raw.TileView.Title),
			Subtitle: strings.TrimSpace(raw.TileView.Subtitle),
		}
		if raw.TileView.Fields != nil {
			e.TileView.Fields = trimStringList(*raw.TileView.Fields)
			e.TileView.FieldsSet = true
		}
	}
	if raw.Numerator != nil {
		n := &Numerator{
			Prefix: raw.Numerator.Prefix,
			Length: raw.Numerator.Length,
			Period: raw.Numerator.Period,
			Scope:  raw.Numerator.Scope,
		}
		if n.Length <= 0 {
			n.Length = 8
		}
		if n.Period == "" {
			n.Period = "year"
		}
		e.Numerator = n
	}
	for _, rf := range raw.Fields {
		e.Fields = append(e.Fields, parseField(rf))
	}
	for _, ri := range raw.Indexes {
		idx := IndexSpec{Fields: trimStringList(ri.Fields), Unique: ri.Unique}
		if len(idx.Fields) > 0 {
			e.Indexes = append(e.Indexes, idx)
		}
	}
	for _, rtp := range raw.TableParts {
		tp := TablePart{Name: rtp.Name, Title: rtp.Title, Titles: rtp.Titles}
		for _, rf := range rtp.Fields {
			tp.Fields = append(tp.Fields, parseField(rf))
		}
		e.TableParts = append(e.TableParts, tp)
	}
	for _, rp := range raw.Predefined {
		fields := make(map[string]any, len(rp.Fields))
		for k, v := range rp.Fields {
			fields[k] = v
		}
		e.Predefined = append(e.Predefined, &PredefinedItem{Name: rp.Name, Fields: fields})
	}
	return e, nil
}

func trimStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type rawRegister struct {
	Name       string            `yaml:"name"`
	Title      string            `yaml:"title"`
	Titles     map[string]string `yaml:"titles"`
	Kind       string            `yaml:"kind"` // balance (по умолчанию) | turnover/обороты
	Dimensions []rawField        `yaml:"dimensions"`
	Resources  []rawField        `yaml:"resources"`
	Attributes []rawField        `yaml:"attributes"`
}

// normalizeRegisterKind приводит вид регистра к каноническому значению.
// Принимает русские и английские синонимы; всё неизвестное — балансовый.
func normalizeRegisterKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "turnover", "обороты", "оборотный", "оборотов":
		return RegisterKindTurnover
	default:
		return RegisterKindBalance
	}
}

func LoadRegisterFile(path string) (*Register, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawRegister
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	reg := &Register{Name: raw.Name, Title: raw.Title, Titles: raw.Titles, Kind: normalizeRegisterKind(raw.Kind)}
	for _, rf := range raw.Dimensions {
		reg.Dimensions = append(reg.Dimensions, parseField(rf))
	}
	for _, rf := range raw.Resources {
		reg.Resources = append(reg.Resources, parseField(rf))
	}
	for _, rf := range raw.Attributes {
		reg.Attributes = append(reg.Attributes, parseField(rf))
	}
	return reg, nil
}

type rawInfoRegister struct {
	Name       string            `yaml:"name"`
	Title      string            `yaml:"title"`
	Titles     map[string]string `yaml:"titles"`
	Periodic   bool              `yaml:"periodic"`
	Dimensions []rawField        `yaml:"dimensions"`
	Resources  []rawField        `yaml:"resources"`
}

func LoadInfoRegisterFile(path string) (*InfoRegister, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawInfoRegister
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	ir := &InfoRegister{Name: raw.Name, Title: raw.Title, Titles: raw.Titles, Periodic: raw.Periodic}
	for _, rf := range raw.Dimensions {
		ir.Dimensions = append(ir.Dimensions, parseField(rf))
	}
	for _, rf := range raw.Resources {
		ir.Resources = append(ir.Resources, parseField(rf))
	}
	return ir, nil
}

// rawEnumValue принимает значение перечисления как скаляр (старый формат
// "values: [A, B]") ИЛИ как маппинг {name, titles} (новый, с переводами).
type rawEnumValue struct {
	Name   string
	Titles map[string]string
}

func (v *rawEnumValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		v.Name = node.Value
		return nil
	}
	var s struct {
		Name   string            `yaml:"name"`
		Titles map[string]string `yaml:"titles"`
	}
	if err := node.Decode(&s); err != nil {
		return err
	}
	v.Name, v.Titles = s.Name, s.Titles
	return nil
}

type rawEnum struct {
	Name   string         `yaml:"name"`
	Values []rawEnumValue `yaml:"values"`
}

func LoadEnumFile(path string) (*Enum, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawEnum
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	e := &Enum{Name: raw.Name}
	for _, rv := range raw.Values {
		e.Values = append(e.Values, rv.Name)
		if len(rv.Titles) > 0 {
			if e.ValueTitles == nil {
				e.ValueTitles = map[string]map[string]string{}
			}
			e.ValueTitles[rv.Name] = rv.Titles
		}
	}
	return e, nil
}

type rawConstant struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	Default string            `yaml:"default"`
	Label   string            `yaml:"label"`
	Labels  map[string]string `yaml:"labels"`
}

type rawConstantsFile struct {
	Constants []rawConstant `yaml:"constants"`
}

func LoadConstantsFile(path string) ([]*Constant, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawConstantsFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var result []*Constant
	for _, rc := range raw.Constants {
		c := &Constant{
			Name:    rc.Name,
			Type:    FieldType(rc.Type),
			Default: rc.Default,
			Label:   rc.Label,
			Labels:  rc.Labels,
		}
		if strings.HasPrefix(rc.Type, "reference:") {
			c.RefEntity = strings.TrimPrefix(rc.Type, "reference:")
		} else if strings.HasPrefix(rc.Type, "enum:") {
			c.EnumName = strings.TrimPrefix(rc.Type, "enum:")
		} else if l, s, ok := parseNumberSpec(rc.Type); ok {
			c.Type = FieldTypeNumber
			c.Length = l
			c.Scale = s
		}
		result = append(result, c)
	}
	return result, nil
}

func parseField(rf rawField) Field {
	title := rf.Title
	if title == "" {
		title = rf.Label
	}
	f := Field{Name: rf.Name, Title: title, Titles: rf.Titles, Type: FieldType(rf.Type), AllowInlineCreate: rf.AllowInlineCreate}
	if strings.HasPrefix(rf.Type, "reference:") {
		f.RefEntity = strings.TrimPrefix(rf.Type, "reference:")
	} else if strings.HasPrefix(rf.Type, "enum:") {
		f.EnumName = strings.TrimPrefix(rf.Type, "enum:")
	} else if l, s, ok := parseNumberSpec(rf.Type); ok {
		// "number(10,2)" / "decimal(15,2)" / "decimal(15)" → number с разрядностью.
		f.Type = FieldTypeNumber
		f.Length = l
		f.Scale = s
	}
	return f
}

// parseNumberSpec разбирает инлайн-нотацию разрядности числового типа:
// "number(10,2)" → 10,2; "decimal(15,2)" → 15,2; "decimal(15)" → 15,0.
// Возвращает ok=false для всех остальных строк (включая голый "number").
// Семантика как в SQL NUMERIC(precision, scale) и в 1С (Длина, Точность).
func parseNumberSpec(typ string) (length, scale int, ok bool) {
	t := strings.TrimSpace(typ)
	idx := strings.Index(t, "(")
	if idx <= 0 || !strings.HasSuffix(t, ")") {
		return 0, 0, false
	}
	base := strings.TrimSpace(t[:idx])
	if base != "number" && base != "decimal" {
		return 0, 0, false
	}
	params := t[idx+1 : len(t)-1]
	parts := strings.Split(params, ",")
	if len(parts) >= 1 {
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &length); err != nil {
			return 0, 0, false
		}
	}
	if len(parts) >= 2 {
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &scale)
	}
	return length, scale, true
}
