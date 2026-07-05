package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

type formConditionalRuntime struct {
	form  *metadata.FormModule
	rules []metadata.FormCondRule
}

func newFormConditionalRuntime(form *metadata.FormModule) *formConditionalRuntime {
	rt := &formConditionalRuntime{form: form}
	if form != nil && len(form.Conditional) > 0 {
		rt.rules = append([]metadata.FormCondRule(nil), form.Conditional...)
	}
	return rt
}

func (rt *formConditionalRuntime) builtins() map[string]any {
	add := interpreter.BuiltinFunc(rt.addFormattingRule)
	clear := interpreter.BuiltinFunc(rt.clearFormatting)
	return map[string]any{
		"ДобавитьПравилоОформления": add,
		"AddFormattingRule":  add,
		"ОчиститьОформление": clear,
		"ClearFormatting":    clear,
	}
}

func (rt *formConditionalRuntime) addFormattingRule(args []any, _ string, _ int) (any, error) {
	if rt == nil {
		return nil, nil
	}
	if len(args) < 3 {
		return nil, fmt.Errorf("ДобавитьПравилоОформления: ожидается Цель, Условие и стиль")
	}
	rule := metadata.FormCondRule{
		Target: formConditionalArgString(args[0]),
		When:   strings.TrimSpace(formConditionalArgString(args[1])),
	}
	if rule.When == "" {
		return nil, fmt.Errorf("ДобавитьПравилоОформления: условие не задано")
	}

	var style metadata.FormCellStyle
	styleSet := false
	if st, field, ok := formConditionalStyleFromArg(args[2]); ok {
		style = mergeFormConditionalStyle(style, st)
		styleSet = styleSet || !formConditionalStyleZero(st)
		if field != "" {
			rule.Field = field
		}
	} else if len(args) == 3 {
		style.Background = formConditionalArgString(args[2])
		styleSet = true
	} else {
		rule.Field = formConditionalArgString(args[2])
	}

	if len(args) >= 4 {
		if st, field, ok := formConditionalStyleFromArg(args[3]); ok {
			style = mergeFormConditionalStyle(style, st)
			styleSet = styleSet || !formConditionalStyleZero(st)
			if rule.Field == "" && field != "" {
				rule.Field = field
			}
		} else {
			style.Background = formConditionalArgString(args[3])
			styleSet = true
		}
	}
	if len(args) >= 5 {
		style.Color = formConditionalArgString(args[4])
		styleSet = true
	}
	if len(args) >= 6 {
		style.Bold = formConditionalArgBool(args[5])
		styleSet = true
	}
	if len(args) >= 7 {
		style.Italic = formConditionalArgBool(args[6])
		styleSet = true
	}

	if !styleSet || cssOfForm(style) == "" {
		return nil, fmt.Errorf("ДобавитьПравилоОформления: стиль не задан или отброшен фильтром безопасности")
	}
	rule.Style = style
	rt.rules = append(rt.rules, rule)
	return nil, nil
}

func (rt *formConditionalRuntime) clearFormatting(args []any, _ string, _ int) (any, error) {
	if rt == nil {
		return nil, nil
	}
	if len(args) == 0 || strings.TrimSpace(formConditionalArgString(args[0])) == "" {
		rt.rules = nil
		return nil, nil
	}
	target := strings.TrimSpace(formConditionalArgString(args[0]))
	aliases := formConditionalTargetAliases(rt.form)
	kept := rt.rules[:0]
	for _, rule := range rt.rules {
		if formConditionalSameTarget(rule.Target, target, aliases) {
			continue
		}
		kept = append(kept, rule)
	}
	rt.rules = kept
	return nil, nil
}

func formConditionalSameTarget(a, b string, aliases map[string]string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if strings.EqualFold(a, b) {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(formConditionalCanonicalTarget(a, aliases), formConditionalCanonicalTarget(b, aliases))
}

func formConditionalCanonicalTarget(target string, aliases map[string]string) string {
	target = strings.TrimSpace(target)
	if canonical := aliases[strings.ToLower(target)]; canonical != "" {
		return canonical
	}
	return target
}

func mergeFormConditionalStyle(dst, src metadata.FormCellStyle) metadata.FormCellStyle {
	if src.Color != "" {
		dst.Color = src.Color
	}
	if src.Background != "" {
		dst.Background = src.Background
	}
	if src.Bold {
		dst.Bold = true
	}
	if src.Italic {
		dst.Italic = true
	}
	return dst
}

func formConditionalStyleZero(s metadata.FormCellStyle) bool {
	return s.Color == "" && s.Background == "" && !s.Bold && !s.Italic
}

func formConditionalArgString(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func formConditionalArgBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case float32:
		return t != 0
	case string:
		return formConditionalStringBool(t)
	default:
		if n, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprintf("%v", v)), 64); err == nil {
			return n != 0
		}
		return formConditionalStringBool(fmt.Sprintf("%v", v))
	}
}

func formConditionalStringBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "истина", "1", "да", "yes", "y":
		return true
	}
	return false
}

func formConditionalStyleFromArg(v any) (metadata.FormCellStyle, string, bool) {
	switch t := v.(type) {
	case nil:
		return metadata.FormCellStyle{}, "", false
	case map[string]any:
		return formConditionalStyleFromMap(func(k string) (any, bool) {
			for mk, mv := range t {
				if formConditionalStyleKeyMatches(mk, k) {
					return mv, true
				}
			}
			return nil, false
		})
	case map[string]string:
		return formConditionalStyleFromMap(func(k string) (any, bool) {
			for mk, mv := range t {
				if formConditionalStyleKeyMatches(mk, k) {
					return mv, true
				}
			}
			return nil, false
		})
	case interface{ Get(string) any }:
		return formConditionalStyleFromMap(func(k string) (any, bool) {
			for _, key := range formConditionalStyleLookupKeys(k) {
				if value := t.Get(key); value != nil {
					return value, true
				}
			}
			return nil, false
		})
	case interface{ Get(any) any }:
		return formConditionalStyleFromMap(func(k string) (any, bool) {
			for _, key := range formConditionalStyleLookupKeys(k) {
				if value := t.Get(key); value != nil {
					return value, true
				}
			}
			return nil, false
		})
	default:
		return metadata.FormCellStyle{}, "", false
	}
}

func formConditionalStyleFromMap(get func(string) (any, bool)) (metadata.FormCellStyle, string, bool) {
	var style metadata.FormCellStyle
	var field string
	ok := false
	if v, found := get("background"); found {
		style.Background = formConditionalArgString(v)
		ok = true
	}
	if v, found := get("color"); found {
		style.Color = formConditionalArgString(v)
		ok = true
	}
	if v, found := get("bold"); found {
		style.Bold = formConditionalArgBool(v)
		ok = true
	}
	if v, found := get("italic"); found {
		style.Italic = formConditionalArgBool(v)
		ok = true
	}
	if v, found := get("field"); found {
		field = formConditionalArgString(v)
		ok = true
	}
	return style, field, ok
}

func formConditionalStyleKey(key string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
}

func formConditionalStyleKeyMatches(key, canonical string) bool {
	key = formConditionalStyleKey(key)
	for _, alias := range formConditionalStyleLookupKeys(canonical) {
		if key == formConditionalStyleKey(alias) {
			return true
		}
	}
	return false
}

func formConditionalStyleLookupKeys(key string) []string {
	switch key {
	case "background":
		return []string{"background", "Background", "Фон", "цветфона", "ЦветФона", "backgroundcolor", "BackgroundColor", "bg"}
	case "color":
		return []string{"color", "Color", "Цвет", "цветтекста", "ЦветТекста", "textcolor", "TextColor", "foreground", "Foreground"}
	case "bold":
		return []string{"bold", "Bold", "Жирный", "жирный"}
	case "italic":
		return []string{"italic", "Italic", "Курсив", "курсив"}
	case "field":
		return []string{"field", "Field", "Поле", "поле", "Колонка", "колонка", "column", "Column"}
	default:
		return []string{key}
	}
}
