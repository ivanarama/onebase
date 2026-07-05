package ui

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report/compose"
)

const (
	formRowClassKey  = "_form_row_class"
	formCellClassKey = "_form_cell_classes"
)

func applyManagedFormConditionalStyles(form *metadata.FormModule, rowsByName map[string][]map[string]any, header map[string]any, ev compose.Evaluator) []string {
	if form == nil || len(form.Conditional) == 0 || len(rowsByName) == 0 || ev == nil {
		clearFormConditionalStyles(rowsByName)
		return nil
	}
	clearFormConditionalStyles(rowsByName)
	aliases := formConditionalTargetAliases(form)
	wc := &formWarnCollector{}
	applied := map[string]map[int]map[string]bool{}
	for _, rule := range form.Conditional {
		style := cssOfForm(rule.Style)
		if style == "" {
			continue
		}
		targetNames, ok := formConditionalRuleTargets(rule.Target, aliases, rowsByName)
		if !ok {
			wc.add(fmt.Sprintf("условное оформление формы «%s»: цель «%s» не найдена", rule.When, rule.Target))
			continue
		}
		for _, targetName := range targetNames {
			rows := rowsByName[targetName]
			for i, row := range rows {
				if row == nil {
					continue
				}
				fieldKey := formConditionalAppliedField(rule.Field)
				if formConditionalAlreadyApplied(applied, targetName, i, fieldKey) {
					continue
				}
				evalRow := formConditionalEvalRow(header, row, i)
				matches, err := ev.EvalBool(rule.When, compose.Row(evalRow))
				if err != nil {
					wc.add(fmt.Sprintf("условное оформление формы «%s»: %v", rule.When, err))
					continue
				}
				if !matches {
					continue
				}
				if strings.TrimSpace(rule.Field) == "" {
					addFormRowClass(row, formRowStyleClass(style))
					markFormConditionalApplied(applied, targetName, i, fieldKey)
					continue
				}
				field := formConditionalFieldName(row, rule.Field)
				addFormCellClass(row, field, formCellStyleClass(style))
				markFormConditionalApplied(applied, targetName, i, fieldKey)
			}
		}
	}
	return wc.msgs
}

func formConditionalAppliedField(field string) string {
	return strings.ToLower(strings.TrimSpace(field))
}

func formConditionalAlreadyApplied(applied map[string]map[int]map[string]bool, target string, row int, field string) bool {
	return applied[target] != nil && applied[target][row] != nil && applied[target][row][field]
}

func markFormConditionalApplied(applied map[string]map[int]map[string]bool, target string, row int, field string) {
	if applied[target] == nil {
		applied[target] = map[int]map[string]bool{}
	}
	if applied[target][row] == nil {
		applied[target][row] = map[string]bool{}
	}
	applied[target][row][field] = true
}

func clearFormConditionalStyles(rowsByName map[string][]map[string]any) {
	for _, rows := range rowsByName {
		for _, row := range rows {
			delete(row, formRowClassKey)
			delete(row, formCellClassKey)
		}
	}
}

func cssOfForm(s metadata.FormCellStyle) string {
	return cssStyle(s.Color, s.Background, s.Bold, s.Italic)
}

func formConditionalCSS(form *metadata.FormModule) string {
	if form == nil || len(form.Conditional) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var styles []string
	for _, rule := range form.Conditional {
		style := cssOfForm(rule.Style)
		if style == "" || seen[style] {
			continue
		}
		seen[style] = true
		styles = append(styles, style)
	}
	sort.Strings(styles)
	var b strings.Builder
	for _, style := range styles {
		imp := cssImportant(style)
		if imp == "" {
			continue
		}
		rowClass := formRowStyleClass(style)
		cellClass := formCellStyleClass(style)
		fmt.Fprintf(&b, ".tp-table tbody tr.%s>td,.tp-table tbody tr.%s:hover>td,.tp-table tbody tr.%s:nth-child(even)>td,.tp-table tbody tr.%s>td input,.tp-table tbody tr.%s>td select,.ob-grid .slick-row.%s .slick-cell{%s}\n",
			rowClass, rowClass, rowClass, rowClass, rowClass, rowClass, imp)
		fmt.Fprintf(&b, ".tp-table tbody td.%s,.tp-table tbody tr:hover td.%s,.tp-table tbody tr:nth-child(even) td.%s,.tp-table tbody td.%s input,.tp-table tbody td.%s select,.ob-grid .slick-row .slick-cell.%s{%s}\n",
			cellClass, cellClass, cellClass, cellClass, cellClass, cellClass, imp)
	}
	return b.String()
}

func cssImportant(style string) string {
	var parts []string
	for _, p := range strings.Split(style, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "!important") {
			p += "!important"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ";")
}

func formRowStyleClass(style string) string {
	return formStyleClass("ob-cfr", style)
}

func formCellStyleClass(style string) string {
	return formStyleClass("ob-cfc", style)
}

func formStyleClass(prefix, style string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(style))
	return fmt.Sprintf("%s-%08x", prefix, h.Sum32())
}

func formConditionalTargetAliases(form *metadata.FormModule) map[string]string {
	aliases := map[string]string{}
	add := func(alias, name string) {
		alias = strings.TrimSpace(alias)
		name = strings.TrimSpace(name)
		if alias == "" || name == "" {
			return
		}
		aliases[strings.ToLower(alias)] = name
	}
	if form == nil {
		return aliases
	}
	for _, attr := range form.Attributes {
		if attr != nil && strings.EqualFold(attr.TypeRef, "ValueTable") {
			add(attr.Name, attr.Name)
		}
	}
	form.Walk(func(el *metadata.FormElement) bool {
		if el == nil || el.Kind != metadata.FormElementTablePart {
			return true
		}
		name := formDataPathFieldName(el.DataPath)
		if name == "" {
			name = el.TablePart
		}
		if name == "" {
			name = el.Name
		}
		add(name, name)
		add(el.Name, name)
		return true
	})
	return aliases
}

func formConditionalRuleTargets(target string, aliases map[string]string, rowsByName map[string][]map[string]any) ([]string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		names := make([]string, 0, len(rowsByName))
		for name := range rowsByName {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, true
	}
	if canonical := aliases[strings.ToLower(target)]; canonical != "" {
		if actual := actualFormRowsName(rowsByName, canonical); actual != "" {
			return []string{actual}, true
		}
		return []string{canonical}, true
	}
	if actual := actualFormRowsName(rowsByName, target); actual != "" {
		return []string{actual}, true
	}
	return nil, false
}

func actualFormRowsName(rowsByName map[string][]map[string]any, name string) string {
	if _, ok := rowsByName[name]; ok {
		return name
	}
	for k := range rowsByName {
		if strings.EqualFold(k, name) {
			return k
		}
	}
	return ""
}

func formConditionalEvalRow(header map[string]any, row map[string]any, idx int) map[string]any {
	out := make(map[string]any, len(header)+len(row)+4)
	for k, v := range header {
		out[k] = v
	}
	for k, v := range row {
		if strings.HasPrefix(k, "_form_") {
			continue
		}
		out[k] = v
	}
	out["НомерСтроки"] = idx + 1
	out["ИндексСтроки"] = idx
	out["RowNumber"] = idx + 1
	out["RowIndex"] = idx
	return out
}

func formConditionalFieldName(row map[string]any, field string) string {
	field = strings.TrimSpace(field)
	for k := range row {
		if strings.EqualFold(k, field) {
			return k
		}
	}
	return field
}

func addFormRowClass(row map[string]any, class string) {
	if class == "" {
		return
	}
	row[formRowClassKey] = joinClasses(formRowClass(row), class)
}

func addFormCellClass(row map[string]any, field, class string) {
	if field == "" || class == "" {
		return
	}
	cellClasses, _ := row[formCellClassKey].(map[string]string)
	if cellClasses == nil {
		cellClasses = map[string]string{}
		row[formCellClassKey] = cellClasses
	}
	cellClasses[field] = joinClasses(cellClasses[field], class)
}

func joinClasses(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" || strings.Contains(" "+a+" ", " "+b+" ") {
		return a
	}
	return a + " " + b
}

func formRowClass(row map[string]any) string {
	if row == nil {
		return ""
	}
	s, _ := row[formRowClassKey].(string)
	return s
}

func formCellClass(row map[string]any, field string) string {
	if row == nil {
		return ""
	}
	if cellClasses, _ := row[formCellClassKey].(map[string]string); cellClasses != nil {
		if s := cellClasses[field]; s != "" {
			return s
		}
		for k, s := range cellClasses {
			if strings.EqualFold(k, field) {
				return s
			}
		}
	}
	if cellClasses, _ := row[formCellClassKey].(map[string]any); cellClasses != nil {
		if s := fmt.Sprintf("%v", cellClasses[field]); s != "" && s != "<nil>" {
			return s
		}
		for k, v := range cellClasses {
			if strings.EqualFold(k, field) {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return ""
}

func formDataPathFieldName(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

type formWarnCollector struct {
	seen map[string]bool
	msgs []string
}

func (w *formWarnCollector) add(msg string) {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[msg] {
		return
	}
	w.seen[msg] = true
	w.msgs = append(w.msgs, msg)
}
