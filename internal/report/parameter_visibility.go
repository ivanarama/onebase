package report

import "fmt"

type ParamVisibility struct {
	Param  string `yaml:"param" json:"param"`
	Equals *bool  `yaml:"equals" json:"equals"`
}

// ParamBoolValue shares the report parser's semantics, including the explicit
// string "false" (which is truthy if passed directly to an HTML template).
func ParamBoolValue(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	if value == nil {
		return false
	}
	v, err := ParseParamValue(fmt.Sprint(value), Param{Type: "bool"}, ParamParseForm)
	result, _ := v.(bool)
	return err == nil && result
}

func ParamVisible(params []Param, param Param, values map[string]any) bool {
	if param.VisibleWhen == nil {
		return true
	}
	when := param.VisibleWhen
	if when.Equals == nil {
		return false
	}
	for _, source := range params {
		if source.Name == when.Param && source.Type == "bool" {
			return ParamBoolValue(values[source.Name]) == *when.Equals
		}
	}
	return false
}

func ValidateParamVisibility(rep *Report) error {
	byName := map[string][]Param{}
	for _, p := range rep.Params {
		byName[p.Name] = append(byName[p.Name], p)
	}
	for _, p := range rep.Params {
		when := p.VisibleWhen
		if when == nil {
			continue
		}
		sources := byName[when.Param]
		if len(sources) != 1 || sources[0].Type != "bool" || when.Equals == nil || p.Name == when.Param {
			return fmt.Errorf("parameter %s: visible_when requires another unique bool parameter and an explicit equals value", p.Name)
		}
	}
	return nil
}
