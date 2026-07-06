package metadata

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Journal struct {
	Name        string
	Title       string
	Titles      map[string]string
	Documents   []string
	Columns     []JournalColumn
	Filters     []JournalFilter
	Conditional []JournalCondRule
}

// DisplayName возвращает заголовок журнала документов с учётом языка.
func (j *Journal) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := j.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if j.Title != "" {
		return j.Title
	}
	return j.Name
}

type JournalColumn struct {
	Field    string
	Label    string
	Labels   map[string]string
	Fallback []string
	// Map — явное соответствие docTypeName → fieldName в этом документе.
	// лучшая альтернатива fallback, потому что:
	//   1. Explicit — видно для какого документа какое поле.
	//   2. Не зависит от порядка обхода fallback (raw COALESCE мог давать
	//      нежелательные совпадения если в документе два совпадающих поля).
	//   3. UI может показывать tooltip «в документе X это поле называется Y».
	// Если Map[doc] есть — используется он, fallback не применяется. Если
	// не задан — работает старый fallback-механизм (back compat).
	Map    map[string]string
	Format string // "date" | "number" | "" (auto)
}

// DisplayLabel возвращает подпись колонки журнала с учётом языка.
func (c JournalColumn) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := c.Labels[lang]; ok && v != "" {
			return v
		}
	}
	if c.Label != "" {
		return c.Label
	}
	return c.Field
}

type JournalFilter struct {
	Field  string
	Label  string
	Labels map[string]string
	Type   string // "date_range" | "reference:X" | "string"
}

// DisplayLabel возвращает подпись фильтра журнала с учётом языка.
func (f JournalFilter) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := f.Labels[lang]; ok && v != "" {
			return v
		}
	}
	if f.Label != "" {
		return f.Label
	}
	return f.Field
}

type JournalCondRule struct {
	When  string
	Field string // "" = вся строка
	Style JournalCellStyle
}

type JournalCellStyle struct {
	Color      string
	Background string
	Bold       bool
	Italic     bool
}

type rawJournal struct {
	Name      string            `yaml:"name"`
	Title     string            `yaml:"title"`
	Titles    map[string]string `yaml:"titles"`
	Documents []string          `yaml:"documents"`
	Columns   []struct {
		Field    string            `yaml:"field"`
		Label    string            `yaml:"label"`
		Labels   map[string]string `yaml:"labels"`
		Fallback []string          `yaml:"fallback"`
		Map      map[string]string `yaml:"map"`
		Format   string            `yaml:"format"`
	} `yaml:"columns"`
	Filters []struct {
		Field  string            `yaml:"field"`
		Label  string            `yaml:"label"`
		Labels map[string]string `yaml:"labels"`
		Type   string            `yaml:"type"`
	} `yaml:"filters"`
	Conditional           []rawJournalCondRule `yaml:"conditional"`
	ConditionalFormatting []rawJournalCondRule `yaml:"conditional_formatting"`
}

type rawJournalCondRule struct {
	When  string           `yaml:"when"`
	Field string           `yaml:"field"`
	Style JournalCellStyle `yaml:"style"`
	Then  JournalCellStyle `yaml:"then"`
}

func LoadJournalFile(path string) (*Journal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawJournal
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Name == "" {
		raw.Name = strings.TrimSuffix(filepath.Base(path), ".yaml")
	}
	if raw.Title == "" {
		raw.Title = raw.Name
	}
	j := &Journal{
		Name:        raw.Name,
		Title:       raw.Title,
		Titles:      raw.Titles,
		Documents:   raw.Documents,
		Conditional: rawJournalConditional(raw),
	}
	for _, rc := range raw.Columns {
		label := rc.Label
		if label == "" {
			label = rc.Field
		}
		j.Columns = append(j.Columns, JournalColumn{
			Field:    rc.Field,
			Label:    label,
			Labels:   rc.Labels,
			Fallback: rc.Fallback,
			Map:      rc.Map,
			Format:   rc.Format,
		})
	}
	for _, rf := range raw.Filters {
		ft := rf.Type
		if ft == "" {
			ft = "string"
		}
		j.Filters = append(j.Filters, JournalFilter{Field: rf.Field, Label: rf.Label, Labels: rf.Labels, Type: ft})
	}
	return j, nil
}

func rawJournalConditional(raw rawJournal) []JournalCondRule {
	rawRules := append([]rawJournalCondRule{}, raw.Conditional...)
	rawRules = append(rawRules, raw.ConditionalFormatting...)
	out := make([]JournalCondRule, 0, len(rawRules))
	for _, rr := range rawRules {
		style := rr.Style
		if journalStyleZero(style) {
			style = rr.Then
		}
		out = append(out, JournalCondRule{When: rr.When, Field: rr.Field, Style: style})
	}
	return out
}

func journalStyleZero(s JournalCellStyle) bool {
	return s.Color == "" && s.Background == "" && !s.Bold && !s.Italic
}

func LoadJournalDir(dir string) ([]*Journal, error) {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var journals []*Journal
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		j, err := LoadJournalFile(filepath.Join(dir, item.Name()))
		if err != nil {
			return nil, err
		}
		journals = append(journals, j)
	}
	return journals, nil
}
