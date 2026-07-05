package ui

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report/compose"
)

const (
	journalRowStyleKey  = "_journal_style"
	journalCellStyleKey = "_journal_cell_styles"
)

func applyJournalConditionalStyles(rows []map[string]any, rules []metadata.JournalCondRule, ev compose.Evaluator) []string {
	if len(rows) == 0 || len(rules) == 0 || ev == nil {
		return nil
	}
	targets := journalConditionTargets(rules)
	wc := &journalWarnCollector{}
	for _, row := range rows {
		r := alignJournalConditionRow(row, targets)
		applied := map[string]bool{}
		for _, rule := range rules {
			field := journalStyleField(rule.Field)
			if applied[field] {
				continue
			}
			style := cssOfJournal(rule.Style)
			if style == "" {
				continue
			}
			ok, err := ev.EvalBool(rule.When, compose.Row(r))
			if err != nil {
				wc.add(fmt.Sprintf("условие оформления «%s»: %v", rule.When, err))
				continue
			}
			if !ok {
				continue
			}
			applied[field] = true
			if field == "" {
				row[journalRowStyleKey] = joinStyles(journalRowStyle(row), style)
				continue
			}
			cellStyles, _ := row[journalCellStyleKey].(map[string]string)
			if cellStyles == nil {
				cellStyles = map[string]string{}
				row[journalCellStyleKey] = cellStyles
			}
			cellStyles[field] = joinStyles(cellStyles[field], style)
		}
	}
	return wc.msgs
}

func cssOfJournal(s metadata.JournalCellStyle) string {
	return cssStyle(s.Color, s.Background, s.Bold, s.Italic)
}

func journalConditionTargets(rules []metadata.JournalCondRule) map[string]bool {
	targets := map[string]bool{}
	for _, r := range rules {
		if r.Field != "" {
			targets[r.Field] = true
		}
	}
	return targets
}

func alignJournalConditionRow(row map[string]any, targets map[string]bool) map[string]any {
	_, hasDocKind := row["_doc_kind"]
	if len(targets) == 0 && !hasDocKind {
		return row
	}
	byLower := make(map[string]string, len(targets))
	for t := range targets {
		byLower[strings.ToLower(t)] = t
	}
	out := make(map[string]any, len(row)+len(targets)+2)
	for k, v := range row {
		out[k] = v
		if t, ok := byLower[strings.ToLower(k)]; ok && t != k {
			out[t] = v
		}
	}
	if hasDocKind {
		out["Документ"] = row["_doc_kind"]
		out["document"] = row["_doc_kind"]
	}
	return out
}

func journalStyleField(field string) string {
	if field == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "_doc_kind", "документ", "document":
		return "_doc_kind"
	default:
		return field
	}
}

func journalRowStyle(row map[string]any) string {
	if row == nil {
		return ""
	}
	s, _ := row[journalRowStyleKey].(string)
	return s
}

func journalCellStyle(row map[string]any, field string) string {
	if row == nil {
		return ""
	}
	cellStyles, _ := row[journalCellStyleKey].(map[string]string)
	if cellStyles == nil {
		return ""
	}
	if s := cellStyles[field]; s != "" {
		return s
	}
	for k, s := range cellStyles {
		if strings.EqualFold(k, field) {
			return s
		}
	}
	return ""
}

type journalWarnCollector struct {
	seen map[string]bool
	msgs []string
}

func (w *journalWarnCollector) add(msg string) {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	if w.seen[msg] {
		return
	}
	w.seen[msg] = true
	w.msgs = append(w.msgs, msg)
}
