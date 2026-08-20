# План 31: Стартовая страница с виджетами (Главная)

## Контекст

Стартовая страница в `internal/ui/templates.go:312-319` — статичный заголовок «Добро пожаловать». В 1С аналог — «Начальная страница» с настраиваемыми формами; в OneBase делаем дашборд из виджетов на основе Query Language.

**Решения** (см. AskUserQuestion 2026-05-15):
- Виджеты — отдельные объекты метаданных (`widgets/*.yaml`), редактируются в конфигураторе как catalogs/reports.
- Главная страница — отдельный файл `config/home_page.yaml`, ссылается на виджеты по имени, описывает раскладку.
- MVP-типы виджетов: `kpi`, `list`, `chart`, `actions` + блок «Последние документы пользователя».
- Источник данных — существующий Query Language, без расширения грамматики DSL.

**Связанный план:** `Plans/139-chart-object.md` — DSL-объект `Диаграмма` для отчётов через ECharts. Виджет типа `chart` использует ту же библиотеку рендеринга (ECharts CDN), но конфигурируется декларативно YAML, без `chart_proc`. Если виджет нужен сложный — пользователь делает отчёт с `chart_proc` и вставляет ссылку через виджет `report`.

## Архитектура

### Файлы конфигурации

```
config/home_page.yaml       # одна страница на конфигурацию
widgets/Продажи_KPI.yaml    # каждый виджет = отдельный YAML
widgets/ТопПродавцов.yaml
widgets/ДинамикаПродаж.yaml
```

### home_page.yaml — формат

```yaml
title: Главная
layout: grid                 # grid (auto-fill cards) | rows (вертикальный стек)
rows:
  - widgets: [Продажи_KPI, СреднийЧек_KPI, КоличествоПродаж_KPI]  # KPI-строка
  - widgets: [ДинамикаПродаж]                                      # широкая диаграмма
  - widgets: [ТопПродавцов, ТопТоваров]                            # два списка рядом
  - widgets: [БыстрыеДействия, ПоследниеДокументы]
```

Альтернативный плоский формат (для `layout: grid`):
```yaml
title: Главная
layout: grid
widgets:
  - { name: Продажи_KPI,         span: 1 }
  - { name: СреднийЧек_KPI,      span: 1 }
  - { name: ДинамикаПродаж,      span: 3 }
  - { name: БыстрыеДействия,     span: 2 }
```

### widgets/*.yaml — формат

**KPI:**
```yaml
name: Продажи_KPI
type: kpi
title: Выручка за месяц
format: money               # money | number | percent
query: |
  ВЫБРАТЬ СУММА(Сумма) КАК Значение
  ИЗ Документ.РеализацияТоваров
  ГДЕ Дата >= &НачалоМесяца
params:
  НачалоМесяца: "{{today|start_of_month}}"   # template как в scheduled jobs
compare_to: prev_period      # опционально: показать дельту %
```

**List:**
```yaml
name: ТопПродавцов
type: list
title: Топ продавцов месяца
limit: 10
query: |
  ВЫБРАТЬ Ответственный, СУММА(Сумма) КАК Выручка
  ИЗ Документ.РеализацияТоваров
  ГДЕ Дата >= &НачалоМесяца
  СГРУППИРОВАТЬ ПО Ответственный
  УПОРЯДОЧИТЬ ПО Выручка УБЫВ
params:
  НачалоМесяца: "{{today|start_of_month}}"
columns:                     # опционально: явное управление колонками
  - { field: Ответственный, label: "Сотрудник" }
  - { field: Выручка,       label: "Выручка", format: money, align: right }
```

**Chart:**
```yaml
name: ДинамикаПродаж
type: chart
chart_kind: bar              # bar | line | pie
title: Продажи по дням
query: |
  ВЫБРАТЬ Дата, СУММА(Сумма) КАК Сумма
  ИЗ Документ.РеализацияТоваров
  ГДЕ Дата >= &НачалоПериода
  СГРУППИРОВАТЬ ПО Дата
  УПОРЯДОЧИТЬ ПО Дата
params:
  НачалоПериода: "{{today|minus_days:30}}"
x_field: Дата
y_fields: [Сумма]            # несколько → несколько серий
```

**Actions:**
```yaml
name: БыстрыеДействия
type: actions
title: Создать
items:
  - { label: "Реализация",  entity: РеализацияТоваров }
  - { label: "Поступление", entity: ПоступлениеТоваров }
  - { label: "Контрагент",  entity: Контрагент }
```

**Recent (платформенный, без query):**
```yaml
name: ПоследниеДокументы
type: recent
title: Мои последние документы
limit: 8
entities: [РеализацияТоваров, ПоступлениеТоваров]   # пусто = все
scope: current_user          # current_user | all
```

## Файлы кода

### 1. СОЗДАТЬ `internal/metadata/widget.go`

```go
type Widget struct {
    Name      string
    Type      WidgetType  // kpi | list | chart | actions | recent
    Title     string
    Query     string
    Params    map[string]string  // raw templates ({{today|...}})
    // type-specific:
    Format     string             // kpi: money/number/percent
    CompareTo  string             // kpi: prev_period
    Limit      int                // list/recent
    Columns    []WidgetColumn     // list
    ChartKind  string             // chart: bar/line/pie
    XField     string             // chart
    YFields    []string           // chart
    Items      []WidgetAction     // actions
    Entities   []string           // recent
    Scope      string             // recent
}
```

Функции: `LoadWidget(path)`, `LoadWidgetDir(dir)`.

### 2. СОЗДАТЬ `internal/metadata/homepage.go`

```go
type HomePage struct {
    Title   string
    Layout  string             // grid | rows
    Rows    [][]string         // rows[i] = список имён виджетов в строке
    Widgets []HomePageWidget   // плоский формат с span
}
```

`LoadHomePage(path)` — читает `config/home_page.yaml`. Если файла нет — возвращает дефолтную страницу (создаётся в коде, чтобы текущие установки не сломались).

### 3. ИЗМЕНИТЬ `internal/project/loader.go`

- Добавить `Widgets []*metadata.Widget` в `Project`.
- Добавить `HomePage *metadata.HomePage`.
- В `Load()` — вызвать `loadWidgets()` (по аналогии с `loadReports`) и `loadHomePage()`.

### 4. СОЗДАТЬ `internal/widget/runner.go`

Исполнение виджета: компилирует Query, подставляет параметры (re-use логику scheduled jobs `{{today|...}}`), выполняет, возвращает результат в типизированной форме:

```go
type Result struct {
    Type    string                // kpi | list | chart | actions | recent
    KPI     *KPIResult            // одно число + дельта
    Rows    []map[string]any      // для list / recent
    Chart   *ChartData            // labels[] + series[][]
    Actions []ActionItem          // готовые ссылки на /ui/новый
}
```

Кеш на 60 секунд (in-memory map по name → result) — KPI/диаграммы могут считаться долго, дашборд перерисовывается часто.

### 5. ИЗМЕНИТЬ `internal/ui/handlers.go`

В `func (s *Server) index`:
- Загрузить `HomePage` из проекта (или дефолт).
- Для каждого виджета вызвать `widget.Run(ctx, w, db)`.
- Передать массив результатов в шаблон `page-index`.

### 6. ИЗМЕНИТЬ `internal/ui/templates.go`

Заменить `tplIndex` на дашборд:

```html
{{define "page-index"}}
{{template "head" .}}{{template "nav" .}}
<main>
  <h2>{{.HomePage.Title}}</h2>
  {{range .WidgetResults}}
    {{if eq .Type "kpi"}}      {{template "widget-kpi" .}}
    {{else if eq .Type "list"}} {{template "widget-list" .}}
    {{else if eq .Type "chart"}}{{template "widget-chart" .}}
    {{else if eq .Type "actions"}}{{template "widget-actions" .}}
    {{else if eq .Type "recent"}}{{template "widget-recent" .}}
    {{end}}
  {{end}}
</main></div>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<script>/* инициализация всех echarts-канвасов */</script>
</body></html>
{{end}}
```

Каждый виджет в отдельном `{{define "widget-..."}}`, стилизованный как `.card`. KPI-карточка — большая цифра + подпись + опц. зелёная/красная дельта.

### 7. Конфигуратор — `internal/launcher/configurator.go`

- Добавить ветку дерева **«Виджеты»** (наряду с Отчётами, Регистрами).
- Endpoint редактирования: `/cfg/widgets/<name>` — YAML-редактор (Monaco) + предпросмотр.
- Создание нового виджета: шаблон по выбранному типу.
- Добавить ветку **«Главная страница»** (одна запись) → редактор `home_page.yaml`.

### 8. Параметры-шаблоны

Расширить существующий `{{today|...}}` парсер из scheduled jobs:
- `start_of_month`, `end_of_month`
- `start_of_year`, `end_of_year`
- `start_of_week`
- `minus_days:N`, `minus_months:N` (есть)

Файл: где-то в `internal/scheduler/templates.go` или `internal/metadata/scheduled.go` — найти и переиспользовать.

### 9. Кросс-проектные дефолты

Если в конфигурации нет `home_page.yaml` и нет `widgets/` — показывать дефолтную страницу с:
- виджет `recent` (последние документы пользователя) — работает без настройки
- виджет `actions` со всеми справочниками/документами из меню

Это убирает текущее ощущение «пустоты» сразу после создания базы без необходимости настройки.

## Фазирование

**Фаза 1 (MVP, чтобы сразу заменить «Добро пожаловать»)** — ✅ выполнено (2026-05-15):
1. ✅ `widget.go` + `homepage.go` (metadata)
2. ✅ `widget/runner.go`
3. ✅ KPI + list + actions + recent
4. ✅ Шаблон `page-index` с этими 4 типами
5. ✅ Дефолтная страница без файла (синтетический `_default_recent` через `_audit`)

**Фаза 2 (диаграммы)** — ✅ выполнено (2026-05-15):
6. ✅ chart-виджет + ECharts CDN (bar/line/pie через `<script>`-blob)
7. ✅ Параметры-шаблоны `{{today|...}}` для виджетов (`scheduler.ResolveParamTemplates`)

**Фаза 3 (UX в конфигураторе)** — ✅ выполнено (2026-05-15):
8. ✅ Ветка «Виджеты» в дереве + YAML-редактор (`internal/launcher/widget_handlers.go`,
    панели в `configurator_tmpl.go`). Создание через стандартный `/configurator/new` с `kind=widget`,
    удаление отдельной кнопкой. Сохранение валидирует через `metadata.LoadWidgetFile`.
9. ✅ Узел «Главная страница» + редактор раскладки (`config/home_page.yaml`)
10. ✅ Кеш результатов виджетов (60s in-memory, `internal/widget/cache.go`).
    Ключ `widgetName\x00user`, ошибки не кешируются, нет принудительной инвалидации
    из dev-loop (надеемся на TTL).

**Фаза 4 (опционально)** — ⏳ не сделано:
11. Персональная стартовая страница на пользователя (override через `_user_home_page` в БД).
    Сейчас раскладка одна на конфигурацию.
12. Drag-and-drop редактор раскладки на JS — пока пользователь редактирует `home_page.yaml`
    вручную в textarea.
13. Виджет `report` — встраивание готового отчёта по `report_name` (переиспользует
    существующий `chart_proc` пайплайн).
14. Виджет `text` — markdown-объявления/баннеры (полезно для админ-уведомлений).
15. Параметры на уровне страницы — переключатель периода «Сегодня / Неделя / Месяц»
    сверху дашборда, прокидывается во все виджеты как `&Период`.
16. Горячая инвалидация кеша из конфигуратора — сейчас `ui.Server.InvalidateWidgetCache()`
    реализован, но не вызывается; нужно проксировать сигнал из launcher после сохранения
    виджета/home_page или после `reg.Load*`.
17. Replicas/Monaco для YAML-редактора виджетов (сейчас обычный `<textarea>`).
18. Удаление виджета на disk-backed базах не очищает движения в `recent`-виджете —
    проверить, что нет битых ссылок после rename.

## Тестирование

- Unit: `metadata/widget_test.go`, `metadata/homepage_test.go` — парсинг YAML.
- Unit: `widget/runner_test.go` — выполнение query с подстановкой параметров.
- Интеграция: добавить виджеты в `examples/trade`, проверить рендеринг.

## Открытые вопросы

- Лимит количества виджетов на странице? Стартово — без лимита, кеш спасает. Не возвращаемся пока не упрёмся.
- Параметры на уровне страницы (период с переключателем «Сегодня / Неделя / Месяц»)? Перенесено в Фазу 4, пункт 15.
- Виджет с RBAC — должен ли запрос видеть только разрешённые пользователю данные? Сейчас запросы выполняются с полными правами процесса базы (`storage.DB` без RBAC-фильтра). Если нужна изоляция — добавить проброс роли в `query.CompileOpts` (отдельный план).
- Recent-виджет с `scope: current_user` использует `_audit.user_login`. Если в Фазе 4 появится `_user_home_page`, надо договориться про ключ — `user_login` или `user_id`.
