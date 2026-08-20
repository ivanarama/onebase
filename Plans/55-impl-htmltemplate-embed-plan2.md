# Конфигуратор: вынос фронта в go:embed (Фаза 2) — план реализации (план 55)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** Вынести статический CSS и большой статический JS из `internal/launcher/configurator_tmpl.go` (6726 строк) в файлы `internal/launcher/static/configurator.{css,js}` (отдаются существующим `//go:embed static` на `/static/*`), заменив серверную интерполяцию внутри JS на bootstrap-`<script>` (`window.__cfg`, `window.__cfgI18n`). Поведение не меняется.

**Architecture:** Один набор шаблонов `cfgTmpl` (`html/template`, Фаза 1). Продакшен-рендер — `renderCfg` → `cfgTmpl.ExecuteTemplate(w, "cfg-main", data)`. Главный скрипт (строки 651–4493 в `cfgFoot`) на ~97% статичен: ~11 точек данных + 91 i18n-вызов, БЕЗ условной генерации JS-кода. Динамику отдаём bootstrap-блобом через `template.JS(json)`; i18n — дамп словаря лаунчера для языка.

**Tech Stack:** Go, `html/template`, `embed.FS`, `go test ./internal/launcher/`.

**Подзадачи:** 2a — CSS (тривиально, независимо). 2b-1 — bootstrap данных+i18n, JS пока inline. 2b-2 — физический вынос JS в файл + перенацеливание тестов. Делать строго в этом порядке, мелкими коммитами.

**Вне рамок:** `cfgSyntaxRef` (~70 строк inline-JS) и `cfgHead` init-скрипты — опционально, отдельным заходом (этап 3 плана 55). Home-dashboard `{{js}}`-сайты (5862/5981) уже в bootstrap-форме — НЕ трогать.

---

## Task 1 (2a): вынести CSS в /static

**Files:**
- Create: `internal/launcher/static/configurator.css`
- Modify: `internal/launcher/configurator_tmpl.go` (удалить `cfgCSS`, поправить include в `cfgHead`, убрать из `.Parse`)

- [ ] **Step 1: Скопировать тело CSS в файл**

`cfgCSS` — строки 109–409: `const cfgCSS = ` + backtick + `{{define "css"}}` (109), `<style>` (110) … `</style>` (408), `{{end}}` + backtick (409). Скопировать **тело между `<style>` и `</style>`** (строки 110–408, без самих тегов) в новый `internal/launcher/static/configurator.css`. Тело 100% статично (проверено: нет `{{` в 110–408).

- [ ] **Step 2: Переключить подключение в cfgHead**

В `configurator_tmpl.go` заменить строку 434 `{{template "css" .}}` на:
```html
<link rel="stylesheet" href="/static/configurator.css">
```

- [ ] **Step 3: Удалить const cfgCSS и убрать из .Parse**

Удалить весь `const cfgCSS = ...` (109–409). В строке 105 убрать `cfgCSS + ` из начала конкатенации `.Parse(cfgCSS + cfgHead + ...)` → `.Parse(cfgHead + ...)`.

- [ ] **Step 4: Сборка + тесты + проверка отдачи файла**

Run: `go build ./... && go test ./internal/launcher/ 2>&1 | tail -3`
Expected: build ok; `ok` (ни один тест не ассертит на CSS — регрессий быть не должно). Также: `grep -rn 'cfgCSS\|{{template "css"' internal/launcher/` → пусто.

- [ ] **Step 5: Smoke-тест наличия CSS-линка**

Дополнить `internal/launcher/configurator_render_test.go` (в существующем `TestConfigurator_PagesRender` или новым тестом): отрендерить `cfg-main` и проверить `strings.Contains(out, `href="/static/configurator.css"`)` и отсутствие `<style>` от старого блока (например, что нет уникального CSS-селектора из старого блока — взять реальный селектор из configurator.css, напр. `.obj-tabs{`). Запустить — PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/launcher/static/configurator.css internal/launcher/configurator_tmpl.go internal/launcher/configurator_render_test.go
git commit -m "refactor(configurator): вынести CSS в /static/configurator.css (план 55 фаза 2a)"
```

---

## Task 2 (2b-1): bootstrap данных + i18n; JS остаётся inline

Цель этого шага — сделать главный скрипт независимым от серверной интерполяции, **не перемещая его**. После шага скрипт читает данные из `window.__cfg` и переводы из `window.__cfgI18n`/`T()`, а `{{t}}`/`{{.X}}` внутри `<script>` исчезают. Это держит риск изолированным: рендер ещё inline, тесты ещё видят JS в выводе.

**Files:**
- Modify: `internal/i18n/i18n.go` (добавить `Bundle.Dict(lang)`)
- Modify: `internal/launcher/configurator.go` (поле `Bootstrap`/`I18n` в `configuratorData`; заполнение в `loadCfgData`)
- Modify: `internal/launcher/configurator_tmpl.go` (bootstrap-`<script>` + правка ~11 var-init + 91 `{{t}}`→`T()` в главном скрипте + tail `var base`)

- [ ] **Step 1 (RED): тест bootstrap-блоба**

Создать `internal/launcher/configurator_bootstrap_test.go`. Отрендерить `cfg-main` на `richCfgData("tree")` (хелпер из `configurator_render_test.go`) и проверить:
```go
out := render cfg-main
// window.__cfg содержит имена сущностей как JSON-массив (а не как '...'-литералы)
if !strings.Contains(out, `window.__cfg`) { t.Error("нет window.__cfg") }
if !strings.Contains(out, `window.__cfgI18n`) { t.Error("нет window.__cfgI18n") }
// данные пробрасываются: имя сущности из richCfgData есть в bootstrap JSON
if !strings.Contains(out, `Номенклатура`) { t.Error("entityNames не в bootstrap") }
// внутри главного скрипта больше нет серверного {{t}} — косвенно: T( используется
if !strings.Contains(out, `T(`) { t.Error("нет рантайм-хелпера T()") }
```
Run → FAIL (нет `window.__cfg`/`T(` пока).

- [ ] **Step 2: `Bundle.Dict(lang)` в i18n**

В `internal/i18n/i18n.go` добавить метод, возвращающий копию словаря языка (или nil):
```go
// Dict возвращает словарь перевода для языка (key→перевод); nil если языка нет
// (напр. базовый ru — переводов нет, фронт падает в ключ через T(k)=dict[k]||k).
func (b *Bundle) Dict(lang string) map[string]string {
	if d, ok := b.dicts[lang]; ok {
		out := make(map[string]string, len(d))
		for k, v := range d { out[k] = v }
		return out
	}
	return nil
}
```
(Сверить имя поля `dicts` и `T`-резолвинг по факту i18n.go; следовать существующему `Resolve`.)

- [ ] **Step 3: bootstrap-данные в configuratorData + loadCfgData**

В `internal/launcher/configurator.go`:
- Добавить в `configuratorData` поле `Bootstrap template.JS` и `I18n template.JS` (рядом с `QBSchema`/`LayoutMeta`).
- В `loadCfgData` после заполнения остальных полей собрать bootstrap-структуру и i18n-карту:
```go
boot := map[string]any{
	"entityNames":    data.AllEntityNames,
	"enumNames":      data.AllEnumNames,
	"selectedTreeId": data.SelectedTreeID,
	"fieldsSaved":    data.FieldsSavedEntity,
	"moduleSaved":    data.ModuleSavedEntity,
	"baseId":         data.Base.ID,
	"basePort":       data.Base.Port,
	"hasSession":     data.SessionToken != "",
	"groupOrder":     data.GroupOrder,
	// _ldMeta и _mqbSchema уже template.JS — кладём как есть (raw JSON или null)
}
bb, _ := json.Marshal(boot)
data.Bootstrap = template.JS(bb)
dict := launcherBundle.Dict(resolveLang(...)) // язык как в renderCfg
ib, _ := json.Marshal(dict)                    // nil → "null"
data.I18n = template.JS(ib)
```
(`_ldMeta`/`_mqbSchema` оставить отдельными полями в bootstrap или прокинуть как есть; см. Step 5.)

- [ ] **Step 4: bootstrap-`<script>` в шаблоне**

В `cfgFoot`, **перед** строкой 651 (`<script>` главного скрипта), вставить:
```html
<script>
window.__cfg = {{.Bootstrap}};
window.__cfgI18n = {{.I18n}} || {};
function T(k){ return (window.__cfgI18n[k] || k); }
</script>
```
(`{{.Bootstrap}}`/`{{.I18n}}` — `template.JS`, не переэкранируются.)

- [ ] **Step 5: переписать ~11 var-init на чтение из __cfg**

В главном скрипте заменить серверные присвоения на чтение bootstrap (имена переменных НЕ меняем — остальной код не трогаем):
```
880  var _cfgEntityNames = window.__cfg.entityNames;
881  var _cfgEnumNames   = window.__cfg.enumNames;
1713 var directId        = window.__cfg.selectedTreeId;
1714 var saved           = window.__cfg.fieldsSaved || window.__cfg.moduleSaved;
1748 var _ldMeta         = {{if .LayoutMeta}}{{.LayoutMeta}}{{else}}{}{{end}};   // оставить (template.JS) ИЛИ перенести в __cfg.ldMeta
3176 _mqbSchema          = {{.QBSchema}};                                         // оставить ИЛИ __cfg.qbSchema
3629 var _dbgBase        = window.__cfg.baseId;
3630 var _basePort       = window.__cfg.basePort;
3631 var _hasSession     = window.__cfg.hasSession;
3632 var _treeGroupOrder = window.__cfg.groupOrder;
```
Для строки 3087 (`{{range $.AllEntityNames}}+'<option ...{{.}}>'{{end}}`) заменить серверный `{{range}}` на JS-цикл по `window.__cfg.entityNames`, строящий те же `<option>` (вставить хелпер или `.map().join('')`). Tail-скрипт строка 4520 `var base='{{.Base.ID}}'` → `var base = window.__cfg.baseId`.
Рекомендация по `_ldMeta`/`_mqbSchema`: проще оставить их как `{{.LayoutMeta}}`/`{{.QBSchema}}` (они уже `template.JS`) — тогда они останутся единственными серверными интерполяциями в скрипте, что помешает полному выносу в Task 3. Поэтому **перенести их в bootstrap**: `"ldMeta": data.LayoutMeta` (но это template.JS внутри map → json.Marshal обернёт строку; вместо этого положить сырой объект до маршалинга, либо отдельным полем `window.__ldMeta={{.LayoutMeta}}` в bootstrap-`<script>`). Простейшее: в bootstrap-`<script>` добавить `window.__ldMeta = {{if .LayoutMeta}}{{.LayoutMeta}}{{else}}{}{{end}}; window.__qbSchema = {{if .QBSchema}}{{.QBSchema}}{{else}}null{{end}};` и в скрипте `var _ldMeta = window.__ldMeta; _mqbSchema = window.__qbSchema;`.

- [ ] **Step 6: заменить 91 `{{t $.Lang "KEY"}}` → `T("KEY")` в главном скрипте**

Процедура (детерминированная, для каждого из 91 сайтов в строках 651–4493):
`{{t $.Lang "КЛЮЧ"}}` внутри JS-строки → `T("КЛЮЧ")`. Примеры:
- `cfgToast('{{t $.Lang "PDF открыт во внешнем приложении"}}')` → `cfgToast(T("PDF открыт во внешнем приложении"))`
- `'{{t $.Lang "Отмена"}}'` → `T("Отмена")`
Найти все: `rg -n '\{\{t \$\.Lang' internal/launcher/configurator_tmpl.go` (ограничить строками главного скрипта 651–4493). КЛЮЧ копировать дословно. НЕ трогать `{{t}}` вне главного скрипта (HTML-панели, cfgSyntaxRef, cfgHead) — они остаются серверными.
**Контроль:** после правок `rg -n '\{\{t \$\.Lang' <диапазон 651-4493>` → пусто; `rg -c '\{\{(\.|t |js |range|if)' <651-4493>` → только `{{.LayoutMeta}}/{{.QBSchema}}`-остатки, если их не перенесли (лучше 0).

- [ ] **Step 7: GREEN + полный прогон**

Run: `go test ./internal/launcher/ -run 'Bootstrap|PagesRender|XSS' -v` → PASS. Затем весь пакет `go test ./internal/launcher/ 2>&1 | tail -5` → `ok`. Существующие тесты на JS-функции (`langref`, `ai_assist_context`, `layout_editor`) ещё зелёные, т.к. JS пока inline в выводе. `go vet` чисто.
> Контингенция: если `T()` или `__cfg` ломают рендер (html/template execute-ошибка на вставке `template.JS`), разобрать конкретную строку. Если какой-то `{{t}}`-ключ содержит `"` — он уже строковый литерал Go-шаблона; в `T("...")` экранировать кавычку в JS (`T("...\"...")`) — таких в списке ключей нет, но проверить.

- [ ] **Step 8: Commit**
```bash
git add internal/i18n/i18n.go internal/launcher/configurator.go internal/launcher/configurator_tmpl.go internal/launcher/configurator_bootstrap_test.go
git commit -m "refactor(configurator): bootstrap window.__cfg/__cfgI18n, JS читает данные/переводы рантайм (план 55 фаза 2b-1)"
```

---

## Task 3 (2b-2): физический вынос JS в /static + перенацелить тесты

После 2b-1 главный скрипт (651–4493) и хвостовой (4517–4660) — чистый статический JS. Перенести их в файл.

**Files:**
- Create: `internal/launcher/static/configurator.js`
- Modify: `internal/launcher/configurator_tmpl.go` (заменить `<script>…</script>` на `<script src=...>`)
- Modify: `internal/launcher/{langref,ai_assist_context,layout_editor}_render_test.go` (перенацелить на файл)

- [ ] **Step 1: Перенести JS в файл**

Скопировать содержимое главного `<script>` (тело 652–4492, без тегов `<script>`/`</script>`), малого скрипта (4494–4500) и хвостового (4518–4659) в `internal/launcher/static/configurator.js` (в исходном порядке). В `cfgFoot` заменить эти три `<script>`-блока на один `<script src="/static/configurator.js"></script>` (после bootstrap-`<script>` из Task 2; bootstrap должен идти ПЕРВЫМ, чтобы `window.__cfg` существовал до загрузки файла). HTML-панели между скриптами (474–650, 4500–4517) оставить в шаблоне как есть.

- [ ] **Step 2: helper для чтения встроенного JS в тестах**

Тесты больше не найдут JS в рендере. Дать им доступ к содержимому файла через ту же `embed.FS`. Добавить в `internal/launcher` (нетестовый файл, напр. `static.go`) экспорт для тестов или прочитать файл с диска. Простейшее — в тестах читать файл: `os.ReadFile("static/configurator.js")` (рабочая директория тестов пакета = каталог пакета). Создать общий хелпер в одном из тест-файлов:
```go
func configuratorJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/configurator.js")
	if err != nil { t.Fatalf("read configurator.js: %v", err) }
	return string(b)
}
```

- [ ] **Step 3: перенацелить 3 тест-файла**

В `langref_render_test.go`, `ai_assist_context_render_test.go`, `layout_editor_render_test.go`: заменить источник для ассертов на JS-функции с `renderCfgFoot(t)` на `configuratorJS(t)`. Ассерты на имена функций (`moveLayoutArea`, `loadLangref`, `registerHoverProvider`, `activeCode`, и т.д.) остаются теми же подстроками — меняется только то, ГДЕ ищем. Для ассертов на i18n-литералы (напр. `cfgToast('PDF открыт...')`) поправить под новую форму `T("PDF открыт...")`. Ассерты на HTML (панели, `tab-tree`) НЕ трогать — они остались в шаблоне.
**Классификация при падении:** если ассерт на JS-функцию не нашёлся в `configurator.js` — значит фрагмент не перенесён (реальная потеря); если на i18n-литерал — поправить форму на `T("...")`.

- [ ] **Step 4: smoke — файл подключён, bootstrap раньше**

В `configurator_render_test.go` добавить проверку: `cfg-main` содержит `src="/static/configurator.js"`, и `window.__cfg` идёт РАНЬШЕ этого `<script src>` (проверить `strings.Index(out, "window.__cfg") < strings.Index(out, `src="/static/configurator.js"`)`).

- [ ] **Step 5: верификация**

Run: `go build ./... && go vet ./internal/launcher/ && go test ./internal/launcher/ 2>&1 | tail -5` → всё зелёное. `rg -c '\{\{' internal/launcher/static/configurator.js` → 0 (в статическом файле не должно остаться шаблонных токенов). Подсчитать ужатие: `wc -l internal/launcher/configurator_tmpl.go` (ожидаем ~2400–2600 строк против 6726).

- [ ] **Step 6: Commit**
```bash
git add internal/launcher/static/configurator.js internal/launcher/configurator_tmpl.go internal/launcher/*_render_test.go
git commit -m "refactor(configurator): вынести JS в /static/configurator.js, тесты на файл (план 55 фаза 2b-2)"
```

---

## Task 4: финальная проверка, статус, выкат

- [ ] **Step 1: полный гейт**
Run: `go build ./... && go vet ./... && go test ./... 2>&1 | grep -vE '^ok|no test files'; echo EXIT=${PIPESTATUS[*]}` → пусто, EXIT 0. `go test -race ./internal/launcher/` → ok.

- [ ] **Step 2: ручной обход в браузере** (скилл `verify`/`run`): дерево/редакторы/бэкапы/ИИ/виджеты/предпросмотр макетов — рендерятся и работают; в DevTools нет 404 на `/static/configurator.{css,js}` и нет JS-ошибок (особенно проверить, что `window.__cfg` доступен до `configurator.js`).

- [ ] **Step 3: обновить статус плана 55**
В `Plans/55-monolith-split-embed-frontend.md` и `Plans/55-impl-htmltemplate-embed.md` отметить этап 2 Фаза 2 реализованной. Commit `docs(plan): фаза 2 плана 55 — фронт конфигуратора вынесен в go:embed`.

- [ ] **Step 4: finishing-a-development-branch** — PR (та же ветка `feature/55-configurator-htmltemplate` или новая `feature/55-phase2`; решить по состоянию PR #119).

---

## Self-review

- **Покрытие спеки (раздел 4):** CSS-вынос (Task 1), статический JS в файл + bootstrap через `template.JS` (Task 2–3), порядок низкий→высокий риск (CSS→bootstrap→move), golden/smoke и ручной обход (Task 3–4), CSP-бонус — не делаем, отмечено вне рамок.
- **Плейсхолдеры:** CSS-шаги конкретны; 91 i18n-замена и перенацеливание тестов даны детерминированной процедурой с примерами и стоп-условием (нельзя предсказать каждую подстроку пофайлово до прогона — как в Фазе 1). `Bundle.Dict`, bootstrap-структура, `T()`-хелпер — даны кодом.
- **Согласованность:** `window.__cfg`/`window.__cfgI18n`/`T()` определяются в Task 2 Step 4 и используются в Task 2 Step 5–6 и Task 3; `configuratorJS(t)`-хелпер вводится в Task 3 Step 2 и используется в Step 3. Имена JS-переменных (`_cfgEntityNames` и т.д.) сохранены — остальной 3840-строчный JS не трогаем.
- **Риск-нота:** bootstrap-`<script>` обязан идти ПЕРЕД `<script src=configurator.js>` (Task 3 Step 1/Step 4) — иначе `window.__cfg` undefined.
