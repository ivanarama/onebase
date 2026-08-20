# Компоновка отчётов · Stage 2 (визуальный конструктор в конфигураторе) — Implementation Plan (план 59)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** В конфигураторе отчёт собирается визуально (вкладки Структура / Оформление / График / Предпросмотр), а блок `composition` сериализуется в YAML «под капотом». Редактирование отчёта больше не теряет `composition`.

**Architecture:** Бэкенд (2a) — чистая `parseCompositionForm(url.Values) (*report.Composition, bool)` + round-trip `composition` в `configuratorSaveReport`/`cfgReport` (`internal/launcher/configurator.go`). Фронтенд (2b) — новые `obj-tab`/`obj-pane` в редакторе отчёта (`internal/launcher/configurator_tmpl.go`), формирующие поля формы `comp.*`, + динамические строки на JS. Тестируемость: backend — юнит-тесты; UI — структурный тест рендера + ручная визуальная приёмка.

**Tech Stack:** Go; `gopkg.in/yaml.v3`; embedded HTML/JS в Go-строках; тесты `testing`.

**Дизайн:** [59-report-composition-design.md](59-report-composition-design.md) (раздел «Опыт разработчика»). **Зависит от:** Stage 1 (`report.Composition`, `internal/report/compose`, рантайм-рендер — уже в `feature/59-report-composition`). **Ветка:** `feature/59-report-composition` (продолжение).

> **Контекст-бага (фиксится в 2a):** сейчас `configuratorSaveReport` гоняет YAML через
> структуру `saveReport` без поля `composition` (unmarshal→remarshal) — сохранение отчёта
> в конфигураторе **молча удаляет** существующий блок `composition`. 2a обязателен и сам по себе.

---

## Контракт полей формы (`comp.*`)

Конструктор (2b) рендерит, а бэкенд (2a) парсит:

- `comp.present` — скрытое поле «=1» (маркер, что форма несёт композицию).
- `comp.grouping.N` — поля группировок по порядку (N=0,1,…; до первого пустого).
- `comp.measure.N.field`, `comp.measure.N.agg`, `comp.measure.N.title`.
- `comp.totals.grand`, `comp.totals.subtotals` — чекбоксы («on» при включении).
- `comp.detail` — чекбокс.
- `comp.sort.N.field`, `comp.sort.N.dir`.
- `comp.cond.N.when`, `comp.cond.N.field`, `comp.cond.N.color`, `comp.cond.N.background`, `comp.cond.N.bold`, `comp.cond.N.italic`.
- `comp.chart.type` (""=нет графика | bar | line | pie), `comp.chart.category`, `comp.chart.series` (через запятую).

Семантика на сохранении: `comp.present` отсутствует → composition не трогаем (старая форма).
`comp.present` есть, но нет ни группировок, ни показателей → `composition` очищается (nil).

---

## Структура файлов

- Создать: `internal/launcher/report_composition_form.go` — `parseCompositionForm(url.Values) (*report.Composition, bool)`.
- Создать: `internal/launcher/report_composition_form_test.go` — юнит-тесты парсера.
- Изменить: `internal/launcher/configurator.go` — `saveReport.Composition` + применение в `updateReportFile`; `cfgReport.Composition` + заполнение в цикле сборки.
- Изменить: `internal/launcher/configurator_tmpl.go` — вкладки Структура/Оформление/График/Предпросмотр + JS динамических строк.
- Создать/изменить: `internal/launcher/report_builder_render_test.go` — структурный тест рендера вкладок.

---

# Stage 2a — backend round-trip

## Task 1: `parseCompositionForm` (чистый парсер)

**Files:**
- Create: `internal/launcher/report_composition_form.go`
- Test: `internal/launcher/report_composition_form_test.go`

- [ ] **Step 1: Падающий тест**

Создать `internal/launcher/report_composition_form_test.go`:

```go
package launcher

import (
	"net/url"
	"testing"
)

func TestParseCompositionForm(t *testing.T) {
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Менеджер")
	f.Set("comp.grouping.1", "Клиент")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	f.Set("comp.measure.0.title", "Сумма, ₽")
	f.Set("comp.totals.grand", "on")
	f.Set("comp.totals.subtotals", "on")
	f.Set("comp.detail", "on")
	f.Set("comp.sort.0.field", "Сумма")
	f.Set("comp.sort.0.dir", "desc")
	f.Set("comp.cond.0.when", "Сумма < 0")
	f.Set("comp.cond.0.color", "#c00")
	f.Set("comp.cond.0.bold", "on")
	f.Set("comp.chart.type", "bar")
	f.Set("comp.chart.category", "Менеджер")
	f.Set("comp.chart.series", "Сумма")

	c, present := parseCompositionForm(f)
	if !present {
		t.Fatal("present=false")
	}
	if c == nil {
		t.Fatal("composition nil")
	}
	if len(c.Groupings) != 2 || c.Groupings[1] != "Клиент" {
		t.Fatalf("groupings: %v", c.Groupings)
	}
	if len(c.Measures) != 1 || c.Measures[0].Agg != "sum" || c.Measures[0].Title != "Сумма, ₽" {
		t.Fatalf("measures: %+v", c.Measures)
	}
	if !c.Totals.Grand || !c.Totals.Subtotals || !c.Detail {
		t.Fatalf("totals/detail")
	}
	if len(c.Sort) != 1 || c.Sort[0].Dir != "desc" {
		t.Fatalf("sort: %+v", c.Sort)
	}
	if len(c.Conditional) != 1 || c.Conditional[0].Style.Color != "#c00" || !c.Conditional[0].Style.Bold {
		t.Fatalf("cond: %+v", c.Conditional)
	}
	if c.Chart == nil || c.Chart.Type != "bar" || c.Chart.Category != "Менеджер" || len(c.Chart.Series) != 1 {
		t.Fatalf("chart: %+v", c.Chart)
	}
}

func TestParseCompositionFormAbsentAndEmpty(t *testing.T) {
	if c, present := parseCompositionForm(url.Values{}); present || c != nil {
		t.Fatalf("absent: present=%v c=%v", present, c)
	}
	f := url.Values{}
	f.Set("comp.present", "1") // включён, но пусто
	c, present := parseCompositionForm(f)
	if !present || c != nil {
		t.Fatalf("empty: present=%v c=%v (ждали present=true, c=nil)", present, c)
	}
}
```

- [ ] **Step 2: Запустить — FAIL**

Run: `go test ./internal/launcher/ -run TestParseCompositionForm -v`
Expected: FAIL (нет `parseCompositionForm`).

- [ ] **Step 3: Реализовать парсер**

Создать `internal/launcher/report_composition_form.go`:

```go
package launcher

import (
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
)

// parseCompositionForm собирает report.Composition из полей формы comp.*.
// Возвращает (nil, false) если маркера comp.present нет (composition не трогаем),
// (nil, true) если включён, но пуст (composition очищается).
func parseCompositionForm(f url.Values) (*report.Composition, bool) {
	if f.Get("comp.present") == "" {
		return nil, false
	}
	c := &report.Composition{}
	for i := 0; ; i++ {
		v := strings.TrimSpace(f.Get(fieldKey("comp.grouping.", i)))
		if v == "" {
			break
		}
		c.Groupings = append(c.Groupings, v)
	}
	for i := 0; ; i++ {
		fld := strings.TrimSpace(f.Get(idxKey("comp.measure.", i, ".field")))
		if fld == "" {
			break
		}
		c.Measures = append(c.Measures, report.Measure{
			Field: fld,
			Agg:   f.Get(idxKey("comp.measure.", i, ".agg")),
			Title: strings.TrimSpace(f.Get(idxKey("comp.measure.", i, ".title"))),
		})
	}
	c.Totals.Grand = f.Get("comp.totals.grand") != ""
	c.Totals.Subtotals = f.Get("comp.totals.subtotals") != ""
	c.Detail = f.Get("comp.detail") != ""
	for i := 0; ; i++ {
		fld := strings.TrimSpace(f.Get(idxKey("comp.sort.", i, ".field")))
		if fld == "" {
			break
		}
		c.Sort = append(c.Sort, report.SortKey{Field: fld, Dir: f.Get(idxKey("comp.sort.", i, ".dir"))})
	}
	for i := 0; ; i++ {
		when := strings.TrimSpace(f.Get(idxKey("comp.cond.", i, ".when")))
		if when == "" {
			break
		}
		c.Conditional = append(c.Conditional, report.CondRule{
			When:  when,
			Field: strings.TrimSpace(f.Get(idxKey("comp.cond.", i, ".field"))),
			Style: report.CellStyle{
				Color:      strings.TrimSpace(f.Get(idxKey("comp.cond.", i, ".color"))),
				Background: strings.TrimSpace(f.Get(idxKey("comp.cond.", i, ".background"))),
				Bold:       f.Get(idxKey("comp.cond.", i, ".bold")) != "",
				Italic:     f.Get(idxKey("comp.cond.", i, ".italic")) != "",
			},
		})
	}
	if ct := strings.TrimSpace(f.Get("comp.chart.type")); ct != "" {
		var series []string
		for _, s := range strings.Split(f.Get("comp.chart.series"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				series = append(series, s)
			}
		}
		c.Chart = &report.ChartSpec{Type: ct, Category: strings.TrimSpace(f.Get("comp.chart.category")), Series: series}
	}
	// пусто → очистка composition (nil), но present=true
	if len(c.Groupings) == 0 && len(c.Measures) == 0 {
		return nil, true
	}
	return c, true
}

func fieldKey(prefix string, i int) string { return prefix + itoa(i) }
func idxKey(prefix string, i int, suffix string) string { return prefix + itoa(i) + suffix }
func itoa(i int) string { return strings.TrimLeft(string(rune('0'+i)), "") } // см. Step 3a
```

- [ ] **Step 3a: заменить `itoa` на корректную реализацию**

`itoa` выше — заглушка (ломается на i≥10). Заменить на `strconv.Itoa` и добавить импорт `"strconv"`, убрав самописный `itoa`:

```go
func fieldKey(prefix string, i int) string { return prefix + strconv.Itoa(i) }
func idxKey(prefix string, i int, suffix string) string { return prefix + strconv.Itoa(i) + suffix }
```

- [ ] **Step 4: Запустить — PASS**

Run: `go test ./internal/launcher/ -run TestParseCompositionForm -v`
Expected: PASS (оба теста).

- [ ] **Step 5: Коммит**

```bash
git add internal/launcher/report_composition_form.go internal/launcher/report_composition_form_test.go
git commit -m "feat(configurator): parseCompositionForm — форма comp.* → Composition (план 59 stage 2)"
```

---

## Task 2: round-trip `composition` в сохранении отчёта

**Files:**
- Modify: `internal/launcher/configurator.go` (`saveReport` ~2446, `updateReportFile` ~2466, `configuratorSaveReport` ~2434)
- Test: `internal/launcher/report_composition_form_test.go` (добавить)

- [ ] **Step 1: Падающий тест round-trip**

Добавить в `report_composition_form_test.go` (тест проверяет, что `applyReportComposition` обновляет/сохраняет/очищает/не трогает блок):

```go
import "gopkg.in/yaml.v3"

func TestApplyReportComposition(t *testing.T) {
	// исходный YAML с composition
	raw := []byte("name: R\nquery: \"ВЫБРАТЬ 1\"\ncomposition:\n  groupings: [Старое]\n  measures:\n    - {field: X, agg: sum}\n")

	// форма без present → composition сохраняется как было
	out, err := applyReportComposition(raw, url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Старое") {
		t.Fatalf("composition должна сохраниться без present:\n%s", out)
	}

	// форма present + новые поля → перезапись
	f := url.Values{}
	f.Set("comp.present", "1")
	f.Set("comp.grouping.0", "Новое")
	f.Set("comp.measure.0.field", "Сумма")
	f.Set("comp.measure.0.agg", "sum")
	out, _ = applyReportComposition(raw, f)
	if !strings.Contains(string(out), "Новое") || strings.Contains(string(out), "Старое") {
		t.Fatalf("composition должна перезаписаться:\n%s", out)
	}

	// форма present, пусто → composition удаляется
	f2 := url.Values{}
	f2.Set("comp.present", "1")
	out, _ = applyReportComposition(raw, f2)
	if strings.Contains(string(out), "composition") {
		t.Fatalf("composition должна очиститься:\n%s", out)
	}
}
```

- [ ] **Step 2: Запустить — FAIL**

Run: `go test ./internal/launcher/ -run TestApplyReportComposition -v`
Expected: FAIL (нет `applyReportComposition`).

- [ ] **Step 3: Реализовать `applyReportComposition` + подключить в сохранение**

В `internal/launcher/report_composition_form.go` добавить чистую функцию, которая делает round-trip композиции (отдельно от формы параметров/запроса, чтобы тестировать без HTTP):

```go
// applyReportComposition обновляет блок composition в сыром YAML отчёта по форме.
// Сохраняет существующий composition, если в форме нет comp.present.
func applyReportComposition(raw []byte, f url.Values) ([]byte, error) {
	var doc struct {
		Name        string               `yaml:"name"`
		Title       string               `yaml:"title,omitempty"`
		Params      []map[string]any     `yaml:"params,omitempty"`
		Query       string               `yaml:"query"`
		ChartProc   string               `yaml:"chart_proc,omitempty"`
		Composition *report.Composition  `yaml:"composition,omitempty"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if c, present := parseCompositionForm(f); present {
		doc.Composition = c // c==nil очищает
	}
	return yaml.Marshal(&doc)
}
```

> **Важно (TDD-наблюдение):** этот `doc` дублирует поля `saveReport` из
> `configuratorSaveReport`. Чтобы не разъезжаться, в Task 2 Step 4 интегрируем единым путём.

- [ ] **Step 4: Интегрировать в `configuratorSaveReport`**

В `internal/launcher/configurator.go`:
1. Добавить в структуру `saveReport` (~2446) поле:
   ```go
   Composition *report.Composition `yaml:"composition,omitempty"`
   ```
   (импорт `report` уже есть — `report.LoadFile`/типы используются; если нет — добавить `"github.com/ivantit66/onebase/internal/report"`.)
2. В `updateReportFile` (~2466) после `rep.ChartProc = chartProc` добавить:
   ```go
   if c, present := parseCompositionForm(r.Form); present {
       rep.Composition = c
   }
   ```
   (`r.Form` уже заполнен `r.ParseForm()` в начале обработчика.)

   Это сохраняет существующий `composition` (он попадает в `rep` при `yaml.Unmarshal`,
   т.к. поле теперь есть), и перезаписывает/очищает по форме конструктора.

   `applyReportComposition` из Step 3 остаётся как тестируемое ядро логики round-trip
   (его поведение покрыто Task 2 Step 1); `updateReportFile` использует тот же
   `parseCompositionForm`. Если предпочитаешь единый путь — вызвать `applyReportComposition`
   внутри `updateReportFile` вместо ручной сборки `saveReport`; в этом плане оставляем
   `saveReport` (минимальная правка), а `applyReportComposition` гарантирует семантику тестами.

- [ ] **Step 5: Запустить — PASS + сборка**

Run: `go test ./internal/launcher/ -run 'TestApplyReportComposition|TestParseCompositionForm' -v && go build ./internal/...`
Expected: PASS; компиляция чистая.

- [ ] **Step 6: Коммит**

```bash
git add internal/launcher/configurator.go internal/launcher/report_composition_form.go internal/launcher/report_composition_form_test.go
git commit -m "fix(configurator): round-trip блока composition при сохранении отчёта (план 59 stage 2)"
```

---

## Task 3: пред-заполнение — `cfgReport.Composition`

**Files:**
- Modify: `internal/launcher/configurator.go` (`cfgReport` ~186, цикл сборки ~757)

- [ ] **Step 1: Добавить поле и заполнение**

1. В `cfgReport` (~186) добавить:
   ```go
   Composition *report.Composition
   ```
2. В цикле сборки (~757) при создании `rv`:
   ```go
   rv := cfgReport{Name: rep.Name, Title: rep.Title, Query: rep.Query, ChartProc: rep.ChartProc, Composition: rep.Composition}
   ```
   (`rep` — элемент `proj.Reports`, тип `*report.Report`, поле `Composition` уже есть из Stage 1.)

- [ ] **Step 2: Сборка**

Run: `go build ./internal/...`
Expected: компиляция чистая (поле станет доступно в шаблоне как `.Composition` в Task 4–6).

- [ ] **Step 3: Коммит**

```bash
git add internal/launcher/configurator.go
git commit -m "feat(configurator): прокинуть Composition в cfgReport для пред-заполнения (план 59 stage 2)"
```

---

# Stage 2b — UI конструктора (configurator_tmpl.go)

> Шаблон — Go-строка в бэктиках. **Не вводить бэктики** в добавляемый HTML/JS.
> Вкладки используют существующий механизм `cfgObjTab(this,'paneId')` + классы
> `obj-tab`/`obj-pane`. Динамические строки — по образцу `repAddParam`/`repReindex`.
> Все вкладки внутри той же `<form action=".../configurator/report">`, поэтому их поля
> уходят в `configuratorSaveReport` (Task 2). Каждая вкладка с композицией рендерит
> скрытое `<input type="hidden" name="comp.present" value="1">` (один раз на форму).

## Task 4: вкладка «Структура» (группировки, показатели, итоги, сортировка)

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (блок редактора отчёта ~5181–5237: добавить таб и пейн)
- Test: `internal/launcher/report_builder_render_test.go`

- [ ] **Step 1: Структурный падающий тест**

Создать `internal/launcher/report_builder_render_test.go` — проверяет, что у отчёта с
композицией отрендерены вкладки и поля. Использовать существующий путь рендера
конфигуратора (как в `configurator_tabs_render_test.go` — открыть его, повторить паттерн
вызова рендера/шаблона). Тест ассертит наличие подстрок:
`comp.present`, `comp.grouping.0` (с предзаполнением из Composition), `Структура`,
`Оформление`, `График`, `obj-tab`.

```go
package launcher

import "strings"
import "testing"

func TestReportBuilderRender(t *testing.T) {
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	})
	for _, want := range []string{"comp.present", "comp.grouping.0", "Структура", "Оформление", "График", "obj-tab"} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}
}
```

> Хелпер (по образцу `renderTabTree` в `configurator_tabs_render_test.go`):
> ```go
> func renderConfiguratorReport(t *testing.T, comp *report.Composition) string {
> 	t.Helper()
> 	data := &configuratorData{
> 		Base: &Base{ID: "b", Name: "Т", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
> 		Reports: []cfgReport{{Name: "Прод", Composition: comp}},
> 	}
> 	var buf bytes.Buffer
> 	if err := cfgTmpl.ExecuteTemplate(&buf, "tab-tree", data); err != nil {
> 		t.Fatalf("ExecuteTemplate: %v", err)
> 	}
> 	return buf.String()
> }
> ```
> Импорты теста: `bytes`, `strings`, `testing`, `github.com/ivantit66/onebase/internal/report`.

- [ ] **Step 2: Запустить — FAIL**

Run: `go test ./internal/launcher/ -run TestReportBuilderRender -v`
Expected: FAIL (вкладок ещё нет / хелпер не находит подстроки).

- [ ] **Step 3: Добавить таб-кнопки и пейн «Структура»**

В `configurator_tmpl.go` в блоке `<div class="obj-tabs">` редактора отчёта (~5182) добавить три кнопки после существующих:

```html
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-struct-{{$rn}}')">{{t $.Lang "Структура"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-cond-{{$rn}}')">{{t $.Lang "Оформление"}}</div>
          <div class="obj-tab" onclick="cfgObjTab(this,'ot-rep-cchart-{{$rn}}')">{{t $.Lang "График"}}</div>
```

После пейна диаграммы (~5236, перед закрывающим `</div>` `.obj-editor`) добавить пейн
«Структура» (+ скрытый маркер present):

```html
        <div class="obj-pane" id="ot-rep-struct-{{$rn}}">
          <input type="hidden" name="comp.present" value="1">
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Группировки"}}
            <button type="button" class="cfg-add-btn" onclick="compAddGrouping('cg-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cg-{{$rn}}">
            {{range $i, $g := .Composition.Groupings}}
            <tr><td><input type="text" name="comp.grouping.{{$i}}" value="{{$g}}" style="width:100%"></td>
              <td><button type="button" onclick="this.closest('tr').remove();compReindex('cg-{{$rn}}','comp.grouping.')">✕</button></td></tr>
            {{end}}
          </table>
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Показатели"}}
            <button type="button" class="cfg-add-btn" onclick="compAddMeasure('cm-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cm-{{$rn}}">
            {{range $i, $m := .Composition.Measures}}
            <tr>
              <td><input type="text" name="comp.measure.{{$i}}.field" value="{{$m.Field}}"></td>
              <td><select name="comp.measure.{{$i}}.agg">
                {{$a := $m.Agg}}<option value="sum" {{if eq $a "sum"}}selected{{end}}>sum</option>
                <option value="count" {{if eq $a "count"}}selected{{end}}>count</option>
                <option value="avg" {{if eq $a "avg"}}selected{{end}}>avg</option>
                <option value="min" {{if eq $a "min"}}selected{{end}}>min</option>
                <option value="max" {{if eq $a "max"}}selected{{end}}>max</option></select></td>
              <td><input type="text" name="comp.measure.{{$i}}.title" value="{{$m.Title}}" placeholder="{{$m.Field}}"></td>
              <td><button type="button" onclick="this.closest('tr').remove();compReindexMeasures('cm-{{$rn}}')">✕</button></td>
            </tr>
            {{end}}
          </table>
          <div style="margin-top:10px">
            <label><input type="checkbox" name="comp.totals.grand" {{if and .Composition .Composition.Totals.Grand}}checked{{end}}> {{t $.Lang "Общий итог"}}</label>
            <label style="margin-left:12px"><input type="checkbox" name="comp.totals.subtotals" {{if and .Composition .Composition.Totals.Subtotals}}checked{{end}}> {{t $.Lang "Промежуточные итоги"}}</label>
            <label style="margin-left:12px"><input type="checkbox" name="comp.detail" {{if and .Composition .Composition.Detail}}checked{{end}}> {{t $.Lang "Детальные строки"}}</label>
          </div>
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Сортировка"}}
            <button type="button" class="cfg-add-btn" onclick="compAddSort('cs-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cs-{{$rn}}">
            {{range $i, $s := .Composition.Sort}}
            <tr><td><input type="text" name="comp.sort.{{$i}}.field" value="{{$s.Field}}"></td>
              <td><select name="comp.sort.{{$i}}.dir"><option value="asc" {{if eq $s.Dir "asc"}}selected{{end}}>asc</option><option value="desc" {{if eq $s.Dir "desc"}}selected{{end}}>desc</option></select></td>
              <td><button type="button" onclick="this.closest('tr').remove();compReindexSort('cs-{{$rn}}')">✕</button></td></tr>
            {{end}}
          </table>
        </div>
```

> `{{range}}` по `.Composition.Groupings` падает, если `.Composition == nil`. Обернуть
> каждый `{{range .Composition.X}}` в `{{if .Composition}}…{{end}}` ИЛИ выводить пустую
> таблицу при nil. В шаблоне Go `{{range nilSlice}}` безопасен, но `.Composition.Groupings`
> при `.Composition==nil` — паника. Поэтому каждый блок обернуть `{{with .Composition}}…{{end}}`
> и внутри использовать `.Groupings`/`.Measures`/`.Sort`/`.Totals`/`.Detail` (тогда `$rn`
> взять заранее в переменную до `{{with}}`). Чекбоксы `{{if and .Composition …}}` уже nil-безопасны.

- [ ] **Step 4: JS динамических строк**

В общий `<script>` конфигуратора (рядом с `repAddParam`/`repReindex`) добавить функции
`compAddGrouping`, `compAddMeasure`, `compAddSort`, `compReindex(tableId, prefix)`,
`compReindexMeasures`, `compReindexSort` — добавляют строку с корректным индексом и
переиндексируют `name`-атрибуты после удаления (по образцу `repReindex`). Пример каркаса:

```javascript
function compAddGrouping(id){var t=document.getElementById(id);var i=t.rows.length;
  var tr=t.insertRow();tr.innerHTML='<td><input type="text" name="comp.grouping.'+i+'" style="width:100%"></td>'+
  '<td><button type="button" onclick="this.closest(\\'tr\\').remove();compReindex(\\''+id+'\\',\\'comp.grouping.\\')">✕</button></td>';}
function compReindex(id,prefix){var t=document.getElementById(id);for(var i=0;i<t.rows.length;i++){
  var inp=t.rows[i].querySelector('input');if(inp)inp.name=prefix+i;}}
```

(Аналогично для measure/sort — переиндексировать все `name` в строке по шаблону
`comp.measure.<i>.field|agg|title`. Реализовать `compReindexMeasures`/`compReindexSort`.)

- [ ] **Step 5: Запустить — структурный тест PASS + сборка**

Run: `go build ./internal/... && go test ./internal/launcher/ -run TestReportBuilderRender -v`
Expected: компиляция (шаблон парсится через `template.Must`) и тест зелёные.

- [ ] **Step 6: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/report_builder_render_test.go
git commit -m "feat(configurator): вкладка «Структура» конструктора отчёта (план 59 stage 2)"
```

---

## Task 5: вкладка «Оформление» (условные правила)

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (добавить пейн `ot-rep-cond-{{$rn}}`)
- Test: расширить `report_builder_render_test.go`

- [ ] **Step 1: Добавить ассерт в тест**

В `TestReportBuilderRender` (или новый тест) добавить ожидание `comp.cond.0.when` при наличии
правила в Composition. Передавать в хелпер композицию с одним `Conditional`.

- [ ] **Step 2: Запустить — FAIL** (поля правила нет).

- [ ] **Step 3: Добавить пейн «Оформление»**

```html
        <div class="obj-pane" id="ot-rep-cond-{{$rn}}">
          <div class="section-hd" style="margin-top:12px">{{t $.Lang "Условное оформление"}}
            <button type="button" class="cfg-add-btn" onclick="compAddCond('cc-{{$rn}}')">+</button></div>
          <table class="fields-tbl" id="cc-{{$rn}}">
            <tr><th>{{t $.Lang "Когда"}} (DSL)</th><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Цвет"}}</th><th>{{t $.Lang "Фон"}}</th><th>Ж</th><th>К</th><th></th></tr>
            {{with .Composition}}{{range $i, $r := .Conditional}}
            <tr>
              <td><input type="text" name="comp.cond.{{$i}}.when" value="{{$r.When}}" style="width:100%"></td>
              <td><input type="text" name="comp.cond.{{$i}}.field" value="{{$r.Field}}" placeholder="{{t $.Lang "вся строка"}}"></td>
              <td><input type="color" name="comp.cond.{{$i}}.color" value="{{if $r.Style.Color}}{{$r.Style.Color}}{{else}}#000000{{end}}"></td>
              <td><input type="color" name="comp.cond.{{$i}}.background" value="{{if $r.Style.Background}}{{$r.Style.Background}}{{else}}#ffffff{{end}}"></td>
              <td><input type="checkbox" name="comp.cond.{{$i}}.bold" {{if $r.Style.Bold}}checked{{end}}></td>
              <td><input type="checkbox" name="comp.cond.{{$i}}.italic" {{if $r.Style.Italic}}checked{{end}}></td>
              <td><button type="button" onclick="this.closest('tr').remove();compReindexCond('cc-{{$rn}}')">✕</button></td>
            </tr>
            {{end}}{{end}}
          </table>
          <div class="edit-hint" style="margin-top:6px">{{t $.Lang "Цвет/фон по умолчанию (#000000/#ffffff) при сохранении игнорируются — задайте отличный."}}</div>
        </div>
```

> **Грабли `input[type=color]`:** всегда шлёт значение (#rrggbb), пустого нет. Чтобы
> «не задано» не превращалось в чёрный текст/белый фон на каждой детали, в `parseCompositionForm`
> (Task 1) трактовать `#000000`/`#ffffff` как «не задано». Добавить в парсер: при чтении
> `.color`=="#000000" → "" и `.background`=="#ffffff" → "". Внести эту правку и **дописать
> тест** в Task 1-файле (значения по умолчанию дают пустой стиль). Сделать это здесь, в Task 5.

- [ ] **Step 4: JS `compAddCond`/`compReindexCond`** — по образцу Task 4 (переиндексация всех
  `name` строки по `comp.cond.<i>.*`).

- [ ] **Step 5: Запустить — PASS + сборка** (`go build ./internal/...`, тест рендера, тесты парсера с дефолт-цветами).

- [ ] **Step 6: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/report_composition_form.go internal/launcher/report_composition_form_test.go internal/launcher/report_builder_render_test.go
git commit -m "feat(configurator): вкладка «Оформление» + дефолт-цвета как пусто (план 59 stage 2)"
```

---

## Task 6: вкладка «График»

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (пейн `ot-rep-cchart-{{$rn}}`)
- Test: расширить `report_builder_render_test.go`

- [ ] **Step 1: Ассерт** `comp.chart.type` в тесте (композиция с Chart).
- [ ] **Step 2: FAIL.**
- [ ] **Step 3: Пейн «График»**

```html
        <div class="obj-pane" id="ot-rep-cchart-{{$rn}}">
          {{$ch := "" }}{{$cc := ""}}{{$cse := ""}}
          {{with .Composition}}{{with .Chart}}{{$ch = .Type}}{{$cc = .Category}}{{end}}{{end}}
          <div class="fg" style="margin-top:12px"><label>{{t $.Lang "Тип графика"}}</label>
            <select name="comp.chart.type">
              <option value="" {{if eq $ch ""}}selected{{end}}>{{t $.Lang "нет"}}</option>
              <option value="bar" {{if eq $ch "bar"}}selected{{end}}>{{t $.Lang "столбцы"}}</option>
              <option value="line" {{if eq $ch "line"}}selected{{end}}>{{t $.Lang "линия"}}</option>
              <option value="pie" {{if eq $ch "pie"}}selected{{end}}>{{t $.Lang "круг"}}</option>
            </select></div>
          <div class="fg"><label>{{t $.Lang "Категория (поле группировки)"}}</label>
            <input type="text" name="comp.chart.category" value="{{$cc}}"></div>
          <div class="fg"><label>{{t $.Lang "Ряды (показатели через запятую)"}}</label>
            <input type="text" name="comp.chart.series" value="{{with .Composition}}{{with .Chart}}{{join .Series ","}}{{end}}{{end}}"></div>
        </div>
```

> `join` в FuncMap конфигуратора **нет** (в `cfgTmpl`, `configurator_tmpl.go:13–30`, только
> `lower` и др.). Добавить туда `"join": strings.Join,` (импорт `strings` уже есть).
> Переменные `$ch/$cc` через `{{with}}` nil-безопасны.

- [ ] **Step 4: PASS + сборка.**
- [ ] **Step 5: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/report_builder_render_test.go
git commit -m "feat(configurator): вкладка «График» конструктора отчёта (план 59 stage 2)"
```

---

## Task 7: «Предпросмотр» + зелёная сборка

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (кнопка предпросмотра)

- [ ] **Step 1: Кнопка «Предпросмотр»**

Добавить рядом с «Сохранить» в форме отчёта кнопку, открывающую отчёт в пользовательском
режиме базы (в новой вкладке/iframe), используя уже существующий путь запуска базы и
маршрут отчёта `/ui/report/<lower-name>`:

```html
        <a class="btn-check" href="/bases/{{$.Base.ID}}/run/ui/report/{{lower .Name}}" target="_blank">{{t $.Lang "Предпросмотр"}}</a>
```

> Сверить фактический префикс запуска пользовательского режима из конфигуратора (как
> открывается «Предприятие»/iframe в этом проекте — найти в `configurator_tmpl.go`/
> `server.go`, например `runLink`/`/run/`). Подставить реальный путь. Предпросмотр требует
> сохранения и запущенной базы — это приемлемо для v1 (живой предпросмотр — follow-up).

- [ ] **Step 2: Полная сборка/тесты launcher**

Run: `go build ./... && go vet ./internal/launcher/ && go test ./internal/launcher/`
Expected: всё зелёное; шаблон парсится.

- [ ] **Step 3: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go
git commit -m "feat(configurator): кнопка предпросмотра отчёта (план 59 stage 2)"
```

---

## Task 8: регрессия + ручная приёмка

- [ ] **Step 1: Полный прогон**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: всё зелёное.

- [ ] **Step 2: Ручная приёмка (визуальная)**

1. Конфигуратор → отчёт → вкладки Структура/Оформление/График видны; добавление/удаление
   строк работает; «Сохранить» → в `reports/<имя>.yaml` появляется корректный блок `composition`.
2. Открыть отчёт без композиции, отредактировать заголовок, сохранить → `composition`
   (если был) не потерян; если не было — не появился.
3. «Предпросмотр» → отчёт строится со сворачиванием/итогами/графиком (рантайм Stage 1).
4. Сломать `when` в правиле → `onebase check` (Stage 1) сообщает об ошибке.

- [ ] **Step 3: Коммит (если правки)**

```bash
git add -A && git commit -m "chore(configurator): зелёная сборка конструктора отчётов (план 59 stage 2)"
```

---

## Self-review (выполнено при написании)

- **Покрытие дизайна (Stage 2):** backend round-trip + фикс потери composition (Task 1–2),
  пред-заполнение (Task 3), вкладки Структура/Оформление/График (Task 4–6), Предпросмотр
  (Task 7), регрессия + ручная приёмка (Task 8).
- **Контракт полей `comp.*`** согласован между парсером (Task 1) и шаблоном (Task 4–6).
- **Грабли учтены:** `strconv.Itoa` вместо самописного (Task 1 Step 3a); `{{with .Composition}}`
  для nil-безопасности шаблона (Task 4); дефолт-цвета `input[type=color]` → пусто (Task 5);
  отсутствие `join` в FuncMap — проверить (Task 6); реальный префикс запуска базы (Task 7).
- **Точки сверки при исполнении (помечены `>`):** способ рендера в структурном тесте
  (по образцу `configurator_tabs_render_test.go`), наличие `join` в FuncMap, путь запуска
  пользовательского режима из конфигуратора.
- **Вне объёма Stage 2:** живой предпросмотр без сохранения, drag-drop переупорядочивание
  (сейчас — кнопки удаления + добавление в конец; порядок группировок задаётся
  последовательностью строк), экспорт составного отчёта (Stage 3), i18n подписей таблицы рантайма.

## Follow-ups по код-ревью Stage 2 (не блокеры)

- **`applyReportComposition` — тест-онли дубль.** Продакшн-путь сохранения — инлайн в
  `updateReportFile` (`saveReport.Composition` + `parseCompositionForm`). Унифицировать
  (вынести round-trip в одну тестируемую функцию) при расколе god-файла `configurator.go`
  (план 55).
- **Короткий hex (`#c00`) не круг-трипится через `input[type=color]`.** Конструктор всегда
  пишет `#rrggbb`, так что свой вывод он переживает; но вручную/через API заведённый
  короткий hex браузер сбросит в `#000000` → парсер обнулит. Нормализовать 3-значный hex
  в 6-значный при рендере (или в парсере).
- **Двойные вкладки «Диаграмма» и «График».** Легаси `chart_proc`/`chart_source` (кастомный
  DSL-график) и новый декларативный `chart` сосуществуют — by design, но UX-путаница;
  на приёмке решить, объединять ли.
- **Break-on-empty в `parseCompositionForm`.** Списки (группировки/показатели/сортировка/
  условия) обрываются на первом пустом ключевом поле — консистентно с разбором параметров
  отчёта; JS держит индексы непрерывными при удалении, поэтому обычным UI пробел не создать,
  но оставленное вручную пустым промежуточное поле отбросит хвост. Кандидат на «пропускать
  пустые строки» вместо break.
- **i18n подписей таблицы рантайма** (`ВСЕГО`/`Итого`) — общий follow-up со Stage 1.
