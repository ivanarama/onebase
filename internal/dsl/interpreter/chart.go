package interpreter

import (
	"fmt"
	"strings"
)

// ─── Chart (Диаграмма) — 1C-like chart object for ECharts ──────────────────

// Chart is the main DSL chart object: Новый Диаграмма
type Chart struct {
	title     string
	chartType string // "гистограмма"/"bar", "линейная"/"line", "круговая"/"pie"
	width     string
	height    string
	legend    bool
	tooltip   bool
	labels    bool
	series    []*ChartSeries
	points    []*ChartPoint
	values    map[int]map[int]float64 // [seriesIdx][pointIdx] → value
}

func NewChart() *Chart {
	return &Chart{
		chartType: "гистограмма",
		width:     "100%",
		height:    "400px",
		legend:    true,
		tooltip:   true,
		values:    make(map[int]map[int]float64),
	}
}

func (c *Chart) Get(name string) any {
	switch strings.ToLower(name) {
	case "заголовок", "title":
		return c.title
	case "тип", "type":
		return c.chartType
	case "ширина", "width":
		return c.width
	case "высота", "height":
		return c.height
	case "легенда", "legend":
		return c.legend
	case "подсказки", "tooltip":
		return c.tooltip
	case "подписи", "labels":
		return c.labels
	case "серии", "series":
		return &ChartSeriesCollection{chart: c}
	case "точки", "points":
		return &ChartPointsCollection{chart: c}
	}
	return nil
}

func (c *Chart) Set(name string, v any) {
	switch strings.ToLower(name) {
	case "заголовок", "title":
		c.title = fmt.Sprintf("%v", v)
	case "тип", "type":
		c.chartType = fmt.Sprintf("%v", v)
	case "ширина", "width":
		c.width = fmt.Sprintf("%v", v)
	case "высота", "height":
		c.height = fmt.Sprintf("%v", v)
	case "легенда", "legend":
		c.legend = truthy(v)
	case "подсказки", "tooltip":
		c.tooltip = truthy(v)
	case "подписи", "labels":
		c.labels = truthy(v)
	}
}

func (c *Chart) CallMethod(method string, args []any) any { return nil }

// setValue stores a value for a given series/point combination.
func (c *Chart) setValue(seriesIdx, pointIdx int, v float64) {
	if c.values[seriesIdx] == nil {
		c.values[seriesIdx] = make(map[int]float64)
	}
	c.values[seriesIdx][pointIdx] = v
}

// echartsType returns the ECharts chart type string.
func (c *Chart) echartsType() string {
	switch strings.ToLower(c.chartType) {
	case "круговая", "pie":
		return "pie"
	case "линейная", "line":
		return "line"
	default:
		return "bar"
	}
}

// ToEChartsOption builds the ECharts option map for JSON serialization.
func (c *Chart) ToEChartsOption() map[string]any {
	opt := map[string]any{}

	if c.title != "" {
		opt["title"] = map[string]any{"text": c.title}
	}

	if c.tooltip {
		trigger := "axis"
		if c.echartsType() == "pie" {
			trigger = "item"
		}
		opt["tooltip"] = map[string]any{"trigger": trigger}
	}

	et := c.echartsType()

	if et == "pie" {
		// Single series: [{name: "Category", value: 123}, ...]
		data := make([]map[string]any, len(c.points))
		for i, pt := range c.points {
			val := 0.0
			if c.values[0] != nil {
				val = c.values[0][i]
			}
			data[i] = map[string]any{"name": pt.label, "value": val}
		}
		series := map[string]any{
			"type": "pie",
			"data": data,
		}
		if c.labels {
			series["label"] = map[string]any{"show": true, "formatter": "{b}: {d}%"}
		}
		opt["series"] = []any{series}
		if c.legend {
			opt["legend"] = map[string]any{}
		}
	} else {
		// Bar / Line with category axis
		categories := make([]string, len(c.points))
		for i, pt := range c.points {
			categories[i] = pt.label
		}
		opt["xAxis"] = map[string]any{"type": "category", "data": categories}
		opt["yAxis"] = map[string]any{"type": "value"}

		seriesList := make([]any, len(c.series))
		legendData := make([]string, len(c.series))
		for si, s := range c.series {
			data := make([]float64, len(c.points))
			for pi := range c.points {
				if c.values[si] != nil {
					data[pi] = c.values[si][pi]
				}
			}
			siMap := map[string]any{
				"name": s.name,
				"type": et,
				"data": data,
			}
			if c.labels {
				siMap["label"] = map[string]any{"show": true, "position": "top"}
			}
			seriesList[si] = siMap
			legendData[si] = s.name
		}
		opt["series"] = seriesList
		if c.legend && len(c.series) > 1 {
			opt["legend"] = map[string]any{"data": legendData}
		}
	}

	return opt
}

// ─── ChartSeriesCollection ─────────────────────────────────────────────────

type ChartSeriesCollection struct {
	chart *Chart
}

func (c *ChartSeriesCollection) Get(name string) any    { return nil }
func (c *ChartSeriesCollection) Set(name string, v any) {}

func (c *ChartSeriesCollection) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "добавить", "add":
		s := &ChartSeries{chart: c.chart, idx: len(c.chart.series)}
		c.chart.series = append(c.chart.series, s)
		return s
	case "количество", "count":
		return float64(len(c.chart.series))
	}
	return nil
}

// ─── ChartSeries ───────────────────────────────────────────────────────────

type ChartSeries struct {
	chart *Chart
	idx   int
	name  string
}

func (s *ChartSeries) Get(name string) any {
	switch strings.ToLower(name) {
	case "имя", "name":
		return s.name
	}
	return nil
}

func (s *ChartSeries) Set(name string, v any) {
	switch strings.ToLower(name) {
	case "имя", "name":
		s.name = fmt.Sprintf("%v", v)
	}
}

func (s *ChartSeries) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "установитьзначение", "setvalue":
		if len(args) >= 2 {
			ptIdx := -1
			switch v := args[0].(type) {
			case *ChartPoint:
				ptIdx = v.idx
			default:
				if f, ok := toFloat(v); ok {
					ptIdx = int(f)
				}
			}
			val := toFloatOr0(args[1])
			if ptIdx >= 0 {
				s.chart.setValue(s.idx, ptIdx, val)
			}
		}
	}
	return nil
}

// ─── ChartPointsCollection ─────────────────────────────────────────────────

type ChartPointsCollection struct {
	chart *Chart
}

func (c *ChartPointsCollection) Get(name string) any    { return nil }
func (c *ChartPointsCollection) Set(name string, v any) {}

func (c *ChartPointsCollection) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "добавить", "add":
		pt := &ChartPoint{chart: c.chart, idx: len(c.chart.points)}
		c.chart.points = append(c.chart.points, pt)
		return pt
	case "количество", "count":
		return float64(len(c.chart.points))
	}
	return nil
}

// ─── ChartPoint ────────────────────────────────────────────────────────────

type ChartPoint struct {
	chart *Chart
	idx   int
	label string
}

func (p *ChartPoint) Get(name string) any {
	switch strings.ToLower(name) {
	case "значение", "value":
		return p.label
	}
	return nil
}

func (p *ChartPoint) Set(name string, v any) {
	switch strings.ToLower(name) {
	case "значение", "value":
		p.label = fmt.Sprintf("%v", v)
	}
}

func (p *ChartPoint) CallMethod(method string, args []any) any { return nil }

// ─── Factory ───────────────────────────────────────────────────────────────

func NewChartFunctions() map[string]any {
	return map[string]any{
		"__factory_диаграмма": func(args []any) any { return NewChart() },
		"__factory_chart":     func(args []any) any { return NewChart() },
	}
}
