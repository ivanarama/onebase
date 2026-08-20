# История ИИ-запросов конфигуратора + формат метаданных в промпте генератора — Implementation Plan (план 57)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** (А) опционально (флаг в настройках ИИ) вести историю всех ИИ-обращений конфигуратора — запрос **и** ответ модели — с просмотром в отдельной странице; (Б) дать генератору каркаса формат метаданных в промпте, чтобы он создавал табличные части (в т.ч. у справочников).

**Architecture:** Журнал переиспользует существующую таблицу `_ai_audit` (storage), расширенную колонкой `response` через готовый `db.AddColumnIfMissing`. Флаг `log_history` живёт в `llm.Config` (JSON в `_settings`). Чистая функция `logCfgAI` вызывается из всех четырёх конфигураторных ИИ-хендлеров. Просмотр — серверная HTML-страница в admin-зоне конфигуратора. Формат метаданных — статическая справка в системном промпте генератора.

**Tech Stack:** Go; `internal/storage` (DB, диалекты SQLite/Postgres), `internal/llm`, `internal/launcher` (chi-роуты, шаблоны).

---

## Контекст и цель

После добавления генератора каркаса (🏗️) выяснилось: (1) истории ИИ-запросов конфигуратора нет — `LogAIQuery` вызывается только в пользовательском режиме (`internal/ui`), конфигураторные эндпоинты не логируют, а UI просмотра журнала ИИ не существует нигде; (2) генератор не создаёт табличные части, потому что системный промпт не даёт модели формат YAML метаданных (метаданные движок поддерживает — `tableparts` в `metadata/yaml.go:45`). Управляемые формы генератор не создаёт **by design** (этап 2a — только метаданные) — это не входит в план.

Решения пользователя: историю логировать для **всех** ИИ-инструментов конфигуратора; хранить **запрос + ответ модели**; запись включается флагом в настройках ассистента.

## Структура файлов

- `internal/storage/ai_audit.go` — +поле `Response`, миграция колонки, чтение/запись (Task 1).
- `internal/llm/config.go` — +флаг `LogHistory` (Task 2).
- `internal/launcher/ai_history.go` — **новый**: `logCfgAI`, `cfgLogin`, `genResponseSummary`, `renderAIHistory`, `truncate`, `cfgAdminAIHistory` (Tasks 3, 4).
- `internal/launcher/ai_generate.go`, `ai_assist.go`, `ai_explain_query.go` — вызовы `logCfgAI` (Task 3); `ai_generate.go` — формат в промпте (Task 5).
- `internal/launcher/server.go` — маршрут `ai-history` (Task 4).
- `internal/launcher/configurator_tmpl.go` — пункт меню (Task 4).
- Тесты: `internal/storage/ai_audit_test.go`, `internal/llm/config_test.go`, `internal/launcher/ai_history_test.go`, `internal/launcher/ai_generate_test.go`.

---

## Task 1: Колонка `response` в журнале ИИ (storage)

**Files:**
- Modify: `internal/storage/ai_audit.go`
- Test: `internal/storage/ai_audit_test.go`

- [ ] **Step 1: Падающий тест**

Добавить в `internal/storage/ai_audit_test.go`:

```go
func TestAIAudit_StoresAndReadsResponse(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.LogAIQuery(ctx, AIAuditEntry{
		Task: "конфигуратор-генерация", Model: "glm-4.6",
		Query: "справочник Клиенты", Response: "создан catalogs/клиенты.yaml",
		InputTokens: 12, OutputTokens: 34,
	})
	got, err := db.ListAIAudit(ctx, 10)
	if err != nil {
		t.Fatalf("ListAIAudit: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ожидалась 1 запись, получено %d", len(got))
	}
	if got[0].Response != "создан catalogs/клиенты.yaml" {
		t.Errorf("Response не сохранён/прочитан: %q", got[0].Response)
	}
}
```

Проверь импорты тест-файла: нужны `context`, `path/filepath`. Если отсутствуют — добавь.

- [ ] **Step 2: Запустить — FAIL (нет поля Response):**

Run: `go test ./internal/storage/ -run TestAIAudit_StoresAndReadsResponse -count=1`
Expected: FAIL — `unknown field Response` (компиляция).

- [ ] **Step 3: Реализовать**

В `internal/storage/ai_audit.go`:

(a) В `AIAuditEntry` добавить поле после `OutputTokens`:
```go
	Response     string // ответ модели (для журнала конфигуратора)
```

(b) В `EnsureAIAuditSchema`, сразу перед `return nil`, добавить ленивую миграцию колонки (готовый кросс-диалектный хелпер из `ddl.go:163`):
```go
	if err := db.AddColumnIfMissing(ctx, "_ai_audit", "response", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ai_audit: add response: %w", err)
	}
```

(c) В `LogAIQuery` дописать колонку `response` и плейсхолдер №10:
```go
	q := fmt.Sprintf(`INSERT INTO _ai_audit
		(id, user_id, user_login, task, model, query, rows_count, input_tokens, output_tokens, response)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10))
	_, _ = db.Exec(ctx, q,
		uuid.NewString(), e.UserID, e.UserLogin, e.Task, e.Model, e.Query,
		e.Rows, e.InputTokens, e.OutputTokens, e.Response)
```

(d) В `ListAIAudit` добавить `response` в SELECT (перед `at`) и в `Scan`:
```go
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT id, user_id, user_login, task, model, query,
		rows_count, input_tokens, output_tokens, response, at
		FROM _ai_audit ORDER BY at DESC LIMIT %d`, limit))
	...
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserLogin, &e.Task, &e.Model, &e.Query,
			&e.Rows, &e.InputTokens, &e.OutputTokens, &e.Response, &at); err != nil {
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/storage/ -run TestAIAudit -count=1`
Expected: PASS (новый тест + существующие `TestAIAudit*`).

- [ ] **Step 5: Commit**

```
git add internal/storage/ai_audit.go internal/storage/ai_audit_test.go
git commit -m "feat(storage): поле response в журнале ИИ _ai_audit (план 57)"
```

---

## Task 2: Флаг `LogHistory` в конфиге ИИ (llm)

**Files:**
- Modify: `internal/llm/config.go`
- Test: `internal/llm/config_test.go` (создать, если нет)

- [ ] **Step 1: Падающий тест**

Добавить (или создать файл `internal/llm/config_test.go` с `package llm` и `import "testing"`):

```go
func TestParseConfig_LogHistory(t *testing.T) {
	c, err := ParseConfig(`{"enabled":true,"log_history":true}`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if !c.LogHistory {
		t.Error("log_history не распознан конфигом")
	}
	// по умолчанию выключено
	d, _ := ParseConfig(`{"enabled":true}`)
	if d.LogHistory {
		t.Error("LogHistory должен быть false по умолчанию")
	}
}
```

- [ ] **Step 2: Запустить — FAIL:**

Run: `go test ./internal/llm/ -run TestParseConfig_LogHistory -count=1`
Expected: FAIL — `unknown field LogHistory`.

- [ ] **Step 3: Реализовать**

В `internal/llm/config.go`, в `type Config struct`, после `DefaultProfile`:
```go
	LogHistory     bool       `json:"log_history,omitempty" yaml:"log_history,omitempty"` // вести журнал ИИ-обращений конфигуратора
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/llm/ -run TestParseConfig_LogHistory -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/llm/config.go internal/llm/config_test.go
git commit -m "feat(llm): флаг log_history в конфиге ИИ (план 57)"
```

---

## Task 3: Логирование во всех конфигураторных ИИ-хендлерах (launcher)

**Files:**
- Create: `internal/launcher/ai_history.go`
- Modify: `internal/launcher/ai_generate.go`, `ai_assist.go`, `ai_explain_query.go`
- Test: `internal/launcher/ai_history_test.go`

- [ ] **Step 1: Падающий тест**

Создать `internal/launcher/ai_history_test.go`:

```go
package launcher

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestLogCfgAI_RespectsFlag(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// флаг выключен — ничего не пишем
	logCfgAI(ctx, db, llm.Config{LogHistory: false}, "admin",
		"конфигуратор-генерация", "ТЗ", "ответ", llm.ChatResponse{Model: "m"})
	if e, _ := db.ListAIAudit(ctx, 10); len(e) != 0 {
		t.Fatalf("при выключенном флаге запись не должна создаваться, есть %d", len(e))
	}

	// флаг включён — пишем запрос и ответ
	logCfgAI(ctx, db, llm.Config{LogHistory: true}, "admin",
		"конфигуратор-генерация", "ТЗ", "ответ",
		llm.ChatResponse{Model: "glm-4.6", InputTokens: 5, OutputTokens: 7})
	e, _ := db.ListAIAudit(ctx, 10)
	if len(e) != 1 || e[0].Response != "ответ" || e[0].Task != "конфигуратор-генерация" || e[0].OutputTokens != 7 {
		t.Fatalf("запись журнала неверна: %+v", e)
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет logCfgAI):**

Run: `go test ./internal/launcher/ -run TestLogCfgAI -count=1`
Expected: FAIL — `undefined: logCfgAI`.

- [ ] **Step 3: Создать `internal/launcher/ai_history.go` (часть 1 — логирование)**

```go
package launcher

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/storage"
)

// logCfgAI пишет обращение к ИИ из конфигуратора в журнал _ai_audit, если в
// настройках включён log_history. Best-effort: на ответ пользователю не влияет.
func logCfgAI(ctx context.Context, db *storage.DB, cfg llm.Config, login, task, query, response string, resp llm.ChatResponse) {
	if !cfg.LogHistory || db == nil {
		return
	}
	db.LogAIQuery(ctx, storage.AIAuditEntry{
		UserLogin:    login,
		Task:         task,
		Model:        resp.Model,
		Query:        query,
		Response:     response,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	})
}

// cfgLogin возвращает логин текущего пользователя конфигуратора (или "").
func cfgLogin(ctx context.Context) string {
	if u := cfgUserFromContext(ctx); u != nil {
		return u.Login
	}
	return ""
}

// genResponseSummary формирует текст ответа генератора для журнала: пояснение
// модели + список предложенных объектов.
func genResponseSummary(text string, changes []GenChange) string {
	var b strings.Builder
	b.WriteString(text)
	if len(changes) > 0 {
		b.WriteString("\n\nОбъекты:")
		for _, c := range changes {
			b.WriteString("\n- " + c.Kind + ": " + c.Path)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/launcher/ -run TestLogCfgAI -count=1`
Expected: PASS.

- [ ] **Step 5: Вставить вызовы в хендлеры**

`internal/launcher/ai_generate.go` — в `cfgAIGenerate`, заменить финальный успешный ответ:
```go
	changes := g.diff()
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-генерация", req.Prompt, genResponseSummary(resp.Text, changes), resp)
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model, "changes": changes})
```
(заменяет строку `writeJSON(w, 200, map[string]any{"ok": true, ... "changes": g.diff()})`).

`internal/launcher/ai_assist.go` — в `cfgAIAssist`, перед финальным `writeJSON(w, 200, map[string]any{"ok": true, ...})`:
```go
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-помощник", req.Prompt, resp.Text, resp)
```

`internal/launcher/ai_explain_query.go` — в `cfgAIExplain`, перед `writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, ...})` (строка 57):
```go
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-объяснение", req.Text, resp.Text, resp)
```
— и в `cfgAIQuery`, перед `writeJSON(w, 200, map[string]any{"ok": true, "query": resp.Text, ...})` (строка 115):
```go
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-запрос", req.Description, resp.Text, resp)
```

(Во всех четырёх `db`, `cfg`, `resp`, `r` уже в области видимости.)

- [ ] **Step 6: Сборка + тест пакета**

Run: `go build ./internal/launcher/` → успех.
Run: `go test ./internal/launcher/ -run TestLogCfgAI -count=1` → PASS.

- [ ] **Step 7: Commit**

```
git add internal/launcher/ai_history.go internal/launcher/ai_history_test.go internal/launcher/ai_generate.go internal/launcher/ai_assist.go internal/launcher/ai_explain_query.go
git commit -m "feat(configurator): логирование ИИ-обращений в журнал при log_history (план 57)"
```

---

## Task 4: Страница «История ИИ» (launcher)

**Files:**
- Modify: `internal/launcher/ai_history.go`, `server.go`, `configurator_tmpl.go`
- Test: `internal/launcher/ai_history_test.go`

- [ ] **Step 1: Падающий тест**

Добавить в `internal/launcher/ai_history_test.go` (дописать импорты `strings`, `time`):

```go
func TestRenderAIHistory(t *testing.T) {
	if out := renderAIHistory(nil); !strings.Contains(out, "Журнал пуст") {
		t.Error("пустой журнал должен подсказывать про включение записи")
	}
	out := renderAIHistory([]storage.AIAuditEntry{{
		Task: "конфигуратор-генерация", Model: "glm", Query: "<b>ТЗ</b>",
		Response: "готово", InputTokens: 5, OutputTokens: 6, At: time.Now(),
	}})
	if !strings.Contains(out, "конфигуратор-генерация") || !strings.Contains(out, "готово") {
		t.Error("запись журнала не отрендерена")
	}
	if strings.Contains(out, "<b>ТЗ</b>") {
		t.Error("HTML в запросе должен экранироваться")
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет renderAIHistory):**

Run: `go test ./internal/launcher/ -run TestRenderAIHistory -count=1`
Expected: FAIL — `undefined: renderAIHistory`.

- [ ] **Step 3: Дописать `internal/launcher/ai_history.go` (часть 2 — просмотр)**

Добавить импорты `"fmt"`, `"html"`, `"net/http"`, `"github.com/go-chi/chi/v5"` к существующему блоку и:

```go
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// renderAIHistory строит HTML-фрагмент таблицы журнала ИИ (для admin-оверлея).
func renderAIHistory(entries []storage.AIAuditEntry) string {
	var b strings.Builder
	b.WriteString(`<div style="padding:16px"><h3 style="margin:0 0 10px;font-size:15px">История ИИ-запросов</h3>`)
	if len(entries) == 0 {
		b.WriteString(`<div style="color:#888;font-size:12px">Журнал пуст. Включите запись в настройках ИИ: <code>"log_history": true</code>.</div></div>`)
		return b.String()
	}
	b.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:12px"><thead><tr style="text-align:left;border-bottom:1px solid #e2e8f0;color:#666">` +
		`<th style="padding:4px">Дата</th><th style="padding:4px">Инструмент</th><th style="padding:4px">Модель</th><th style="padding:4px">Токены</th><th style="padding:4px">Запрос / ответ</th></tr></thead><tbody>`)
	for _, e := range entries {
		fmt.Fprintf(&b, `<tr style="border-bottom:1px solid #f1f5f9;vertical-align:top">`+
			`<td style="padding:4px;white-space:nowrap">%s</td><td style="padding:4px">%s</td><td style="padding:4px">%s</td><td style="padding:4px;white-space:nowrap">%d+%d</td>`+
			`<td style="padding:4px"><details><summary style="cursor:pointer">%s</summary>`+
			`<pre style="white-space:pre-wrap;word-break:break-word;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;padding:6px;margin:4px 0">%s</pre></details></td></tr>`,
			e.At.Format("02.01.2006 15:04"), html.EscapeString(e.Task), html.EscapeString(e.Model),
			e.InputTokens, e.OutputTokens, html.EscapeString(truncate(e.Query, 80)), html.EscapeString(e.Response))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// cfgAdminAIHistory — страница «История ИИ» в админ-меню конфигуратора.
func (h *handler) cfgAdminAIHistory(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	entries, err := db.ListAIAudit(r.Context(), 200)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">` + html.EscapeString(err.Error()) + `</div>`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(renderAIHistory(entries)))
}
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/launcher/ -run TestRenderAIHistory -count=1`
Expected: PASS.

- [ ] **Step 5: Зарегистрировать маршрут**

В `internal/launcher/server.go`, рядом со строкой `r.Get("/bases/{id}/configurator/admin/ai", s.h.cfgAdminAI)`:
```go
		r.Get("/bases/{id}/configurator/admin/ai-history", s.h.cfgAdminAIHistory)
```

- [ ] **Step 6: Пункт меню**

В `internal/launcher/configurator_tmpl.go`, сразу после строки `<a href="#" onclick="cfgAdmin('ai');return false">{{t $.Lang "ИИ-помощник"}}</a>`:
```html
      <a href="#" onclick="cfgAdmin('ai-history');return false">{{t $.Lang "История ИИ"}}</a>
```
(Открытие — через существующую `cfgAdmin('ai-history')`, которая грузит `/configurator/admin/ai-history` в admin-оверлей.)

- [ ] **Step 7: Сборка + тесты пакета**

Run: `go build ./internal/launcher/ ./cmd/onebase` (если бинарь залочен — `taskkill /IM onebase.exe /F`).
Run: `go test ./internal/launcher/ -run 'TestRenderAIHistory|TestLogCfgAI' -count=1` → PASS.

- [ ] **Step 8: Commit**

```
git add internal/launcher/ai_history.go internal/launcher/ai_history_test.go internal/launcher/server.go internal/launcher/configurator_tmpl.go
git commit -m "feat(configurator): страница «История ИИ» (план 57)"
```

---

## Task 5: Формат метаданных в промпте генератора (launcher)

**Files:**
- Modify: `internal/launcher/ai_generate.go`
- Test: `internal/launcher/ai_generate_test.go`

- [ ] **Step 1: Падающий тест**

Добавить в `internal/launcher/ai_generate_test.go`:

```go
func TestGenerateSystemPrompt_HasMetadataFormat(t *testing.T) {
	for _, want := range []string{"tableparts", "reference:", "type: number", "posting: true"} {
		if !strings.Contains(aiGenerateSystem, want) {
			t.Errorf("системный промпт генератора не содержит %q", want)
		}
	}
}
```

- [ ] **Step 2: Запустить — FAIL:**

Run: `go test ./internal/launcher/ -run TestGenerateSystemPrompt -count=1`
Expected: FAIL — промпт не содержит формата (например `posting: true`).

- [ ] **Step 3: Реализовать**

В `internal/launcher/ai_generate.go` добавить константу (формат проверен по `examples/trade/documents/ЗаказПоставщику.yaml` и `metadata/yaml.go` — ссылки кодируются в `type`, отдельного `ref` нет):

```go
// metadataFormatGuide — формат YAML объектов для промпта генератора, чтобы модель
// не угадывала ключи (в т.ч. табличные части и тип-ссылки).
const metadataFormatGuide = `Формат объекта метаданных (один YAML-файл = один объект):
  name: ИмяОбъекта            # обязательно, без пробелов
  title: Человекочитаемый заголовок
  fields:
    - {name: Наименование, type: string}
    - {name: Контрагент, type: reference:Контрагент}   # ссылка: reference:<Справочник>
    - {name: Статус, type: enum:СтатусЗаказа}          # перечисление: enum:<Перечисление>
  tableparts:                 # табличные части — и у документов, И у справочников
    - name: Товары
      fields:
        - {name: Номенклатура, type: reference:Номенклатура}
        - {name: Количество, type: number}
        - {name: Цена, type: number}
Типы полей: string, number, date, bool, text, reference:<Справочник>, enum:<Перечисление>.
Документ: posting: true (проведение); numerator: {prefix: "Пр-", length: 6, period: year} (автономер).
Справочник: hierarchical: true (иерархия).
Если в задаче есть состав/строки/товары/табличная часть — ОБЯЗАТЕЛЬНО добавь tableparts (в том числе справочнику).`
```

И дописать его к `aiGenerateSystem` (изменить конец объявления `var aiGenerateSystem = "..." + builtinReference`):
```go
	"Имена и типы полей бери реальные; не выдумывай несуществующие типы. Известные функции: " + builtinReference +
	"\n\n" + metadataFormatGuide
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/launcher/ -run TestGenerateSystemPrompt -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```
git add internal/launcher/ai_generate.go internal/launcher/ai_generate_test.go
git commit -m "feat(configurator): формат метаданных (tableparts) в промпте генератора (план 57)"
```

---

## Task 6: Верификация и финал

- [ ] **Step 1: Полный прогон**

Run: `go test ./... -count=1` → все `ok`, без FAIL.
Run: `go vet ./internal/storage/ ./internal/llm/ ./internal/launcher/` → чисто.
Run: `gofmt -d internal/storage/ai_audit.go internal/llm/config.go internal/launcher/ai_history.go` → пусто (CRLF-артефакт целого файла игнорировать — сверять как в [[gofmt-crlf-false-positive]]; править только точечные отступы).
Run: `go build -o "$env:TEMP\onebase_check.exe" ./cmd/onebase` → BUILD OK.

- [ ] **Step 2: Ручная проверка (нужна рабочая модель в настройках базы)**

  - Настройки → ИИ-помощник → в JSON добавить `"log_history": true` → Сохранить.
  - Сделать запрос 🏗️ генератором и/или 🤖 помощником.
  - Меню → «История ИИ» → запись видна: дата, инструмент, модель, токены, раскрывающийся запрос+ответ.
  - 🏗️: «справочник Заявки с табличной частью Товары (номенклатура, количество, цена)» → в предложенном YAML присутствует блок `tableparts` → Применить → у объекта есть ТЧ.

## Self-Review

**Spec coverage:**
- (А) флаг в настройках → Task 2; запись запрос+ответ всех инструментов → Tasks 1, 3; просмотр → Task 4.
- (Б) формат метаданных (tableparts) в промпте → Task 5.

**Placeholder scan:** код приведён целиком; имена `logCfgAI`/`cfgLogin`/`genResponseSummary`/`renderAIHistory`/`truncate`/`cfgAdminAIHistory`/`metadataFormatGuide` согласованы между задачами и вызовами.

**Type consistency:** `AIAuditEntry.Response` (Task 1) пишется/читается в `logCfgAI` (Task 3) и `renderAIHistory` (Task 4); `llm.Config.LogHistory` (Task 2) проверяется в `logCfgAI` (Task 3); `GenChange` уже существует. `cfgUserFromContext(ctx) *auth.User` (cfgauth.go:22) имеет `.Login`. `llm.ChatResponse` имеет `Model/InputTokens/OutputTokens` (types.go:66). `db.AddColumnIfMissing` (ddl.go:163), `storage.ConnectSQLite` — существующие.
