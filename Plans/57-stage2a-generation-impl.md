# Генерация каркаса (бэкенд, метаданные) — Implementation Plan (план 57)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ИИ через tool-use создаёт объекты-метаданные в staging-черновике, сам прогоняет check, исправляется; сервер возвращает diff. Без UI, без применения, без `.os`.

**Architecture:** Самодостаточная `genSession` (staging-оверлей конфигурации + инструменты + diff) в `internal/launcher/ai_generate.go`, тестируемая без LLM. Тонкий хендлер `cfgAIGenerate` зовёт `llm.RunWithTools` с инструментами сессии и срезом конфигурации (этап 1) в промпте.

**Tech Stack:** Go; `internal/configcheck`, `internal/project`, `internal/llm`, `internal/aicontext`.

**Дизайн:** [57-stage2a-generation.md](57-stage2a-generation.md). **Ветка:** `feature/57-stage2a-generation`.

---

## Структура файлов

- Создать: `internal/launcher/ai_generate.go` — `genSession`, `GenChange`, `tools`, `cfgAIGenerate`.
- Создать: `internal/launcher/ai_generate_test.go` — тесты `genSession` (без LLM).
- Изменить: `internal/launcher/server.go` — маршрут `POST .../configurator/ai-generate`.

---

## Task 1: genSession — overlay + createObject

**Files:**
- Create: `internal/launcher/ai_generate.go`
- Test: `internal/launcher/ai_generate_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/launcher/ai_generate_test.go`:

```go
package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

const validCatalogYAML = "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n"

func newTestGenSession(t *testing.T) *genSession {
	t.Helper()
	src := t.TempDir()
	g, err := newGenSession(src)
	if err != nil {
		t.Fatalf("newGenSession: %v", err)
	}
	t.Cleanup(g.close)
	return g
}

func TestGenCreateObject_WritesToOverlay(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createObject("справочник", "Клиент", validCatalogYAML); err != nil {
		t.Fatalf("createObject: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.overlay, "catalogs", "клиент.yaml"))
	if err != nil {
		t.Fatalf("файл не создан в overlay: %v", err)
	}
	if string(got) != validCatalogYAML {
		t.Errorf("содержимое не совпало: %q", got)
	}
	// исходная конфигурация не изменилась
	if _, err := os.Stat(filepath.Join(g.srcDir, "catalogs", "клиент.yaml")); !os.IsNotExist(err) {
		t.Error("исходный srcDir не должен меняться")
	}
}

func TestGenCreateObject_UnknownKind(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createObject("ракета", "X", "name: X\n"); err == nil {
		t.Error("ожидалась ошибка для неизвестного типа")
	}
}

func TestGenCreateObject_BadName(t *testing.T) {
	g := newTestGenSession(t)
	for _, bad := range []string{"", "../evil", "a/b", "a\\b"} {
		if err := g.createObject("справочник", bad, "name: X\n"); err == nil {
			t.Errorf("ожидалась ошибка для имени %q", bad)
		}
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет genSession):**

Run: `go test ./internal/launcher/ -run TestGenCreate -count=1`
Expected: FAIL — `genSession`/`newGenSession` не объявлены.

- [ ] **Step 3: Реализовать `internal/launcher/ai_generate.go` (часть 1)**

```go
package launcher

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// GenChange — один предложенный объект в diff генерации.
type GenChange struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // "новый" | "изменён"
	NewContent string `json:"newContent"`
	OldContent string `json:"oldContent,omitempty"`
}

// genSession — staging-оверлей конфигурации + накопленные изменения одной генерации.
type genSession struct {
	srcDir  string
	overlay string
	changed map[string]bool // относительные пути (slash) созданных/изменённых файлов
}

// kindSubdir сопоставляет тип объекта подкаталогу конфигурации (как в
// configcheck.CheckDir). Регистронезависимо, по синонимам.
func kindSubdir(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "справочник", "каталог", "catalog":
		return "catalogs", true
	case "документ", "document":
		return "documents", true
	case "регистр накопления", "регистрнакопления", "регистр", "register":
		return "registers", true
	case "регистр сведений", "регистрсведений", "inforegister":
		return "inforegs", true
	case "перечисление", "enum":
		return "enums", true
	case "план счетов", "плансчетов", "chartofaccounts":
		return "accounts", true
	case "регистр бухгалтерии", "регистрбухгалтерии", "accountregister":
		return "accountregs", true
	default:
		return "", false
	}
}

// safeFileName проверяет имя объекта и возвращает имя файла (lower + .yaml).
func safeFileName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", fmt.Errorf("пустое имя объекта")
	}
	if strings.ContainsAny(n, "/\\") || strings.Contains(n, "..") {
		return "", fmt.Errorf("недопустимое имя объекта: %q", name)
	}
	return strings.ToLower(n) + ".yaml", nil
}

// newGenSession делает рекурсивную копию srcDir во временный overlay.
func newGenSession(srcDir string) (*genSession, error) {
	overlay, err := os.MkdirTemp("", "onebase-gen-")
	if err != nil {
		return nil, err
	}
	if err := copyTree(srcDir, overlay); err != nil {
		os.RemoveAll(overlay)
		return nil, err
	}
	return &genSession{srcDir: srcDir, overlay: overlay, changed: map[string]bool{}}, nil
}

func (g *genSession) close() {
	if g.overlay != "" {
		os.RemoveAll(g.overlay)
	}
}

// createObject записывает YAML объекта в overlay по типу. Пишет только внутрь
// overlay (имя валидируется).
func (g *genSession) createObject(kind, name, yamlText string) error {
	subdir, ok := kindSubdir(kind)
	if !ok {
		return fmt.Errorf("неизвестный тип объекта: %q (допустимо: справочник, документ, регистр накопления, регистр сведений, перечисление, план счетов, регистр бухгалтерии)", kind)
	}
	fname, err := safeFileName(name)
	if err != nil {
		return err
	}
	rel := subdir + "/" + fname
	full := filepath.Join(g.overlay, subdir, fname)
	// защита: full обязан лежать внутри overlay
	cleanOverlay := filepath.Clean(g.overlay)
	if !strings.HasPrefix(filepath.Clean(full), cleanOverlay+string(os.PathSeparator)) {
		return fmt.Errorf("путь вне overlay: %q", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(yamlText), 0o644); err != nil {
		return err
	}
	g.changed[rel] = true
	return nil
}

// copyTree рекурсивно копирует содержимое src в dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
```

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/launcher/ -run TestGenCreate -count=1`
Expected: PASS (три теста).

- [ ] **Step 5: gofmt-чисто:**

Run: `gofmt -d internal/launcher/ai_generate.go internal/launcher/ai_generate_test.go`
Expected: пусто (CRLF-артефакты целого файла игнорировать; править только точечные отступы `gofmt -w`).

- [ ] **Step 6: Commit:**

```
git add internal/launcher/ai_generate.go internal/launcher/ai_generate_test.go
git commit -m "feat(configurator): genSession — staging-оверлей и создать_объект (план 57, этап 2a)"
```

---

## Task 2: check + showObject + diff

**Files:**
- Modify: `internal/launcher/ai_generate.go`
- Test: `internal/launcher/ai_generate_test.go`

- [ ] **Step 1: Дописать падающие тесты**

Добавить в `internal/launcher/ai_generate_test.go`:

```go
func TestGenCheck_ReportsBadYAML(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createObject("документ", "Заявка", "name: Заявка\nfields: [oops"); err != nil {
		t.Fatalf("createObject: %v", err)
	}
	out := g.check()
	if !strings.Contains(out, "Заявка") {
		t.Errorf("check не сообщил об ошибке битого документа: %s", out)
	}
}

func TestGenCheck_CleanIsOK(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createObject("справочник", "Клиент", validCatalogYAML); err != nil {
		t.Fatalf("createObject: %v", err)
	}
	if out := g.check(); !strings.Contains(strings.ToLower(out), "нет ошибок") {
		t.Errorf("ожидалось «нет ошибок», получено: %s", out)
	}
}

func TestGenDiff_ListsNew(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createObject("справочник", "Клиент", validCatalogYAML); err != nil {
		t.Fatalf("createObject: %v", err)
	}
	d := g.diff()
	if len(d) != 1 || d[0].Path != "catalogs/клиент.yaml" || d[0].Kind != "новый" || d[0].NewContent != validCatalogYAML {
		t.Fatalf("diff неверный: %+v", d)
	}
}

func TestGenShowObject_ReadsExisting(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "catalogs", "товар.yaml"), []byte("name: Товар\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := newGenSession(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.close)
	if out := g.showObject("Товар"); !strings.Contains(out, "name: Товар") {
		t.Errorf("showObject не вернул YAML: %q", out)
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет check/diff/showObject):**

Run: `go test ./internal/launcher/ -run TestGen -count=1`
Expected: FAIL — методов нет.

- [ ] **Step 3: Дописать методы в `internal/launcher/ai_generate.go`**

Добавить импорты `"github.com/ivantit66/onebase/internal/configcheck"` и
`"github.com/ivantit66/onebase/internal/project"` к блоку импортов, и методы:

```go
// check валидирует overlay без исполнения кода: CheckDir (парс YAML) + project.Load
// (кросс-ссылки; модули парсятся, не исполняются). CheckQueries НЕ зовём — он
// исполняет запросы. Возвращает человекочитаемый текст для модели.
func (g *genSession) check() string {
	issues, _ := configcheck.CheckDir(g.overlay)
	if proj, err := project.Load(g.overlay); err == nil {
		proj.Close()
	} else if !configcheck.AlreadyReported(issues, err.Error()) {
		issues = append(issues, configcheck.Issue{Message: "Project.Load: " + err.Error()})
	}
	if len(issues) == 0 {
		return "Ошибок нет."
	}
	var b strings.Builder
	b.WriteString("Найдены ошибки:\n")
	for _, is := range issues {
		if is.File != "" {
			fmt.Fprintf(&b, "- %s: %s\n", is.File, is.Message)
		} else {
			fmt.Fprintf(&b, "- %s\n", is.Message)
		}
	}
	return b.String()
}

// showObject возвращает YAML существующего объекта (ищет по имени во всех
// подкаталогах метаданных overlay). Для контекста модели.
func (g *genSession) showObject(name string) string {
	fname, err := safeFileName(name)
	if err != nil {
		return "ошибка: " + err.Error()
	}
	for _, sub := range []string{"catalogs", "documents", "registers", "inforegs", "enums", "accounts", "accountregs"} {
		p := filepath.Join(g.overlay, sub, fname)
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("объект %q не найден", name)
}

// diff возвращает предложенные изменения (по changed): новый или изменён.
func (g *genSession) diff() []GenChange {
	rels := make([]string, 0, len(g.changed))
	for rel := range g.changed {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	out := make([]GenChange, 0, len(rels))
	for _, rel := range rels {
		newData, err := os.ReadFile(filepath.Join(g.overlay, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		ch := GenChange{Path: rel, Kind: "новый", NewContent: string(newData)}
		if oldData, err := os.ReadFile(filepath.Join(g.srcDir, filepath.FromSlash(rel))); err == nil {
			ch.Kind = "изменён"
			ch.OldContent = string(oldData)
		}
		out = append(out, ch)
	}
	return out
}
```

Добавить `"sort"` в импорты. Проверь, что `configcheck.AlreadyReported(issues []Issue, msg string) bool` существует (используется в `check_handlers.go:81`); если сигнатура иная — приведи вызов к фактической.

- [ ] **Step 4: Запустить — PASS:**

Run: `go test ./internal/launcher/ -run TestGen -count=1`
Expected: PASS (все TestGen*).

- [ ] **Step 5: gofmt + commit:**

Run: `gofmt -d internal/launcher/ai_generate.go` → пусто.
```
git add internal/launcher/ai_generate.go internal/launcher/ai_generate_test.go
git commit -m "feat(configurator): genSession check/showObject/diff (план 57, этап 2a)"
```

---

## Task 3: tools() + cfgAIGenerate + маршрут

**Files:**
- Modify: `internal/launcher/ai_generate.go`
- Modify: `internal/launcher/server.go`
- Test: `internal/launcher/ai_generate_test.go`

- [ ] **Step 1: Дописать падающий тест**

Добавить в `internal/launcher/ai_generate_test.go` (импорт `"context"` и
`"github.com/ivantit66/onebase/internal/llm"` добавить в тест-файл):

```go
func TestGenTools_Dispatch(t *testing.T) {
	g := newTestGenSession(t)
	tools, exec := g.tools()
	if len(tools) != 3 {
		t.Fatalf("ожидалось 3 инструмента, получено %d", len(tools))
	}
	res := exec(context.Background(), llm.ToolCall{
		ID:   "1",
		Name: "создать_объект",
		Input: map[string]any{"тип": "справочник", "имя": "Клиент", "yaml": validCatalogYAML},
	})
	if res.IsError {
		t.Fatalf("создать_объект вернул ошибку: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(g.overlay, "catalogs", "клиент.yaml")); err != nil {
		t.Errorf("инструмент не записал объект: %v", err)
	}
	chk := exec(context.Background(), llm.ToolCall{ID: "2", Name: "проверить_конфигурацию", Input: map[string]any{}})
	if chk.IsError {
		t.Errorf("проверить_конфигурацию не должен быть ошибкой: %s", chk.Content)
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет tools):**

Run: `go test ./internal/launcher/ -run TestGenTools -count=1`
Expected: FAIL — метода `tools` нет.

- [ ] **Step 3: Добавить `tools()` в `internal/launcher/ai_generate.go`**

Добавить импорты `"context"` и `"github.com/ivantit66/onebase/internal/llm"`, затем:

```go
func strInput(call llm.ToolCall, key string) string {
	if v, ok := call.Input[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// tools формирует инструменты записи в staging для RunWithTools.
func (g *genSession) tools() ([]llm.Tool, llm.ToolExecutor) {
	tools := []llm.Tool{
		{
			Name:        "создать_объект",
			Description: "Создать черновик объекта метаданных в конфигурации. тип: справочник|документ|регистр накопления|регистр сведений|перечисление|план счетов|регистр бухгалтерии. имя — на русском. yaml — содержимое файла объекта (без модулей .os).",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"тип":  map[string]any{"type": "string"},
					"имя":  map[string]any{"type": "string"},
					"yaml": map[string]any{"type": "string"},
				},
				"required": []any{"тип", "имя", "yaml"},
			},
		},
		{
			Name:        "проверить_конфигурацию",
			Description: "Проверить черновик конфигурации (валидность YAML и ссылки). Вызывай после создания объектов и исправляй найденные ошибки.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "показать_объект",
			Description: "Показать YAML существующего объекта по имени — чтобы повторно использовать его поля/типы.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"имя": map[string]any{"type": "string"}},
				"required":   []any{"имя"},
			},
		},
	}
	exec := func(_ context.Context, call llm.ToolCall) llm.ToolResult {
		switch call.Name {
		case "создать_объект":
			if err := g.createObject(strInput(call, "тип"), strInput(call, "имя"), strInput(call, "yaml")); err != nil {
				return llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true}
			}
			return llm.ToolResult{ID: call.ID, Content: "создан объект " + strInput(call, "имя")}
		case "проверить_конфигурацию":
			return llm.ToolResult{ID: call.ID, Content: g.check()}
		case "показать_объект":
			return llm.ToolResult{ID: call.ID, Content: g.showObject(strInput(call, "имя"))}
		default:
			return llm.ToolResult{ID: call.ID, Content: "неизвестный инструмент: " + call.Name, IsError: true}
		}
	}
	return tools, exec
}
```

- [ ] **Step 4: Запустить тест — PASS:**

Run: `go test ./internal/launcher/ -run TestGenTools -count=1`
Expected: PASS.

- [ ] **Step 5: Добавить хендлер `cfgAIGenerate`**

В `internal/launcher/ai_generate.go` добавить импорты `"encoding/json"`, `"net/http"`,
`"time"`, `"github.com/go-chi/chi/v5"`. Открой `internal/launcher/ai_assist.go` и
повтори структуру `cfgAIAssist` (получение базы, декод запроса, `getAuthDB`,
`GetLLMConfig`, таймаут-контекст) для:

```go
// aiGenerateSystem — роль генератора каркаса конфигурации.
var aiGenerateSystem = "Ты — генератор каркаса конфигурации OneBase (платформа учёта, похожая на 1С). " +
	"По описанию задачи на русском создавай объекты метаданных через инструмент «создать_объект»: " +
	"справочники, документы (с табличными частями), регистры, перечисления. Только метаданные YAML — " +
	"без модулей .os (проводки/обработчики на этом шаге не генерируются). " +
	"После создания набора объектов обязательно вызывай «проверить_конфигурацию» и исправляй ошибки. " +
	"Используй существующие объекты (через «показать_объект») вместо дублирования. " +
	"Имена и типы полей бери реальные; не выдумывай несуществующие типы. Известные функции: " + builtinReference

// cfgAIGenerate — генерация каркаса конфигурации по ТЗ в staging-черновик.
// Возвращает предложенный diff; рабочую конфигурацию НЕ меняет (применение — этап 2b).
func (h *handler) cfgAIGenerate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "Некорректный запрос"})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
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

	dir, cleanup, err := materializeProject(ctx, h, b)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "не удалось получить конфигурацию: " + err.Error()})
		return
	}
	if cleanup != nil {
		defer cleanup()
	}
	g, err := newGenSession(dir)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": "не удалось создать черновик: " + err.Error()})
		return
	}
	defer g.close()

	system := aiGenerateSystem
	if schema := projectSchemaText(mustLoadProject(dir)); schema != "" {
		system += "\n\nТекущая конфигурация базы:\n" + schema
	}

	tools, exec := g.tools()
	runner := llm.New(cfg, nil)
	resp, err := runner.RunWithTools(ctx, "конфигуратор", llm.ChatRequest{
		System:   system,
		Messages: []llm.Message{llm.UserText(req.Prompt)},
	}, tools, exec)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model, "changes": g.diff()})
}
```

ВАЖНО: `mustLoadProject` в коде нет — НЕ используй его. Вместо строки со схемой
используй уже существующий best-effort хелпер `h.configSchemaText(ctx, b)` (этап 1):
```go
	system := aiGenerateSystem
	if schema := h.configSchemaText(ctx, b); schema != "" {
		system += "\n\nТекущая конфигурация базы:\n" + schema
	}
```
(`configSchemaText` сам грузит проект; небольшая повторная загрузка приемлема, как
обсуждено в этапе 1.) Удали черновую строку с `mustLoadProject`.

Подтверди по `ai_assist.go`: `writeJSON`, `getAuthDB`, `materializeProject`,
`llm.UserText`, `llm.SafeErr`, `builtinReference` существуют и используются так же.
Если сигнатура `materializeProject`/`getAuthDB` иная — приведи вызов к фактической.

- [ ] **Step 6: Зарегистрировать маршрут**

В `internal/launcher/server.go` рядом со строкой 186
(`r.Post("/bases/{id}/configurator/ai-assist", s.h.cfgAIAssist)`) добавить:
```go
		r.Post("/bases/{id}/configurator/ai-generate", s.h.cfgAIGenerate)
```

- [ ] **Step 7: Сборка + тесты пакета**

Run: `go build ./internal/launcher/ ./cmd/onebase` → успех (если бинарь залочен — `taskkill /IM onebase.exe /F`).
Run: `go test ./internal/launcher/ -run TestGen -count=1` → PASS.

- [ ] **Step 8: Commit:**

```
git add internal/launcher/ai_generate.go internal/launcher/server.go internal/launcher/ai_generate_test.go
git commit -m "feat(configurator): эндпоинт ai-generate — tool-use генерация каркаса в staging (план 57, этап 2a)"
```

---

## Task 4: Верификация и статус

**Files:**
- Modify: `Plans/57-stage2a-generation.md`

- [ ] **Step 1: Полный прогон**

Run: `go test ./... -count=1` → PASS (без FAIL).
Run: `go vet ./...` → чисто.
Run: `gofmt -d internal/launcher/ai_generate.go` → пусто (реальные правки; CRLF игнор).

- [ ] **Step 2: Обновить статус**

В `Plans/57-stage2a-generation.md` заменить `**Статус:** дизайн утверждён, ожидает плана реализации` на `**Статус:** ✅ Реализовано (этап 2a)`.

- [ ] **Step 3: Commit:**

```
git add Plans/57-stage2a-generation.md
git commit -m "docs(plans): этап 2a плана 57 (генерация каркаса, бэкенд) реализован"
```

---

## Self-Review

**Spec coverage:**
- `genSession` + overlay + `createObject` (тип→подкаталог, валидация имени) → Task 1.
- `check` (CheckDir+project.Load, без CheckQueries) + `showObject` + `diff` → Task 2.
- `tools` (3 инструмента) + `cfgAIGenerate` (RunWithTools + срез этапа 1) + маршрут → Task 3.
- Метаданные-только, без UI/применения/.os; запись только в overlay; admin-зона — соблюдено.

**Placeholder scan:** код приведён целиком. Единственная намеренная «ловушка»
(`mustLoadProject`) явно помечена как НЕ использовать с заменой на `configSchemaText`
(этап 1) — чтобы исполнитель не изобрёл новый хелпер.

**Type consistency:** `genSession`/`GenChange`/`kindSubdir`/`safeFileName`/`copyTree`
(Task 1) используются в `check`/`diff`/`showObject` (Task 2) и `tools`/`cfgAIGenerate`
(Task 3). `llm.Tool`/`ToolCall`/`ToolResult`/`ToolExecutor` — из `internal/llm/tools.go`.
`configcheck.CheckDir`/`Issue`/`AlreadyReported`, `project.Load`, `h.configSchemaText`
(этап 1) — существующие.
