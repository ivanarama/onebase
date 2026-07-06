package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// WidgetType is the kind of dashboard widget.
type WidgetType string

const (
	WidgetTypeKPI     WidgetType = "kpi"
	WidgetTypeList    WidgetType = "list"
	WidgetTypeChart   WidgetType = "chart"
	WidgetTypeActions WidgetType = "actions"
	WidgetTypeRecent  WidgetType = "recent"
)

// WidgetColumn defines a single column for the list widget.
type WidgetColumn struct {
	Field  string            `yaml:"field"`
	Label  string            `yaml:"label"`
	Labels map[string]string `yaml:"labels"` // переводы по языкам
	Format string            `yaml:"format"` // money | number | percent | date
	Align  string            `yaml:"align"`  // left | right | center
}

// DisplayLabel возвращает подпись колонки виджета с учётом языка.
func (c WidgetColumn) DisplayLabel(lang string) string {
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

// WidgetAction is a button on the actions widget.
type WidgetAction struct {
	Label  string            `yaml:"label"`
	Labels map[string]string `yaml:"labels"`
	Entity string            `yaml:"entity"` // creates /ui/<kind>/<Entity>/new
	URL    string            `yaml:"url"`    // raw URL alternative if Entity is empty
}

// DisplayLabel возвращает подпись кнопки виджета с учётом языка.
func (a WidgetAction) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := a.Labels[lang]; ok && v != "" {
			return v
		}
	}
	return a.Label
}

// Widget describes a single dashboard widget loaded from widgets/<Name>.yaml.
type Widget struct {
	Name      string            `yaml:"name"`
	Type      WidgetType        `yaml:"type"`
	Title     string            `yaml:"title"`
	Titles    map[string]string `yaml:"titles"` // переводы заголовка по языкам
	Query     string            `yaml:"query"`
	Params    map[string]string `yaml:"params"`     // raw {{today|...}} templates
	Format    string            `yaml:"format"`     // kpi: money | number | percent
	CompareTo string            `yaml:"compare_to"` // kpi: prev_period (optional)
	Limit     int               `yaml:"limit"`      // list / recent
	Columns   []WidgetColumn    `yaml:"columns"`    // list
	ChartKind string            `yaml:"chart_kind"` // chart: bar | line | pie
	ChartType string            `yaml:"chart_type"` // legacy alias for chart_kind
	XField    string            `yaml:"x_field"`    // chart
	YFields   []string          `yaml:"y_fields"`   // chart
	Items     []WidgetAction    `yaml:"items"`      // actions
	Entities  []string          `yaml:"entities"`   // recent — filter to these entity names
	Scope     string            `yaml:"scope"`      // recent: current_user | all
}

// DisplayTitle возвращает заголовок виджета с учётом языка.
func (w *Widget) DisplayTitle(lang string) string {
	if lang != "" {
		if v, ok := w.Titles[lang]; ok && v != "" {
			return v
		}
	}
	return w.Title
}

// LoadWidgetFile parses one widgets/<Name>.yaml file.
func LoadWidgetFile(path string) (*Widget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w Widget
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if w.Name == "" {
		return nil, fmt.Errorf("%s: missing name", path)
	}
	if w.Type == "" {
		return nil, fmt.Errorf("%s: missing type (kpi|list|chart|actions|recent)", path)
	}
	if !isKnownWidgetType(w.Type) {
		return nil, fmt.Errorf("%s: unknown widget type %q", path, w.Type)
	}
	if w.Limit <= 0 && (w.Type == WidgetTypeList || w.Type == WidgetTypeRecent) {
		w.Limit = 10
	}
	if w.ChartKind == "" {
		w.ChartKind = w.ChartType
	}
	if w.ChartKind == "" && w.Type == WidgetTypeChart {
		w.ChartKind = "bar"
	}
	if w.Scope == "" && w.Type == WidgetTypeRecent {
		w.Scope = "current_user"
	}
	return &w, nil
}

// LoadWidgetDir loads all widgets/*.yaml in dir. Missing directory is not an error.
func LoadWidgetDir(dir string) ([]*Widget, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", dir, err)
	}
	var widgets []*Widget
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		w, err := LoadWidgetFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		widgets = append(widgets, w)
	}
	return widgets, nil
}

func isKnownWidgetType(t WidgetType) bool {
	switch t {
	case WidgetTypeKPI, WidgetTypeList, WidgetTypeChart, WidgetTypeActions, WidgetTypeRecent:
		return true
	}
	return false
}
