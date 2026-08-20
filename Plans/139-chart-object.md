# План 139: DSL-объект Диаграмма для отчётов (ECharts)

> Номер сменён с 6 на 139 (issue #1035): под 6 жили разные планы, и ссылка «план 6» перестала быть однозначной. Номер 6 остался за планом «Иерархические справочники» (06-hierarchical-catalogs). Ссылки «план 6» на эту тему читать как «план 139».

## Контекст

Отчёты OneBase — только таблицы. Нужны графики/диаграммы с синтаксисом 1С (объект `Диаграмма`). Рендеринг через ECharts (CDN).

## Паттерн DSL-объекта

Все DSL-объекты (Array, Struct, SpreadsheetDocument) реализуют:
- `This` интерфейс (`Get`/`Set`) — в `internal/dsl/interpreter/env.go:6-9`
- `MethodCallable` интерфейс (`CallMethod`) — в `internal/dsl/interpreter/env.go:11-14`
- Фабрика через `__factory_<тип>` — регистрируется в `buildDSLVars` в `internal/ui/handlers.go:1215`

## Файлы

### 1. СОЗДАТЬ `internal/dsl/interpreter/chart.go`

Четыре структуры: `Chart`, `ChartSeriesCollection`, `ChartSeries`, `ChartPointsCollection`, `ChartPoint`.

**Chart** (главный объект):
- Свойства (Get/Set): `Заголовок`, `Тип` (Круговая/Гистограмма/Линейная), `Ширина`, `Высота`, `Легенда`, `Подписи`
- Get-only: `Серии` → `ChartSeriesCollection`, `Точки` → `ChartPointsCollection`
- Метод: `ToEChartsOption() map[string]any` — генерация JSON-конфига для ECharts

**ChartSeries** (серия данных):
- Свойство: `Имя` (string)
- Метод: `УстановитьЗначение(Точка, Значение)` — stores float64

**ChartPoint** (точка/категория оси X):
- Свойство: `Значение` (string — метка категории)

**Коллекции** (Серии/Точки):
- Метод: `Добавить()` — создаёт элемент, возвращает его

**ToEChartsOption()** — генерирует:
- pie → `series: [{type:"pie", data:[{name,value}...]}]`
- bar → `xAxis:{type:"category"}, series:[{type:"bar", data:[...]}]`
- line → аналогично bar с `type:"line"`

Фабрика:
```go
func NewChartFunctions() map[string]any {
    return map[string]any{
        "__factory_диаграмма": func(args []any) any { return NewChart() },
        "__factory_chart":     func(args []any) any { return NewChart() },
    }
}
```

### 2. СОЗДАТЬ `internal/dsl/interpreter/chart_test.go`

### 3. ИЗМЕНИТЬ `internal/report/report.go`

Добавить поле `ChartProc string yaml:"chart_proc"` в Report struct.

### 4. ИЗМЕНИТЬ `internal/project/loader.go`

Добавить `.rep.os` suffix — маппинг имени файла на имя отчёта.

### 5. ИЗМЕНИТЬ `internal/ui/handlers.go`

- `buildDSLVars`: добавить `NewChartFunctions()`
- `runReport`: вызвать `runChartProc` при наличии `chart_proc`
- Новый метод `runChartProc`: найти процедуру → конвертировать rows в Struct Array → RunWithResult → ToEChartsOption

### 6. ИЗМЕНИТЬ `internal/ui/templates.go`

В `tplReport` вставить блок ECharts (CDN + div + script).

### 7. ПРИМЕР

- `examples/trade/src/ВаловаяПрибыль.rep.os` — DSL-процедура с Диаграмма
- Обновить `examples/trade/reports/валовая_прибыль.yaml` — добавить `chart_proc`
