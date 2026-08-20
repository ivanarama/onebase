# СКД v2 — развитие компоновки отчётов · план реализации (план 68)

> **Для агентов-исполнителей:** РЕКОМЕНДУЕМЫЙ СУБ-НАВЫК: используйте
> `superpowers:subagent-driven-development` (свежий субагент на задачу) или
> `superpowers:executing-plans`. Шаги размечены чекбоксами `- [ ]`.
> Каждая правка движка — по TDD: сначала падающий тест, затем минимальная реализация.

**Цель:** довести «лайт-СКД» (план 59) до полноценной: сворачиваемые блоки и группы,
выравнивание и форматирование значений, экспорт с группами, вычисляемые показатели,
расшифровка в документ, кросс-таблица (pivot) и варианты компоновки.

**Архитектура:** ядро компоновки — чистый пакет `internal/report/compose` (без БД/HTTP):
плоские строки → дерево групп с итогами/стилями. Рендер и график — `internal/ui`.
Конфигуратор-конструктор — `internal/launcher`. Все новые поля блока `composition`
**опциональны** — старые отчёты не меняются (полная обратная совместимость).

**Технологии:** Go (без CGo), `shopspring/decimal` (деньги), `gopkg.in/yaml.v3`,
ECharts (завендорён), excelize (экспорт). Тесты — `go test` (на Windows запускать
через инструмент PowerShell). DSL-интерпретатор — для выражений (`when`, вычисляемые показатели).

---

## Контекст: как устроена СКД сейчас (читать перед началом)

**Ключевые файлы:**
- `internal/report/report.go` — типы `Report`, `Composition`, `Measure`, `Totals`,
  `SortKey`, `CondRule`, `CellStyle`, `ChartSpec` + YAML-теги + `LoadFile`/`ParseBytes`.
- `internal/report/compose/compose.go` — `Compose`/`ComposeN`, `Group`, `DetailRow`,
  `Result`; `alignRowKeys`, `buildGroups`, `aggregate`, `aggMeasure`, `evalStyles`,
  `sortGroups`, `sortDetails`, `toDecimal`, `normalizeGroupKey`, `ExportToDecimal`.
- `internal/report/compose/compose_test.go` — юнит-тесты ядра (без БД).
- `internal/ui/report_compose_render.go` — `renderComposedTable`, `writeGroup`,
  `writeDetail`, `buildComposedChart`, `cssOf`, `fmtVal`, `measureTitle`.
- `internal/ui/report_eval.go` — `interpEvaluator` (DSL-выражения `when`), `newInterpEvaluator`.
- `internal/ui/handlers_reports.go` — `runReport` (ветка `Composition!=nil`),
  `reportExcel`, `reportForm`, `reportRun`.
- `internal/ui/templates.go` — шаблон `page-report` (≈ строка 2077): блоки параметров,
  графика (`ChartOption`), данных (`ComposedHTML`); JS-сворачивание групп (≈ 2148).
- `internal/configcheck/check.go` — `CheckReportComposition` (валидация до рантайма).
- `internal/launcher/report_composition_form.go` — `parseCompositionForm`,
  `applyReportComposition` (конструктор → YAML).
- `internal/launcher/configurator_tmpl.go` — вкладки конструктора (Запрос/Структура/
  Оформление/График/Предпросмотр).

**Текущие типы (`report.go`):**
```go
type Composition struct {
    Groupings   []string   `yaml:"groupings"`
    Measures    []Measure  `yaml:"measures"`
    Totals      Totals     `yaml:"totals"`
    Detail      bool       `yaml:"detail"`
    Sort        []SortKey  `yaml:"sort"`
    Conditional []CondRule `yaml:"conditional"`
    Chart       *ChartSpec `yaml:"chart"`
}
type Measure  struct { Field, Agg, Title string }            // agg: sum|count|avg|min|max ("" = sum)
type Totals   struct { Grand, Subtotals bool }
type SortKey  struct { Field, Dir string }                    // dir: asc|desc
type CondRule struct { When, Field string; Style CellStyle }  // Field "" = вся строка
type CellStyle struct { Color, Background string; Bold, Italic bool }
type ChartSpec struct { Type, Category string; Series []string } // type: bar|line|pie
```

**Ключевые контракты compose:**
- `Compose(rows []Row, spec report.Composition, ev Evaluator) (*Result, error)` — `Row = map[string]any`.
- `Group{Field string; Key any; Subtotals map[string]any; Count int; Children []*Group; Details []DetailRow; Styles map[string]report.CellStyle}`.
- `Result{Columns []string; Groups []*Group; Grand map[string]any; RowCount int; Capped bool}`.
- Имена колонок результата запроса — **в нижнем регистре** (компилятор lowercase'ит
  алиасы). `alignRowKeys` уже выравнивает ключи строк к именам полей spec
  регистронезависимо — НОВЫЕ поля-имена (columns и т.п.) добавляйте в `alignRowKeys`.

**Команды:**
- Тесты: `go test ./internal/report/... ./internal/ui/... ./internal/configcheck/... ./internal/launcher/...`
- Сборка (остановив сервер): `taskkill /IM onebase.exe /F` → `go build -o onebase.exe ./cmd/onebase`
- Проверка конфигурации: `./onebase.exe check --project <dir>`
- Демо-данные для ручной проверки: `./onebase.exe migrate --project <conf> --sqlite t.db`
  затем `./onebase.exe procrun --project <conf> --sqlite t.db --proc ЗаполнитьТестовуюБазу`
  (пример конфигурации с регистром `ВаловаяПрибыль` и отчётом-СКД: `C:/Projects/OneBaseConfs/PuT`).
- Коммиты — `тип(scope): описание` по-русски, в конце `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

**Дисциплина:** каждая задача — отдельный коммит. Правки ядра `compose` — строго TDD
(тест в `compose_test.go` без БД). После каждого этапа — сборка + `check` + ручная
проверка на демо-базе PuT.

---

# Этап A — Quick wins (UX, низкая сложность)

> ✅ **ВЫПОЛНЕНО** (ветка `feature/59-report-composition`): A1 `6a307d7` · A2 `129df74` ·
> A3 `ccab25b` · A4 `ee1aac1` · A5 `8b6aeb0` · A6 (конфигуратор align+format) `3cfce01`.
> Пример PuT обновлён на `format` вместо `ROUND` (`caccb69` в репозитории конфигурации).
> Все тесты зелёные, `onebase.exe` пересобран, `check --project PuT` без ошибок.

## Task A1: Сворачиваемые блоки отчёта (параметры / график / данные)

**Files:**
- Modify: `internal/ui/templates.go` (шаблон `page-report`, ≈ 2081–2145)

Обернуть три блока (`card`) в `<details class="report-block" open><summary>…</summary>…</details>`.
В проекте уже есть паттерн `<details>/<summary>` с запоминанием состояния в `localStorage`
(см. nav-секции ≈ строка 727: слушатель `toggle` пишет в `localStorage`). Переиспользовать.

- [ ] **Step 1:** В `page-report` заменить три `<div class="card">` (параметры, график,
  данные) на `<details class="card report-block" data-block="params|chart|data" open>`
  с `<summary>` («Параметры» / «Диаграмма» / «Данные»). Перенести содержимое внутрь.
- [ ] **Step 2:** Добавить JS (рядом с существующим JS отчёта): для каждого
  `details.report-block` восстановить состояние из `localStorage['rb-'+name+'-'+dataBlock]`
  и писать по событию `toggle` (скопировать паттерн из nav-секций ≈ 2725–2730).
- [ ] **Step 3:** Ручная проверка: открыть отчёт, свернуть «Диаграмму», обновить
  страницу — блок остаётся свёрнутым.
- [ ] **Step 4:** Commit: `feat(report): сворачиваемые блоки отчёта (параметры/график/данные)`

> Тест: ручной (шаблонный JS). Структурный тест не обязателен — изменения чисто разметочные.

## Task A2: Свернуть/развернуть все группы

**Files:**
- Modify: `internal/ui/templates.go` (блок `ComposedHTML`, ≈ 2141–2160 — кнопки + JS)

- [ ] **Step 1:** Над таблицей `ComposedHTML` добавить панель:
  `<div class="rc-toolbar"><button type="button" id="rc-expand">Развернуть всё</button>
  <button type="button" id="rc-collapse">Свернуть всё</button></div>`.
- [ ] **Step 2:** JS: `rc-collapse` — для всех `tr.grp` выставить состояние «свёрнуто»:
  скрыть все `[data-parent]`/вложенные `[data-group^=…]`, заменить `▼`→`▶` в первой ячейке.
  `rc-expand` — обратное (показать все, `▶`→`▼`). Переиспользовать селекторную логику
  существующего обработчика клика по `tr.grp` (≈ 2148–2158), вынеся её в функцию
  `rcSetOpen(tr, open)`.
- [ ] **Step 3:** Ручная проверка на демо PuT (отчёт «Валовая прибыль (СКД)»): кнопки
  сворачивают/разворачивают все уровни.
- [ ] **Step 4:** Commit: `feat(report): кнопки «развернуть/свернуть всё» в СКД`

## Task A3: Выравнивание значений по колонкам

Добавляет управление выравниванием для показателей и колонки группировок.
По умолчанию: показатели — вправо (как сейчас), группировки — влево.

**Files:**
- Modify: `internal/report/report.go` (поле `Align` в `Measure`)
- Modify: `internal/ui/report_compose_render.go` (`measureAlign`, применение в ячейках)
- Modify: `internal/configcheck/check.go` (валидация значения `align`)
- Test: `internal/report/report_composition_test.go`, `internal/ui/report_compose_render_test.go`

- [ ] **Step 1 (тест парсинга):** В `report_composition_test.go` добавить тест: YAML с
  `measures: [{ field: Сумма, agg: sum, align: center }]` → `Measure.Align == "center"`.
- [ ] **Step 2:** Запустить — FAIL (поля нет). `go test ./internal/report/ -run TestComposition -v`
- [ ] **Step 3:** В `report.go` добавить в `Measure`: `Align string \`yaml:"align"\`` // left|right|center ("" = right)
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5 (тест рендера):** В `report_compose_render_test.go` тест: measure с
  `Align: "center"` → в HTML ячейки показателя `text-align:center`; measure без Align →
  `text-align:right` (или класс `num`).
- [ ] **Step 6:** Запустить — FAIL.
- [ ] **Step 7:** В `report_compose_render.go` добавить:
```go
func measureAlign(m report.Measure) string {
    switch m.Align {
    case "left", "center":
        return "text-align:" + m.Align
    default:
        return "text-align:right"
    }
}
```
  Применить в ячейках показателя (`writeGroup`, `writeDetail`, итог, шапка `<th>`):
  объединить с уже вычисляемым `cssOf(...)` через `;`. Заголовок группировок — влево.
- [ ] **Step 8:** Запустить рендер-тесты — PASS. Прогнать весь `./internal/ui/...`.
- [ ] **Step 9 (валидация):** В `CheckReportComposition` (`check.go`) добавить проверку:
  `m.Align ∈ {"", "left", "right", "center"}`, иначе issue «неизвестное выравнивание».
  Тест в `internal/configcheck/composition_check_test.go`.
- [ ] **Step 10:** Конфигуратор: в `parseCompositionForm`/вкладке «Структура» добавить
  селектор align на показатель (`internal/launcher/report_composition_form.go` +
  `configurator_tmpl.go`). Тест в `report_composition_form_test.go`.
- [ ] **Step 11:** Commit: `feat(report): выравнивание значений показателей (left/right/center)`

## Task A4: Форматирование чисел (разрядность, знаки, формат денег/процентов)

**Files:**
- Modify: `internal/report/report.go` (поле `Format` в `Measure`)
- Create: `internal/report/compose/format.go` (чистая функция форматирования)
- Modify: `internal/ui/report_compose_render.go` (`fmtVal` → учёт формата показателя)
- Test: `internal/report/compose/format_test.go`

Формат — строка: `"#,##0.00"` (разрядность + 2 знака), `"#,##0"` (целое с разрядкой),
`"0.0%"` (процент), `""` (как сейчас — сырой `decimal.String()`).

- [ ] **Step 1 (тест):** В `format_test.go`:
```go
func TestFormatNumber(t *testing.T) {
    cases := []struct{ in, format, want string }{
        {"1234567.5", "#,##0.00", "1 234 567,50"},
        {"1234567", "#,##0", "1 234 567"},
        {"0.1234", "0.0%", "12,3%"},
        {"42", "", "42"},
    }
    for _, c := range cases {
        d, _ := decimal.NewFromString(c.in)
        if got := FormatNumber(d, c.format); got != c.want {
            t.Errorf("FormatNumber(%s,%q)=%q want %q", c.in, c.format, got, c.want)
        }
    }
}
```
  (разделитель разрядов — неразрывный пробел ` `, десятичный — запятая; формат RU).
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/report/compose/ -run TestFormatNumber -v`
- [ ] **Step 3:** Реализовать `FormatNumber(d decimal.Decimal, format string) string` в
  `format.go`: распознать наличие `,` (разрядность), число знаков после `.`, суффикс `%`
  (умножить на 100, добавить «%»). Без формата — `d.String()`.
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** В `report.go` добавить `Format string \`yaml:"format"\`` в `Measure`.
- [ ] **Step 6 (рендер):** В `report_compose_render.go` ввести `fmtMeasure(v any, m report.Measure) string`:
  если `m.Format != ""` и значение числовое — `FormatNumber(toDecimal(v), m.Format)`,
  иначе `fmtVal(v)`. Применить во всех ячейках показателей (группы, детали, подытоги, итог).
  (Импортировать `compose.FormatNumber` либо экспортировать обёртку.)
- [ ] **Step 7:** Рендер-тест: measure с `Format:"#,##0.00"` и значением `12333.32` →
  HTML содержит `12 333,32`. Запустить весь `./internal/ui/...` — PASS.
- [ ] **Step 8:** Конфигуратор: поле «Формат» на показатель (необязательное, с подсказкой).
- [ ] **Step 9:** Commit: `feat(report): форматирование чисел показателей (разрядность/знаки/%)`

> После A4 пример PuT можно упростить: убрать `ROUND(...,2)` из запроса и задать
> `format: "#,##0.00"` у показателей (формат скроет float-артефакт при выводе).
> Сделать отдельным коммитом в репозитории конфигурации.

## Task A5: Залипающий заголовок таблицы (sticky header)

**Files:**
- Modify: `internal/ui/templates.go` (CSS-блок, добавить правила `.report-composed`)

- [ ] **Step 1:** В CSS добавить:
```css
.report-composed{border-collapse:collapse;width:100%}
.report-composed thead th{position:sticky;top:38px;background:#fff;z-index:5}
.report-composed td,.report-composed th{padding:6px 10px;border-bottom:1px solid #f1f5f9}
.report-composed td.num{text-align:right;font-variant-numeric:tabular-nums}
```
  (`top:38px` — высота `.topbar`; свериться с `.topbar{height:38px}` ≈ 396.)
- [ ] **Step 2:** Ручная проверка: длинный отчёт прокручивается, шапка остаётся видимой.
- [ ] **Step 3:** Commit: `feat(report): залипающий заголовок таблицы СКД`

### Проверка этапа A
- [ ] `go test ./internal/report/... ./internal/ui/... ./internal/configcheck/... ./internal/launcher/...` — всё зелёное.
- [ ] Сборка `onebase.exe`, `check --project PuT` без ошибок.
- [ ] Ручная проверка отчёта «Валовая прибыль (СКД)» на демо-базе: блоки сворачиваются,
  кнопки «всё» работают, числа отформатированы, шапка залипает.

---

# Этап B — Весомые функции (средняя сложность)

> ✅ **ВЫПОЛНЕНО** (ветка `feature/59-report-composition`, subagent-driven: свежий
> implementer на задачу + двухстадийное ревью между задачами):
> B1 (Excel-экспорт с группами/итогами) `2bb815e` (+`e8094c1` тест detail-режима) ·
> B2 (вычисляемые показатели/проценты, `Evaluator.EvalNum` + двухпроходный `aggregate`)
> `aaaccd2` (+`7b96c6f` тесты `EvalNum`/многоуровневый Expr) ·
> B3 (расшифровка детали в документ, `detail_link`/`detail_entity`) `a409009`
> (+`138e472` не строить ссылку без сущности перехода).
> Финальное ревью этапа + `9e709ae` (пустая ячейка вместо `0` для неопределённого
> показателя в Excel — консистентность с HTML).
> Все тесты зелёные (`report`/`compose`/`ui`/`configcheck`/`launcher`), `go build ./...`
> чист, `onebase.exe` пересобран, `check --project PuT` без ошибок.
>
> **Тех-долг (сознательно вне scope B, для следующих шагов / этапа C):**
> 1. Расшифровка `detail_link` (B3) конфликтует с `resolveUUIDsInReport` (заменяет
>    UUID→наименование в строках ДО `compose.Compose`, см. `handlers_reports.go`):
>    ссылка получит имя вместо UUID. Решение — исключить колонку `DetailLink` из
>    резолва ЛИБО перенести резолв после `compose`.
> 2. Excel-деталь в первой ячейке пока даёт только отступ (значение `detail_link`/
>    идентификатор не выводится) — добавить при доработке расшифровки.
> 3. Добавить в пример PuT демо `Expr`-показателя «Рентабельность, %» и `detail_link`
>    (отдельный коммит в репозитории конфигурации).

## Task B1: Экспорт СКД в Excel с группами и итогами

Сейчас `reportExcel` (`handlers_reports.go:321`) игнорирует `composition` и выгружает
сырые плоские строки. Нужно: при `Composition!=nil` строить тот же `compose.Result` и
выгружать с отступами групп, подытогами, общим итогом и (по возможности) стилями.

**Files:**
- Modify: `internal/ui/handlers_reports.go` (`reportExcel` — ветка composition)
- Create: `internal/ui/report_compose_excel.go` (`composedToSheet(res, spec) ([][]any, опц. стили)`)
- Modify: `internal/excel/…` (при необходимости — экспорт с уровнями/жирностью; проверить API `ExportList`)
- Test: `internal/ui/report_compose_excel_test.go`

- [ ] **Step 1 (тест):** В `report_compose_excel_test.go`: на готовом `compose.Result`
  (2 группы, подытоги, grand) `composedRows(res, spec)` возвращает строки в порядке
  «группа → [дети/детали] → подытог … → ВСЕГО», первая колонка с отступом-префиксом по
  уровню, ячейки показателей — числа. Проверить количество строк и содержимое ключевых.
- [ ] **Step 2:** Запустить — FAIL.
- [ ] **Step 3:** Реализовать `composedRows(res *compose.Result, spec *report.Composition) [][]any`
  (обход дерева, как `renderComposedTable`, но в `[][]any`). Шапка: `Groupings join` + `measureTitle`.
- [ ] **Step 4:** Запустить — PASS.
- [ ] **Step 5:** В `reportExcel`: если `rep.Composition != nil` → `compose.Compose` →
  `composedRows` → `excel.ExportList(headers, rows)`; иначе старый плоский путь.
- [ ] **Step 6:** Ручная проверка: выгрузить отчёт-СКД в Excel — есть группы/итоги.
- [ ] **Step 7:** Commit: `feat(report): экспорт СКД в Excel с группами и итогами`

> PDF-экспорт — отдельной задачей при необходимости (через существующий рендер в
> `internal/sheet`/`pdf`). В объём v2 не входит, если нет явного запроса.

## Task B2: Вычисляемые показатели и проценты

Показатель с выражением вместо агрегата: вычисляется ПОСЛЕ агрегации обычных
показателей, по их подытогам/итогам (на каждом уровне и в grand). Неаддитивные
величины (рентабельность, % от итога) считаются корректно на каждом уровне.

**Files:**
- Modify: `internal/report/report.go` (поле `Expr` в `Measure`)
- Modify: `internal/report/compose/compose.go` (`aggregate` → пост-вычисление Expr-показателей)
- Modify: `internal/configcheck/check.go` (компиляция `Expr` как выражения)
- Test: `internal/report/compose/compose_test.go`

Контракт: `Measure{Field, Agg, Title, Expr, ...}`. Если `Expr != ""` — это вычисляемый
показатель: его значение = результат DSL-выражения `Expr`, где переменные — это
**значения уже агрегированных показателей** данной группы (по их `Field`).
Пример: `{ field: Рентабельность, expr: "ВаловаяПрибыль / Выручка * 100", format: "0.0%", title: "Рент., %" }`.
Вычисление через тот же `Evaluator` (нужен метод вычисления числа, не только bool).

- [ ] **Step 1 (расширить Evaluator):** В `compose.go` добавить в интерфейс `Evaluator`
  метод `EvalNum(expr string, row Row) (decimal.Decimal, bool, error)`. В `interpEvaluator`
  (`report_eval.go`) реализовать (исполнить выражение, привести результат к decimal).
  Обновить фейковые Evaluator в тестах.
- [ ] **Step 2 (тест):** В `compose_test.go`: measures `[{Сумма,sum},{Рент,expr:"Сумма*2"}]`,
  группа с Subtotal Сумма=100 → Subtotal Рент=200. (Фейковый Evaluator считает `Сумма*2`.)
- [ ] **Step 3:** Запустить — FAIL.
- [ ] **Step 4:** В `aggregate(rows, measures, ev)`: сначала обычные показатели (как
  сейчас), затем для `Expr != ""` вычислить `ev.EvalNum(m.Expr, out)` и записать `out[m.Field]`.
  Прокинуть `ev` в `aggregate` (сейчас не принимает) — обновить вызовы в `buildGroups`/`ComposeN`.
  Expr-показатели **не** суммируются по строкам, поэтому в `aggMeasure` их пропускать.
- [ ] **Step 5:** Запустить — PASS. Прогнать весь `./internal/report/compose/`.
- [ ] **Step 6 (валидация):** В `CheckReportComposition` для `Expr != ""` компилировать
  выражение (как `when`) — issue при ошибке. Агрегат для Expr-показателя не требуется.
- [ ] **Step 7:** Конфигуратор: режим показателя «выражение» (поле Expr) во вкладке «Структура».
- [ ] **Step 8:** Commit: `feat(report): вычисляемые показатели (выражения, проценты)`

## Task B3: Расшифровка детальной строки в документ

Клик по детальной строке открывает исходный документ. Запрос отчёта должен вернуть
колонку-идентификатор (UUID регистратора/ссылки); `composition.detail_link` указывает
поле и сущность для перехода.

**Files:**
- Modify: `internal/report/report.go` (поля `DetailLink` / `DetailEntity` в `Composition`)
- Modify: `internal/ui/report_compose_render.go` (`writeDetail` → ссылка на документ)
- Modify: `internal/configcheck/check.go` (валидация: `DetailLink` есть в колонках запроса — когда реализуют сверку колонок)
- Test: `internal/ui/report_compose_render_test.go`

- [ ] **Step 1 (тест):** detail-строка с `DetailLink:"Регистратор"` и значением-UUID →
  HTML содержит `<a href="/ui/document/<entity>/<uuid>">` или маркер `→` со ссылкой.
- [ ] **Step 2:** Запустить — FAIL.
- [ ] **Step 3:** В `report.go`: `DetailLink string \`yaml:"detail_link"\``,
  `DetailEntity string \`yaml:"detail_entity"\`` в `Composition`.
- [ ] **Step 4:** В `writeDetail`: если `spec.DetailLink != ""` и в `d.Values` есть это
  поле — в первую (сейчас пустую) ячейку вставить ссылку «→» на
  `/ui/document/<DetailEntity>/<value>`.
- [ ] **Step 5:** Запустить — PASS.
- [ ] **Step 6:** Конфигуратор: поля «Поле-ссылка» и «Сущность» во вкладке «Структура».
- [ ] **Step 7:** Commit: `feat(report): расшифровка детальной строки в документ`

### Проверка этапа B
- [ ] Все тесты зелёные; сборка; `check --project PuT`.
- [ ] Excel-выгрузка отчёта-СКД содержит группы/итоги.
- [ ] Добавить в пример PuT вычисляемый показатель «Рентабельность, %» и проверить итоги
  по уровням (отдельный коммит в конфигурации).

---

# Этап C — Стратегические функции (высокая сложность)

## Task C1: Кросс-таблица (pivot)

> ✅ **ВЫПОЛНЕНО** (ветка `feature/68-cross-table`, TDD): ядро `compose.ComposeCross`
> (`cross.go` + `cross_test.go`: базовая таблица, потолок, оформление ячеек, сортировка
> колонок, многоуровневые строки с подытогами, несколько показателей, Expr-показатели,
> «дырки»); поле `Composition.Columns`; рендер `renderCrossTable` (`report_cross_render.go`)
> и выгрузка в Excel `crossSheetRows` (`report_cross_excel.go`); ветки кросс-режима в
> `runReport`/`reportExcel`; валидация `columns` в `CheckReportComposition` (несовместимость
> с `detail`, дубль измерения строки/колонки); конфигуратор — зона «Колонки (кросс-таблица)»
> во вкладке «Структура» (+переводы `en.json`). Тесты `report`/`ui`/`configcheck`/`launcher`
> зелёные, `go build ./...` и `go vet ./...` чисты.
>
> **Осознанные отклонения от дизайна плана:**
> 1. Результат — единое дерево узлов `*CrossRow` (каждый узел несёт `Cells` по колонкам),
>    а не отдельные `RowTree []*Group` + плоский `Rows`: естественнее для подытогов по
>    строкам и многоуровневости.
> 2. Диспетчеризация — явный вызов `ComposeCross` из обработчика по `len(Columns)>0`
>    (вариант «проще» из Step 5); `Compose` не трогаем.
> 3. Многоуровневые колонки (`len(columns)>1`) рендерятся плоской подписью пути «A / B»,
>    без colspan-шапки (основной кейс — одно измерение в колонки). colspan-шапка — задел.
> 4. Условное оформление в кросс-режиме — поячеечно (по агрегатам пути); график в
>    кросс-режиме отключён.
> 5. Добавлена выгрузка кросс-таблицы в Excel (в Step 7 явно не требовалась) — для
>    консистентности HTML/Excel.

**Режимы взаимоисключающие.** Если `composition.columns` НЕ пуст — режим кросс-таблицы
(измерения в строки + измерения в колонки, на пересечении — показатели). Если `columns`
пуст — обычная многоуровневая группировка по строкам (как сейчас). **По умолчанию —
обычная.** Условное оформление, форматирование и выравнивание из этапа A применяются
к ячейкам кросс-таблицы.

**Files:**
- Modify: `internal/report/report.go` (`Columns []string` в `Composition`)
- Create: `internal/report/compose/cross.go` (`ComposeCross(rows, spec, ev) (*CrossResult, error)`)
- Create: `internal/report/compose/cross_test.go`
- Create: `internal/ui/report_cross_render.go` (`renderCrossTable(cr, spec) template.HTML`)
- Create: `internal/ui/report_cross_render_test.go`
- Modify: `internal/report/compose/compose.go` (`Compose` диспетчеризует по `len(spec.Columns)`)
- Modify: `internal/ui/handlers_reports.go` (`runReport` — ветка кросс-режима)
- Modify: `internal/configcheck/check.go` (валидация полей `columns`)
- Modify: `internal/ui/report_compose_render.go` (`alignRowKeys` уже учитывает поля spec —
  добавить `Columns` в список целевых имён в `compose.go:alignRowKeys`)

**Дизайн структур (`cross.go`):**
```go
// Колонка кросс-таблицы: путь значений по измерениям columns + показатель.
type CrossCol struct {
    Path    []any  // значения по columns (для многоуровневых колонок)
    Measure string // Field показателя
    Title   string // подпись (значение + название показателя при >1 показателе)
}
type CrossRow struct {
    Group  *Group          // строковая группа (как в обычном режиме, верхний уровень/лист)
    Cells  map[string]any  // ключ = colKey(CrossCol) → агрегированное значение
    Styles map[string]report.CellStyle
}
type CrossResult struct {
    Cols     []CrossCol      // упорядоченный список колонок (значения × показатели)
    Rows     []CrossRow      // строки (листовые группы или дерево — см. ниже)
    RowTree  []*Group        // строковые группы (для отступов/подытогов по строкам)
    RowTotal map[string]any  // итоги по колонкам (нижняя строка «ВСЕГО»)
    Capped   bool
}
```

**Алгоритм `ComposeCross`:**
1. `rows = alignRowKeys(rows, spec)`; применить потолок строк (как в `ComposeN`).
2. Собрать уникальные значения колонок: пройти строки, для каждой собрать `Path` по
   `spec.Columns` → упорядоченный набор уникальных путей (стабильный порядок появления
   или сортировка). Для каждого пути × каждого показателя — один `CrossCol`.
3. Сгруппировать строки по `spec.Groupings` (переиспользовать `buildGroups` для дерева
   строк; листовые группы = строки кросс-таблицы). На каждой листовой группе для каждой
   `CrossCol` агрегировать показатель по подмножеству строк с совпадающим `Path`
   (агрегация через тот же `aggMeasure`/decimal).
4. Итоги: `RowTotal[colKey]` = агрегат показателя по всем строкам колонки.
5. Условное оформление ячеек — через `evalStyles` по значениям ячеек строки (как в обычном режиме).

- [ ] **Step 1 (тест ядра):** В `cross_test.go`: строки
  `{Товар, Месяц, Сумма}` × 2 товара × 2 месяца; spec `Groupings:[Товар] Columns:[Месяц]
  Measures:[{Сумма,sum}]`. Проверить: `len(cr.Cols)==2` (по месяцам), строка «ТоварA» имеет
  `Cells[colKey(Янв)] == сумма`, `RowTotal` по колонкам верный.
- [ ] **Step 2:** Запустить — FAIL. `go test ./internal/report/compose/ -run TestCross -v`
- [ ] **Step 3:** Реализовать `ComposeCross` по алгоритму выше + хелпер
  `colKey(path []any, measure string) string` (стабильный строковый ключ).
- [ ] **Step 4:** Запустить — PASS. Добавить тесты: многоуровневые строки; несколько
  показателей (колонки = значения × показатели); пустые значения колонок; потолок строк.
- [ ] **Step 5 (диспетчер):** В `compose.go` `Compose`: `if len(spec.Columns) > 0 { ... }`
  — вернуть признак кросс-режима. Вариант: оставить `Compose` для обычного и вызывать
  `ComposeCross` явно из обработчика (проще). Тест диспетчеризации.
- [ ] **Step 6 (рендер):** В `report_cross_render.go` `renderCrossTable(cr, spec)`:
  `<thead>` — первый столбец (строковые измерения) + по колонке на `cr.Cols` (с
  многоуровневой шапкой, если `len(Columns)>1`); `<tbody>` — строки с отступами по
  дереву, подытоги, нижняя строка «ВСЕГО» из `RowTotal`. Применить `cssOf`/`measureAlign`/
  `fmtMeasure` (этап A). Тест в `report_cross_render_test.go` (HTML содержит шапку колонок,
  значения ячеек, «ВСЕГО»).
- [ ] **Step 7 (обработчик):** В `runReport` (`handlers_reports.go`): если
  `rep.Composition != nil && len(rep.Composition.Columns) > 0` → `ComposeCross` →
  `renderCrossTable` (в `ComposedHTML`); график для кросс-режима — по согласованию
  (в v2 можно отключить chart в кросс-режиме). Иначе — текущий путь.
- [ ] **Step 8 (валидация):** В `CheckReportComposition`: поля `columns` валидны (как
  groupings); кросс-режим несовместим с `detail:true` (issue или игнор detail) — задокументировать.
- [ ] **Step 9 (конфигуратор):** Во вкладке «Структура» — зона «Колонки (кросс-таблица)»:
  поля-измерения в колонки; подсказка «если заполнено — отчёт строится кросс-таблицей».
  `parseCompositionForm`/`configurator_tmpl.go` + тест.
- [ ] **Step 10:** Ручная проверка на PuT: новый отчёт «Продажи по месяцам» —
  `Groupings:[Номенклатура] Columns:[Месяц]` (поле периода из регистра). Коммит примера —
  в репозитории конфигурации отдельно.
- [ ] **Step 11:** Commit: `feat(report): кросс-таблица (pivot) — измерения в колонки`

## Task C2: Варианты компоновки (несколько компоновок по одним данным)

> ✅ **ВЫПОЛНЕНО** (ветка `feature/68-pivot`, TDD): типы `ReportVariant` +
> `Report.Variants` и парсинг (`report.go` + `report_composition_test.go`); метод
> `Report.ActiveComposition(name)` (выбор варианта, fallback на основной); рантайм-выбор
> по параметру `__variant` в `runReport` и `reportExcel` (`handlers_reports.go`);
> селектор вариантов на форме отчёта (`page-report` в `templates.go`: автосабмит при
> смене, сохранение выбора, Excel-ссылка с `__variant` через хелпер `variantQuery`);
> форма теперь показывается и при наличии только вариантов (без параметров); валидация
> всех вариантов в `CheckReportComposition` (рефакторинг в замыкание `checkComp`,
> сообщения с префиксом `вариант "Имя": …`); переводы `en.json` (`Вариант`/`Основной`).
> Тесты `report`/`ui`/`configcheck`/`launcher` зелёные, `go build ./...` и `go vet`
> чисты, `check` на временной конфигурации с вариантами — без ошибок; битый агрегат в
> варианте ловится с указанием имени варианта.
>
> **Осознанные отклонения от дизайна плана:**
> 1. Полноценный визуальный конструктор управления списком вариантов (Step 6) **не
>    реализован** — план допускает упрощение. Варианты задаются в YAML; ключевой риск
>    (конструктор `composition` затирает `variants` при сохранении) исключён: правка
>    идёт точечно через `setYAMLMapField`, остальные поля сохраняются. Закреплено
>    тестом `TestApplyReportCompositionPreservesVariants`.
> 2. Селектор автосабмитит форму при смене варианта (UX как в 1С) — `__variant`
>    читается из `r.FormValue` (POST формы) и из GET-query (Excel).
> 3. C3 (рантайм-настройки пользователя) вынесен в отдельный план — см.
>    `Plans/70-report-runtime-settings.md`.

Один запрос — несколько именованных компоновок (как «варианты отчёта» 1С). На форме
отчёта — выпадающий выбор варианта; по умолчанию — основной `composition`.

**Files:**
- Modify: `internal/report/report.go` (`Variants []ReportVariant` в `Report`)
- Modify: `internal/ui/handlers_reports.go` (`runReport`/`reportRun` — выбор варианта по параметру)
- Modify: `internal/ui/templates.go` (`page-report` — выпадающий список вариантов)
- Modify: `internal/configcheck/check.go` (валидация каждого варианта как `Composition`)
- Test: `internal/report/report_composition_test.go`, `internal/ui` (выбор варианта)

**Дизайн:**
```go
type ReportVariant struct {
    Name        string       `yaml:"name"`
    Composition *Composition `yaml:"composition"`
}
// Report += Variants []ReportVariant `yaml:"variants"`
```
YAML:
```yaml
composition: {...}      # основной (вариант по умолчанию, name "Основной")
variants:
  - name: "По складам"
    composition: { groupings: [Склад, Номенклатура], ... }
  - name: "Кросс по месяцам"
    composition: { groupings: [Номенклатура], columns: [Месяц], ... }
```

- [ ] **Step 1 (тест парсинга):** YAML с `variants` → `len(rep.Variants)==N`, имена и
  composition разобраны. Тест в `report_composition_test.go`.
- [ ] **Step 2:** FAIL → добавить типы в `report.go` → PASS.
- [ ] **Step 3 (выбор варианта):** В `runReport` определить активную компоновку: параметр
  запроса `__variant` (имя) → соответствующий `Composition`; пусто → основной `composition`.
  Вынести в `func (r *Report) ActiveComposition(name string) *Composition`. Тест на выбор.
- [ ] **Step 4 (UI):** В `page-report` — если `len(Variants)>0`, над кнопкой «Сформировать»
  выпадающий `<select name="__variant">` (основной + варианты), сохранять выбранное.
- [ ] **Step 5 (валидация):** `CheckReportComposition` валидирует основной и каждый
  вариант (выделить общую функцию проверки одной `Composition`).
- [ ] **Step 6:** Конфигуратор: управление вариантами — отдельный список компоновок
  (минимально: имя + переключение между ними в конструкторе). Может быть упрощено.
- [ ] **Step 7:** Ручная проверка на PuT: у отчёта 2 варианта, переключение меняет вид.
- [ ] **Step 8:** Commit: `feat(report): варианты компоновки отчёта (несколько по одним данным)`

## Task C3: Рантайм-настройки пользователя (опционально, крупная фича)

> 📋 **РАСПИСАНО ОТДЕЛЬНЫМ ПЛАНОМ** — `Plans/70-report-runtime-settings.md`
> (создан после стабилизации C1/C2). Ниже — исходная фиксация направления; детальная
> пошаговая разбивка с TDD — в плане 70.

Менять группировки/отборы/показатели на форме отчёта без правки конфигурации (суть
полноценной 1С-СКД). **Очень высокая сложность** — оформить отдельным планом
после стабилизации C1/C2. Здесь только зафиксировано как зависимость и направление:
- хранение пользовательских настроек (по пользователю/отчёту) — таблица `_settings`
  или новая (см. паттерн настроек конфигуратора);
- UI: панель «Настройки» на форме отчёта (drag-n-drop полей, как в конструкторе);
- переиспользование `parseCompositionForm` для построения `Composition` из формы рантайма.

> Не реализовывать в рамках этого плана без отдельного брейнсторма/плана.

### Проверка этапа C
- [ ] Все тесты зелёные; сборка; `check --project PuT`.
- [ ] Кросс-таблица «Продажи по месяцам» строится; обычные отчёты не сломаны (режим по умолчанию).
- [ ] Переключение вариантов работает.

---

## Совместимость и принципы (соблюдать на всех этапах)
- Все новые поля `composition`/`Measure`/`Report` — опциональны; `nil`/`""`/пустой срез =
  прежнее поведение. Старые отчёты (плоские и СКД v1) не меняются.
- Ядро вычислений — в `internal/report/compose` (чистые функции, тесты без БД).
- Имена колонок результата — нижний регистр; новые поля-имена добавлять в `alignRowKeys`.
- Каждая фича — парная поддержка в конфигураторе (вкладки уже есть).
- Деньги — `shopspring/decimal`; не считать в `float64`.

## Self-review (выполнено при составлении)
- **Покрытие:** все пункты обсуждения отражены — сворачиваемые блоки (A1), развернуть
  всё (A2), выравнивание (A3), форматирование (A4), sticky (A5), экспорт с группами (B1),
  вычисляемые/проценты (B2), расшифровка (B3), кросс-таблица взаимоисключающе с обычной по
  умолчанию (C1), варианты (C2), рантайм-настройки вынесены в отдельный план (C3).
- **Типы:** `Measure` расширяется `Align`/`Format`/`Expr`; `Composition` — `Columns`/
  `DetailLink`/`DetailEntity`; `Report` — `Variants`. Имена согласованы между задачами.
- **Зависимости:** A3/A4 (выравнивание/формат) используются рендером кросс-таблицы (C1) —
  делать этап A до C. B2 требует `Evaluator.EvalNum` — добавляется в B2 и переиспользуется.
