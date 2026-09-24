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

	// NewTab открывает ссылку в новой вкладке. Нужен там, куда ведёт ссылка из
	// админки НАРУЖУ — на публичный сайт, во внешнюю систему: в той же вкладке
	// пользователь остаётся без интерфейса и без пути назад, потому что на той
	// стороне ссылки на базу нет и быть не должно.
	NewTab bool `yaml:"new_tab"`
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

// WidgetSource описывает навигацию строк list-виджета (план 182C): сущность,
// чью карточку открывает клик по строке, и колонку результата, несущую
// идентификатор записи. Компилятор подтверждает по RefColumns, что колонка —
// ссылка именно на эту сущность; без подтверждения строки остаются
// некликабельными (fail-closed).
type WidgetSource struct {
	Entity  string `yaml:"entity"`
	IDField string `yaml:"id_field"`
}

// WidgetFilterValue — один вариант select-фильтра list-виджета.
type WidgetFilterValue struct {
	Value  string            `yaml:"value"`
	Label  string            `yaml:"label"`
	Labels map[string]string `yaml:"labels"`
}

// DisplayLabel возвращает подпись варианта с учётом языка.
func (v WidgetFilterValue) DisplayLabel(lang string) string {
	if lang != "" {
		if s, ok := v.Labels[lang]; ok && s != "" {
			return s
		}
	}
	if v.Label != "" {
		return v.Label
	}
	return v.Value
}

// WidgetFilter — интерактивный фильтр list-виджета (план 182D). Значение
// приходит только из браузера по имени фильтра; тип, param и query сервер
// перечитывает из метаданных. Пустое значение передаётся в запрос типизированным
// nil — семантику «пустого отбора» задаёт сам запрос (&Параметр ЕСТЬ ПУСТО).
type WidgetFilter struct {
	Name    string              `yaml:"name"`
	Label   string              `yaml:"label"`
	Labels  map[string]string   `yaml:"labels"`
	Type    string              `yaml:"type"` // string | number | date | bool | select | reference:<Сущность>
	Param   string              `yaml:"param"`
	Values  []WidgetFilterValue `yaml:"values,omitempty"` // select
	Default any                 `yaml:"default,omitempty"`
}

// DisplayLabel возвращает подпись фильтра с учётом языка.
func (f WidgetFilter) DisplayLabel(lang string) string {
	if lang != "" {
		if s, ok := f.Labels[lang]; ok && s != "" {
			return s
		}
	}
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

// ReferenceEntity возвращает имя сущности для типа reference:<Сущность>
// (пустую строку для остальных типов).
func (f WidgetFilter) ReferenceEntity() string {
	const prefix = "reference:"
	if !strings.HasPrefix(f.Type, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(f.Type, prefix))
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
	// Source — навигация строк list-виджета: клик по строке открывает карточку
	// записи Source.Entity по идентификатору из колонки Source.IDField. У
	// kpi/chart/recent/actions навигации строк нет, и check такое объявление
	// отвергает.
	Source *WidgetSource `yaml:"source"`
	// Filters — интерактивные фильтры list-виджета (план 182D). Только
	// объявленные фильтры принимаются от браузера; значения идут bind-параметрами
	// существующего компилятора запросов, текст запроса значениями не правится.
	Filters []WidgetFilter `yaml:"filters,omitempty"`
	// Link — куда ведёт клик по карточке (kpi). Счётчик «в карантине: 3» без
	// ссылки заставляет искать эту очередь руками; со ссылкой карточка сама
	// открывает список с нужным отбором. Значение — внутренний путь приложения
	// (начинается с «/»), внешние адреса не принимаются.
	Link string `yaml:"link"`
	// RefreshOn — имена уже существующих onebase-событий, по которым карточка
	// перечитывается без F5 (план 182B). Штатный источник — «данные.<имя>» от
	// сущности с notify_changes; прочие имена шлёт конфигурация через
	// ОтправитьУведомление. Допустимо для kpi/list/chart/recent; у actions
	// перечитывать нечего, и check такое объявление отвергает.
	RefreshOn []string `yaml:"refresh_on"`
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
