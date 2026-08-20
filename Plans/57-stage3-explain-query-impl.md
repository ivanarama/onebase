# Объяснение ошибок check + подсказка запросов — Implementation Plan (план 57)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Два ИИ-помощника в конфигураторе: объяснение ошибок `check` человеческим языком и подсказка запроса OneBase по описанию.

**Architecture:** Два тонких одиночных LLM-вызова (как `cfgAIAssist`) в `internal/launcher/ai_explain_query.go` + UI-хуки в `configurator_tmpl.go`. Подсказка запроса использует срез конфигурации (`h.configSchemaText`, этап 1). Реальная проверка — ручная (LLM-ключ + браузер).

**Tech Stack:** Go; embedded HTML/JS; тесты `testing` + `renderCfgFoot`.

**Дизайн:** [57-stage3-explain-query.md](57-stage3-explain-query.md). **Ветка:** `feature/57-stage3-explain-query`.

---

## Структура файлов

- Создать: `internal/launcher/ai_explain_query.go` — `aiExplainSystem`, `cfgAIExplain`, `queryHintSystem`, `cfgAIQuery`.
- Создать: `internal/launcher/ai_explain_query_test.go` — тест `queryHintSystem`.
- Изменить: `internal/launcher/server.go` — маршруты `ai-explain`, `ai-query`.
- Изменить: `internal/launcher/configurator_tmpl.go` — кнопка «Объяснить» + JS + строка подсказки в конструкторе.
- Изменить/создать: структурный тест рендера (в `ai_explain_query_test.go`).

---

## Task 1: бэкенд (два эндпоинта)

**Files:**
- Create: `internal/launcher/ai_explain_query.go`
- Modify: `internal/launcher/server.go`
- Test: `internal/launcher/ai_explain_query_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/launcher/ai_explain_query_test.go`:

```go
package launcher

import (
	"strings"
	"testing"
)

func TestQueryHintSystem(t *testing.T) {
	s := queryHintSystem("Справочники:\n  Клиент: Наименование")
	for _, sub := range []string{"ВЫБРАТЬ", "Остатки", "Клиент", "Конфигурация базы"} {
		if !strings.Contains(s, sub) {
			t.Errorf("queryHintSystem не содержит %q:\n%s", sub, s)
		}
	}
	if got := queryHintSystem(""); strings.Contains(got, "Конфигурация базы") {
		t.Error("пустой schema не должен добавлять секцию конфигурации")
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет queryHintSystem):**

Run: `go test ./internal/launcher/ -run TestQueryHintSystem -count=1`
Expected: FAIL.

- [ ] **Step 3: Создать `internal/launcher/ai_explain_query.go`**

FIRST open `internal/launcher/ai_assist.go` and confirm the helper names used below match it exactly: `writeJSON`, `getAuthDB(ctx, b)`, `db.GetLLMConfig(ctx)`, `llm.New(cfg, nil)`, `runner.Run(ctx, "конфигуратор", llm.ChatRequest{...})`, `llm.UserText`, `llm.SafeErr`, `chi.URLParam`, and `h.configSchemaText(ctx, b)` (added in stage 1). If a signature differs, adapt; if missing, report NEEDS_CONTEXT.

```go
package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/llm"
)

var aiExplainSystem = "Ты помогаешь разработчику конфигурации OneBase понять ошибки " +
	"проверки (onebase check). Объясни по-русски, кратко, что означают ошибки и как их " +
	"исправить — по пунктам, с конкретным советом. Не выдумывай: опирайся на текст ошибок."

// cfgAIExplain объясняет вывод проверки конфигурации человеческим языком.
func (h *handler) cfgAIExplain(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, 400, map[string]any{"error": "Пустой запрос"})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	cfg, err := db.GetLLMConfig(r.Context())
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "Конфиг ИИ повреждён: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	runner := llm.New(cfg, nil)
	resp, err := runner.Run(ctx, "конфигуратор", llm.ChatRequest{
		System:   aiExplainSystem,
		Messages: []llm.Message{llm.UserText("Вывод проверки:\n" + req.Text)},
	})
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model})
}

// queryHintSystem — системный промпт подсказки запроса: справочник языка + срез
// конфигурации (этап 1). Пустой schema не добавляет секцию конфигурации.
func queryHintSystem(schema string) string {
	s := "Ты строишь запрос на языке запросов OneBase (1С-подобный: ВЫБРАТЬ поля ИЗ Источник " +
		"[КАК Псевдоним] [ЛЕВОЕ СОЕДИНЕНИЕ ... ПО ...] [ГДЕ ...] [СГРУППИРОВАТЬ ПО ...] " +
		"[УПОРЯДОЧИТЬ ПО ...]). Остатки/обороты регистров — через виртуальные таблицы: " +
		"РегистрНакопления.Имя.Остатки(&НаДату), .Обороты(&Нач, &Кон); срез сведений — " +
		".СрезПоследних(&НаДату). Параметры пиши как &Имя. Используй только существующие " +
		"объекты и поля из контекста ниже. Верни ТОЛЬКО текст запроса, без пояснений и markdown."
	if strings.TrimSpace(schema) != "" {
		s += "\n\nКонфигурация базы:\n" + schema
	}
	return s
}

// cfgAIQuery строит запрос OneBase по описанию на естественном языке.
func (h *handler) cfgAIQuery(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос"})
		return
	}
	if strings.TrimSpace(req.Description) == "" {
		writeJSON(w, 400, map[string]any{"error": "Пустой запрос"})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	cfg, err := db.GetLLMConfig(r.Context())
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "Конфиг ИИ повреждён: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	system := queryHintSystem(h.configSchemaText(ctx, b))
	runner := llm.New(cfg, nil)
	resp, err := runner.Run(ctx, "конфигуратор", llm.ChatRequest{
		System:   system,
		Messages: []llm.Message{llm.UserText(req.Description)},
	})
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "query": resp.Text, "model": resp.Model})
}
```

- [ ] **Step 4: Запустить тест — PASS:**

Run: `go test ./internal/launcher/ -run TestQueryHintSystem -count=1`
Expected: PASS.

- [ ] **Step 5: Зарегистрировать маршруты**

В `internal/launcher/server.go` найти `r.Post("/bases/{id}/configurator/ai-assist", s.h.cfgAIAssist)` и добавить сразу после:
```go
		r.Post("/bases/{id}/configurator/ai-explain", s.h.cfgAIExplain)
		r.Post("/bases/{id}/configurator/ai-query", s.h.cfgAIQuery)
```

- [ ] **Step 6: Сборка + gofmt + commit:**

Run: `go build ./internal/launcher/` → успех.
Run: `gofmt -d internal/launcher/ai_explain_query.go` → пусто (CRLF whole-file артефакты игнор; правь только точечное `gofmt -w`).
```
git add internal/launcher/ai_explain_query.go internal/launcher/server.go internal/launcher/ai_explain_query_test.go
git commit -m "feat(configurator): эндпоинты ai-explain и ai-query (план 57, этап 3)"
```

---

## Task 2: UI-хуки + структурный тест

**Files:**
- Modify: `internal/launcher/configurator_tmpl.go`
- Test: `internal/launcher/ai_explain_query_test.go` (дописать структурный тест)

- [ ] **Step 1: Дописать падающий структурный тест**

Добавить в `internal/launcher/ai_explain_query_test.go`:

```go
func TestConfigurator_ExplainQueryWired(t *testing.T) {
	html := renderCfgFoot(t)
	for _, sub := range []string{
		"configurator/ai-explain", "configurator/ai-query",
		"explainCheckErrors", "qb-ai-desc", "qb-ai-gen", "mqb-qry",
	} {
		if !strings.Contains(html, sub) {
			t.Errorf("в cfg-foot нет %q — хук не подключён", sub)
		}
	}
}
```

(`renderCfgFoot` — существующий хелпер из `langref_render_test.go`.)

- [ ] **Step 2: Запустить — FAIL:**

Run: `go test ./internal/launcher/ -run TestConfigurator_ExplainQueryWired -count=1`
Expected: FAIL.

- [ ] **Step 3: Кнопка «Объяснить» в шапке `#check-all-panel`**

В `internal/launcher/configurator_tmpl.go` найти блок:
```html
  <header>
    <span>{{t $.Lang "Проверка конфигурации"}}</span>
    <button type="button" onclick="closeCheckAll()" title="{{t $.Lang "Закрыть"}}">✕</button>
  </header>
  <div id="check-all-body"></div>
```
и заменить на:
```html
  <header>
    <span>{{t $.Lang "Проверка конфигурации"}}</span>
    <span style="flex:1"></span>
    <button type="button" onclick="explainCheckErrors(this)" title="Объяснить ошибки с помощью ИИ">🤖 Объяснить</button>
    <button type="button" onclick="closeCheckAll()" title="{{t $.Lang "Закрыть"}}">✕</button>
  </header>
  <div id="check-all-body"></div>
  <div id="check-all-explain-out" style="display:none;padding:8px 10px;border-top:1px solid #eef1f6;font-size:12px;white-space:pre-wrap;max-height:220px;overflow:auto;color:#1e293b"></div>
```

- [ ] **Step 4: JS `explainCheckErrors` в cfg-foot**

Найти функцию `closeCheckAll`:
```js
function closeCheckAll() {
  document.getElementById('check-all-panel').style.display = 'none';
}
```
и вставить ПОСЛЕ неё (в том же `<script>` блока cfg-foot):
```js
function explainCheckErrors(btn){
  var body=document.getElementById('check-all-body');
  var out=document.getElementById('check-all-explain-out');
  var text=body?body.innerText.trim():'';
  if(!text){return;}
  if(btn){btn.disabled=true;}
  out.style.display='';out.textContent='Объясняю...';out.style.color='#888';
  fetch('/bases/'+_dbgBase+'/configurator/ai-explain',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:text})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d&&d.ok){out.textContent=d.text;out.style.color='#1e293b';}
      else{out.textContent=(d&&d.error)||'Ошибка';out.style.color='#c00';}
    })
    .catch(function(){out.textContent='Ошибка сети';out.style.color='#c00';})
    .finally(function(){if(btn){btn.disabled=false;}});
}
```
(`_dbgBase` — существующий глобал cfg-foot, его использует `runCheckAll`.)

- [ ] **Step 5: Строка подсказки в конструкторе запроса**

Найти открытие тела модала конструктора:
```html
  <div class="qb-modal-bd">
```
и вставить СРАЗУ ПОСЛЕ него:
```html
    <div style="display:flex;gap:6px;align-items:center;margin-bottom:8px">
      <input id="qb-ai-desc" type="text" placeholder="Опишите запрос словами, напр.: средний чек по менеджерам за месяц" style="flex:1;font-size:12px;border:1px solid #c8d0de;border-radius:4px;padding:5px 8px">
      <button id="qb-ai-gen" type="button" style="background:#7c3aed;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer;font-size:12px;white-space:nowrap">🤖 Сгенерировать</button>
    </div>
```

- [ ] **Step 6: JS обработчик `qb-ai-gen` в cfg-foot**

Найти строку установки обработчика закрытия конструктора:
```js
document.getElementById('qb-close').onclick=function(){document.getElementById('qb-overlay').classList.remove('active');};
```
и вставить ПОСЛЕ неё:
```js
var _qbAiGen=document.getElementById('qb-ai-gen');
if(_qbAiGen)_qbAiGen.onclick=function(){
  var desc=document.getElementById('qb-ai-desc').value.trim();if(!desc)return;
  var btn=this;btn.disabled=true;var old=btn.textContent;btn.textContent='...';
  fetch('/bases/'+_dbgBase+'/configurator/ai-query',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({description:desc})})
    .then(function(r){return r.json();})
    .then(function(d){
      if(d&&d.ok&&d.query){document.getElementById('mqb-qry').value=d.query;document.getElementById('qb-mode').value='query';}
      else{alert((d&&d.error)||'Ошибка генерации запроса');}
    })
    .catch(function(){alert('Ошибка сети');})
    .finally(function(){btn.disabled=false;btn.textContent=old;});
};
```

- [ ] **Step 7: Запустить структурный тест — PASS:**

Run: `go test ./internal/launcher/ -run TestConfigurator_ExplainQueryWired -count=1`
Expected: PASS.

- [ ] **Step 8: Render-тесты cfg-foot не сломаны + сборка:**

Run: `go test ./internal/launcher/ -run 'Configurator|Langref|Layout|Render' -count=1` → PASS.
Run: `go build ./cmd/onebase` → успех (если залочен — `taskkill /IM onebase.exe /F`).

- [ ] **Step 9: Commit:**

```
git add internal/launcher/configurator_tmpl.go internal/launcher/ai_explain_query_test.go
git commit -m "feat(configurator): UI-хуки объяснения ошибок и подсказки запроса (план 57, этап 3)"
```

---

## Task 3: Верификация и статус

**Files:**
- Modify: `Plans/57-stage3-explain-query.md`

- [ ] **Step 1: Полный прогон**

Run: `go test ./... -count=1` → PASS (без FAIL).
Run: `go vet ./...` → чисто.
Run: `gofmt -d internal/launcher/ai_explain_query.go` → пусто.

- [ ] **Step 2: Обновить статус**

В `Plans/57-stage3-explain-query.md` заменить `**Статус:** дизайн утверждён, ожидает плана реализации` на `**Статус:** ✅ Реализовано (этап 3; визуальная приёмка в браузере — отдельный ручной шаг)`.

- [ ] **Step 3: Commit:**

```
git add Plans/57-stage3-explain-query.md
git commit -m "docs(plans): этап 3 плана 57 (объяснение ошибок + подсказка запросов) реализован"
```

---

## Self-Review

**Spec coverage:**
- `cfgAIExplain` (объяснение вывода check) + `aiExplainSystem` → Task 1.
- `cfgAIQuery` + `queryHintSystem` (справочник языка + срез конфигурации этапа 1) → Task 1.
- Маршруты `ai-explain`/`ai-query` в admin-зоне → Task 1.
- UI: кнопка «Объяснить» + `explainCheckErrors`; строка подсказки + `qb-ai-gen` → результат в `#mqb-qry` → Task 2; структурный тест.
- Только текст, ничего не пишут/не исполняют; `textContent` (без XSS) — соблюдено.
- Ручная браузерная приёмка — отмечена.

**Placeholder scan:** код приведён целиком (Go + HTML/JS). UI-вставки заякорены на
уникальные существующие строки (`closeCheckAll`, `qb-modal-bd`, `qb-close` onclick).

**Type consistency:** `queryHintSystem`/`cfgAIExplain`/`cfgAIQuery` — в одном файле;
переиспользуют `h.configSchemaText` (этап 1), `writeJSON`/`getAuthDB`/`llm.*` (как
`cfgAIAssist`). JS: `_dbgBase` (cfg-foot глобал), `#mqb-qry`/`#qb-mode` (поля
конструктора), `#check-all-body`/`#check-all-explain-out` (панель проверки).
