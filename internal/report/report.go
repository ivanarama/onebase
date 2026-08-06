package report

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Param struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`    // string, date, number, bool, select, reference:Entity
	Label   string            `yaml:"label"`   // display label; falls back to Name
	Labels  map[string]string `yaml:"labels"`  // per-language labels (lang code → translation)
	Options []string          `yaml:"options"` // for type: select
}

type Report struct {
	Name        string            `yaml:"name"`
	Title       string            `yaml:"title"`
	Titles      map[string]string `yaml:"titles"`
	Params      []Param           `yaml:"params"`
	Query       string            `yaml:"query"`
	ChartProc   string            `yaml:"chart_proc"`
	Composition *Composition      `yaml:"composition"` // nil = плоская таблица (старое поведение)
	Variants    []ReportVariant   `yaml:"variants"`    // доп. именованные компоновки по тому же запросу
	// OutputFormat — предпочтительный формат выгрузки отчёта (issue #218): пусто|html
	// (интерактивный просмотр + кнопки Excel/PDF), pdf, excel или совместимый
	// legacy-синоним excel_html. Реальная выгрузка идёт через /ui/report/<name>/pdf
	// и /excel независимо от этого поля; формат лишь подсказывает приоритет в UI.
	OutputFormat string `yaml:"output_format,omitempty"`

	// External помечает отчёт из внешнего контура (таблица _ext_reports),
	// а не из конфигурации проекта. Заполняется программно при загрузке;
	// в YAML не сериализуется.
	External bool `yaml:"-"`
}

// ReportVariant — именованная компоновка по тому же запросу отчёта («вариант
// отчёта», как в 1С). На форме отчёта пользователь выбирает вариант; пустой
// выбор соответствует основному блоку composition.
type ReportVariant struct {
	Name        string       `yaml:"name"`
	Composition *Composition `yaml:"composition"`
}

// Composition описывает настройки компоновки данных отчёта.
type Composition struct {
	Groupings    []string   `yaml:"groupings"`
	Columns      []string   `yaml:"columns"` // непусто = режим кросс-таблицы: измерения в колонки
	Measures     []Measure  `yaml:"measures"`
	Totals       Totals     `yaml:"totals"`
	Detail       bool       `yaml:"detail"`
	Sort         []SortKey  `yaml:"sort"`
	Conditional  []CondRule `yaml:"conditional"`
	Appearance   Appearance `yaml:"appearance"` // общее оформление вывода (линии, зебра)
	Chart        *ChartSpec `yaml:"chart"`
	DetailLink   string     `yaml:"detail_link"`   // поле строки с UUID регистратора/ссылки
	DetailEntity string     `yaml:"detail_entity"` // имя сущности для перехода по ссылке
}

// Measure описывает измеримый показатель (поле + агрегат) в компоновке.
type Measure struct {
	Field  string `yaml:"field"`
	Agg    string `yaml:"agg"` // sum|count|avg|min|max ("" = sum)
	Title  string `yaml:"title"`
	Align  string `yaml:"align"`  // left|right|center ("" = right)
	Format string `yaml:"format"` // "" | "#,##0.00" | "#,##0" | "0.0%" и т.п.
	Expr   string `yaml:"expr"`   // непустое = вычисляемый показатель (DSL-выражение по другим полям)
}

// Totals управляет выводом итогов в отчёте.
type Totals struct {
	Grand     bool `yaml:"grand"`
	Subtotals bool `yaml:"subtotals"`
}

// SortKey задаёт поле и направление сортировки.
type SortKey struct {
	Field string `yaml:"field"`
	Dir   string `yaml:"dir"` // asc|desc
}

// CondRule описывает правило условного оформления строки или ячейки.
type CondRule struct {
	When  string    `yaml:"when"`
	Field string    `yaml:"field"` // "" = вся строка
	Style CellStyle `yaml:"style"`
}

// CellStyle определяет стиль ячейки для условного оформления.
type CellStyle struct {
	Color      string `yaml:"color"`
	Background string `yaml:"background"`
	Bold       bool   `yaml:"bold"`
	Italic     bool   `yaml:"italic"`
}

// Appearance — общее оформление вывода компоновки (план 71): свойства всей
// таблицы, не зависящие от условий (в отличие от Conditional). Нулевое значение
// = исторический вид (горизонтальные линии, без зебры) — существующие отчёты не
// меняются. Lines: "" или "horizontal" — только нижние границы (как было);
// "vertical" — только вертикальные; "both" — полная сетка; "none" — без линий.
//
// CollapseTo — начальное состояние групп при открытии отчёта (issue #575):
// уровни 0..N развёрнуты, глубже — свёрнуто. 0 — видны только группы верхнего
// уровня со своими итогами. nil (ключа нет) — всё развёрнуто, как было. Поле
// указательное именно поэтому: `collapse_to: 0` — осмысленное значение, и от
// «не задано» его надо отличать.
type Appearance struct {
	Lines      string `yaml:"lines" json:"lines,omitempty"`
	Zebra      bool   `yaml:"zebra" json:"zebra,omitempty"`
	CollapseTo *int   `yaml:"collapse_to,omitempty" json:"collapse_to,omitempty"`
}

// ChartSpec задаёт параметры диаграммы, встроенной в отчёт.
type ChartSpec struct {
	Type     string   `yaml:"type"` // bar|line|pie
	Category string   `yaml:"category"`
	Series   []string `yaml:"series"`
}

// DisplayLabel возвращает подпись параметра с учётом языка.
func (p *Param) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := p.Labels[lang]; ok && v != "" {
			return v
		}
	}
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

// DisplayName возвращает заголовок отчёта с учётом языка.
func (r *Report) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := r.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

// ActiveComposition возвращает компоновку выбранного варианта по его имени.
// Пустое имя или несовпадение → основной composition (вариант по умолчанию).
func (r *Report) ActiveComposition(name string) *Composition {
	if name != "" {
		for i := range r.Variants {
			if r.Variants[i].Name == name {
				return r.Variants[i].Composition
			}
		}
	}
	return r.Composition
}

func LoadFile(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data)
}

// ParseBytes разбирает YAML отчёта из памяти (без файла). Нужна для внешних
// отчётов, хранящихся в БД (см. internal/extform).
func ParseBytes(data []byte) (*Report, error) {
	var r Report
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
