# Конфигуратор → html/template (Фаза 1) — план реализации (план 55)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Перевести шаблоны конфигуратора с `text/template` на `html/template`, чтобы интерполяции имён конфигурации и пользовательского ввода экранировались по контексту — закрыть XSS-долг (план 47 §1.3), не меняя поведения.

**Architecture:** Весь конфигуратор — один набор шаблонов (`cfgTmpl`, единственный `.Parse(cfgCSS+…+cfgFoot)` в `internal/launcher/configurator_tmpl.go:105`; единственный продакшен-рендер — `renderCfg` в `configurator.go:2067`). `html/template` контекстно-зависим: экранирует по окружению (HTML, атрибут, `<script>`, URL) автоматически. Смешать движки в одном наборе нельзя → переключение целиком. `configurator.go`/`static.go` уже на `html/template`, поэтому поля `template.JS` (`QBSchema`/`LayoutMeta`/`InlineJSYaml`) станут согласованы по типу.

**Tech Stack:** Go, `html/template`, `go test ./internal/launcher/`.

**Вне рамок:** Фаза 2 (вынос статики CSS/JS в `/static` + go:embed) — отдельный план. Этап 3 плана 55 (`ui/templates.go`) — тоже отдельно. Спека: `Plans/55-impl-htmltemplate-embed.md`.

---

### Task 1: Падающий тест экранирования (XSS)

**Files:**
- Create: `internal/launcher/configurator_xss_test.go`

- [ ] **Step 1: Написать падающий тест**

Тест рендерит `cfg-main` (включает `tab-tree` — HTML-контекст с именем сущности — и `cfg-foot` — JS-массив `_cfgEntityNames` из `AllEntityNames`) с XSS-payload в имени и проверяет, что сырой payload в вывод не попал.

```go
package launcher

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfigurator_XSS_Escaped: имена объектов конфигурации экранируются и в
// HTML-, и в JS-контексте (план 55 этап 2 — закрывает XSS-долг плана 47 §1.3).
// До перехода на html/template падает: text/template вставляет payload сырым.
func TestConfigurator_XSS_Escaped(t *testing.T) {
	const payload = `<img src=x onerror=alert(1)>`
	data := &configuratorData{
		Base:           &Base{ID: "b", Name: "Тест", ConfigSource: "file"},
		Lang:           "ru",
		Tab:            "tree",
		Catalogs:       []cfgEntity{{Name: payload, Kind: "Справочник"}},
		AllEntityNames: []string{payload},
	}
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", data); err != nil {
		t.Fatalf("ExecuteTemplate cfg-main: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, payload) {
		t.Fatal("XSS-payload попал в вывод неэкранированным")
	}
	if !strings.Contains(out, "&lt;img") && !strings.Contains(out, `<img`) {
		t.Fatal("ожидалась экранированная форма payload (HTML &lt; или JS \\u003c) — её нет")
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что падает**

Run: `go test ./internal/launcher/ -run TestConfigurator_XSS_Escaped -v`
Expected: FAIL — `XSS-payload попал в вывод неэкранированным` (text/template вставляет `<img …>` сырым и в текст дерева, и в `_cfgEntityNames=['<img …>']`).

- [ ] **Step 3: Коммит теста**

```bash
git add internal/launcher/configurator_xss_test.go
git commit -m "test(configurator): падающий тест экранирования XSS (план 55, фаза 1)"
```

---

### Task 2: Переключить движок на html/template + починить funcmap `js`

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go:6` (импорт)
- Modify: `internal/launcher/configurator_tmpl.go:32-40` (funcmap `js`)

- [ ] **Step 1: Сменить импорт**

В `internal/launcher/configurator_tmpl.go` строка 6: заменить
```go
	"text/template"
```
на
```go
	"html/template"
```
`template.Must`/`template.New`/`template.FuncMap`/`template.JS` существуют в `html/template` с теми же сигнатурами — остальной код не трогаем.

- [ ] **Step 2: Починить funcmap `js` (ловушка двойного экранирования)**

`html/template` повторно экранирует обычную строку, возвращённую в `<script>`-контекст → битый JSON. Возвращать `template.JS`. Заменить блок (строки ~32-40):
```go
	"js": func(v any) string {
		// json.Marshal по умолчанию экранирует <, >, & в \uXXXX — безопасно
		// для вставки в <script> (text/template не экранирует сам).
		b, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return string(b)
	},
```
на
```go
	"js": func(v any) template.JS {
		// json.Marshal экранирует <, >, & в \uXXXX; возвращаем template.JS,
		// чтобы html/template не экранировал повторно (двойное экранирование).
		b, err := json.Marshal(v)
		if err != nil {
			return template.JS("null")
		}
		return template.JS(b)
	},
```

- [ ] **Step 3: Запустить XSS-тест — убедиться, что проходит**

Run: `go test ./internal/launcher/ -run TestConfigurator_XSS_Escaped -v`
Expected: PASS.

> **Контингенция (важно):** `cfgTmpl` строится в `var cfgTmpl = template.Must(...)` на init пакета. Если `html/template` не сможет определить контекст какой-то интерполяции, `template.Must` **паникует на init** — тогда падает компиляция/запуск ВСЕХ тестов пакета с сообщением вида `cannot compute output context for ... in <script>`. Если так: открыть указанный в ошибке участок, обычно это интерполяция внутри JS-выражения/регэкспа/комментария; вынести значение в поле `template.JS` (через `js`-хелпер или JSON) либо переструктурировать. Это ожидаемая часть миграции, не баг плана.

- [ ] **Step 4: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go
git commit -m "fix(security): конфигуратор на html/template — контекстное экранирование (план 47 §1.3)"
```

---

### Task 3: Согласовать существующие рендер-тесты (churn экранирования) + аудит template.JS

**Files:**
- Modify (по факту падений): `internal/launcher/*_render_test.go` и др. рендер-тесты

- [ ] **Step 1: Прогнать все тесты пакета**

Run: `go test ./internal/launcher/ 2>&1 | tail -40`
Expected: XSS-тест зелёный; часть существующих рендер-тестов может упасть из-за изменённого экранирования (данные с `&`, `'`, `<`, `>`, `"`). Кириллица не экранируется — большинство ассертов не затронуты.

- [ ] **Step 2: Для каждого падения — классифицировать и починить ассерт**

Процедура (выполнять для каждого упавшего теста):
1. Посмотреть diff ожидаемой/полученной строки.
2. Если различие — только экранирование HTML-спецсимвола (`&`→`&amp;`, `'`→`&#39;`, `<`→`&lt;`, `"`→`&#34;`, в JS — `&`/`'`/`<`), обновить ожидаемую подстроку в тесте на экранированную форму. Пример: ассерт `strings.Contains(html, "a & b")` → `strings.Contains(html, "a &amp; b")`.
3. Если различие **не** про экранирование (пропал элемент, сломалась структура, ошибка шаблона) — **СТОП**: это настоящая регрессия рендера, разобраться (вероятно случай из контингенции Task 2 Step 3), не «подгонять» тест.

- [ ] **Step 3: Аудит полей template.JS (нет двойного экранирования)**

Грепнуть сайты данных-в-JS и убедиться, что они рендерятся как валидный JSON, а не как `&#34;`-мусор:

Run: `go test ./internal/launcher/ -run 'Layout|QueryBuilder|Langref|Home' -v 2>&1 | tail -30`
Проверка глазами: в выводе тестов, затрагивающих `LayoutMeta`/`QBSchema`/`{{js}}`, JSON остаётся `{"...":...}`, а не `{&#34;...&#34;:...}`. Поля типа `template.JS` html/template не переэкранирует — это контроль, что тип проставлен верно. Если где-то JSON испорчен — значит значение прокидывается как обычная строка, а не `template.JS`: исправить источник (тип поля/хелпер).

- [ ] **Step 4: Полный прогон пакета — зелёный**

Run: `go test ./internal/launcher/ 2>&1 | tail -5`
Expected: `ok  github.com/ivantit66/onebase/internal/launcher`.

- [ ] **Step 5: Коммит**

```bash
git add -A internal/launcher/
git commit -m "test(configurator): согласовать ассерты под экранирование html/template"
```

---

### Task 4: Удалить мёртвую конст + smoke-тест рендера страниц

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go` (удалить `cfgAdminOverlay`, ~строка 474)
- Create: `internal/launcher/configurator_render_test.go`

- [ ] **Step 1: Написать smoke-тест рендера вкладок**

```go
package launcher

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfigurator_PagesRender: каждая вкладка верхнего уровня рендерится без
// ошибки шаблона и содержит якорный фрагмент (план 55, защита от регресса).
func TestConfigurator_PagesRender(t *testing.T) {
	base := &Base{ID: "b", Name: "Тест", ConfigSource: "file"}
	cases := []struct {
		tab    string
		anchor string
	}{
		{"tree", `class="obj-tabs"`},
		{"convert", `id="syntax-ref-panel"`},
		{"files", `id="syntax-ref-panel"`},
		{"backup", `id="syntax-ref-panel"`},
	}
	for _, c := range cases {
		t.Run(c.tab, func(t *testing.T) {
			data := &configuratorData{Base: base, Lang: "ru", Tab: c.tab,
				Catalogs: []cfgEntity{{Name: "Номенклатура", Kind: "Справочник"}}}
			var buf bytes.Buffer
			if err := cfgTmpl.ExecuteTemplate(&buf, "cfg-main", data); err != nil {
				t.Fatalf("рендер вкладки %q: %v", c.tab, err)
			}
			if !strings.Contains(buf.String(), c.anchor) {
				t.Fatalf("во вкладке %q нет якоря %q", c.tab, c.anchor)
			}
		})
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что проходит**

Run: `go test ./internal/launcher/ -run TestConfigurator_PagesRender -v`
Expected: PASS для всех вкладок. Если какой-то `anchor` не совпал — поправить ожидаемую подстроку на реальный якорный элемент этой вкладки (свериться с `cfg-main` в `configurator_tmpl.go`), не трогая логику.

- [ ] **Step 3: Удалить мёртвую конст `cfgAdminOverlay`**

В `internal/launcher/configurator_tmpl.go` найти `const cfgAdminOverlay = ` (~строка 474) и удалить весь литерал. Она не входит в `.Parse(...)` (строка 105) и не используется — `cfg-main` инлайнит этот `<div>` сам. Проверить отсутствие ссылок:

Run: `grep -rn cfgAdminOverlay internal/launcher/`
Expected: пусто после удаления.

- [ ] **Step 4: Сборка + прогон пакета**

Run: `go build ./... && go test ./internal/launcher/ 2>&1 | tail -5`
Expected: build ok; `ok  .../internal/launcher`.

- [ ] **Step 5: Коммит**

```bash
git add internal/launcher/configurator_tmpl.go internal/launcher/configurator_render_test.go
git commit -m "test(configurator): smoke-рендер вкладок + удалить мёртвую cfgAdminOverlay"
```

---

### Task 5: Полная проверка и аудит «намеренной разметки»

**Files:**
- (только проверки; правки — если аудит что-то найдёт)

- [ ] **Step 1: Аудит интерполяций, которые должны выдавать разметку**

Цель — убедиться, что нигде старый код намеренно не вставлял HTML/JS, который html/template теперь заэкранирует и сломает. Поискать в `configurator_tmpl.go` интерполяции данных (не `{{t}}`, не управляющие), вставляющие фрагменты разметки:

Run: `grep -nE '\{\{[^}]*\.[A-ZА-Я][^}]*\}\}' internal/launcher/configurator_tmpl.go | grep -iE 'html|raw|safe' ; echo "---"; grep -nE '\{\{\.[A-Za-zА-Яа-я]+\}\}' internal/launcher/configurator_tmpl.go | wc -l`
Проверка: поля `configuratorData`, чьё значение — заведомо HTML (нет таких по разведке). Если найдётся поле, реально содержащее разметку (напр. предрендеренный фрагмент), и тест на его странице показывает заэкранированный мусор — обернуть источник в `template.HTML` с комментарием-обоснованием. Если ничего — зафиксировать в финальном сообщении, что намеренной raw-разметки нет.

- [ ] **Step 2: Полный прогон + vet + race**

Run: `go build ./... && go vet ./internal/launcher/ && go test ./... 2>&1 | grep -vE '^ok|no test files'; echo EXIT=${PIPESTATUS[*]}`
Expected: пусто (нет падений), все EXIT-коды 0.

- [ ] **Step 3: Подтвердить уход text/template из конфигуратора**

Run: `grep -rn '"text/template"' internal/launcher/`
Expected: пусто (конфигуратор больше не использует text/template).

- [ ] **Step 4: Ручная проверка в браузере (через скилл `verify`/`run`)**

Поднять лаунчер, открыть конфигуратор базы, пройти: дерево метаданных → редактор справочника/документа (поля, формы) → отчёт → бэкапы → страница ИИ-помощника → виджеты. Убедиться: страницы рендерятся и работают как раньше; имена с символами `&`/`<`/кавычками отображаются корректно (как текст, не как разметка). Зафиксировать результат.

- [ ] **Step 5: Обновить статус плана и закоммитить**

В `Plans/55-monolith-split-embed-frontend.md` отметить этап 2 как «Фаза 1 (html/template) реализована»; в `Plans/47-audit-fixes-2026-06-06.md` отметить пункт 1.3 закрытым.

```bash
git add Plans/55-monolith-split-embed-frontend.md Plans/47-audit-fixes-2026-06-06.md
git commit -m "docs(plan): фаза 1 плана 55 (html/template) — XSS-долг плана 47 §1.3 закрыт"
```

---

## Self-review (выполнено при написании)

- **Покрытие спеки:** Раздел 3 спеки (Фаза 1) — Task 1–5 покрывают: переключение движка (T2), фикс `js` (T2 Step 2), аудит `template.JS` (T3 Step 3), закрытие XSS (T1+T2), чистку `cfgAdminOverlay` (T4), аудит намеренной разметки (T5 Step 1), тесты `configurator_xss_test`/`configurator_render_test` (T1/T4), критерий готовности (T5). Фаза 2 — вне рамок этого плана (отмечено).
- **Плейсхолдеры:** нет TBD/«доработать»; вся подстановка тестов и правок дана кодом или точной процедурой. Точечные правки ассертов под экранирование нельзя предсказать пофайлово до запуска — дана детерминированная процедура классификации (T3 Step 2) с примером и стоп-условием на настоящую регрессию.
- **Согласованность типов:** `js` возвращает `template.JS` (html/template) — согласовано с `configurator.go`/`static.go` (тоже `html/template`); поля `QBSchema`/`LayoutMeta`/`InlineJSYaml` уже `template.JS`. Рендер везде через `cfgTmpl.ExecuteTemplate(&buf, "cfg-main"/"tab-tree", data)`; helper-данные — реальные типы `cfgEntity`/`cfgField` из `configurator.go`.
