# describe-контекст для модели — Implementation Plan (план 57)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Дать ИИ компактный точный срез конфигурации (объекты, поля, ТЧ, проведение, формы) — общим пакетом для пользовательского чата и конфигуратора, который раньше конфигурацию не видел.

**Architecture:** Новый пакет `internal/aicontext` (зависит только от `metadata`) с `SchemaText(Input) string`. `ui.aiSchemaText` делегирует ему; конфигуратор (`cfgAIAssist`) грузит проект (`materializeProject`+`project.Load`) и добавляет срез в системный промпт.

**Tech Stack:** Go; тесты `testing`.

**Дизайн:** [57-stage1-describe-context.md](57-stage1-describe-context.md). **Ветка:** `feature/57-stage1-describe-context`.

---

## Структура файлов

- Создать: `internal/aicontext/aicontext.go` — `NamedTitle`, `Input`, `SchemaText`.
- Создать: `internal/aicontext/aicontext_test.go` — юнит-тесты сборщика.
- Изменить: `internal/ui/ai_tools.go` — `aiSchemaText` делегирует в `aicontext`.
- Изменить: `internal/ui/ai_tools_test.go` — добавить тест ТЧ/проведения через ui-путь.
- Изменить: `internal/launcher/ai_assist.go` — `projectSchemaText` + `configSchemaText` + проводка в `cfgAIAssist`.
- Изменить: `internal/launcher/ai_assist_test.go` (создать) — тест `projectSchemaText`.

---

## Task 1: пакет `internal/aicontext`

**Files:**
- Create: `internal/aicontext/aicontext.go`
- Test: `internal/aicontext/aicontext_test.go`

- [ ] **Step 1: Написать падающий тест**

Создать `internal/aicontext/aicontext_test.go`:

```go
package aicontext

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestSchemaText_Entities(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}},
		},
		Forms: []*metadata.FormModule{{Name: "ФормаЗаказа", Kind: "object"}},
	}
	cat := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	txt := SchemaText(Input{
		Entities: []*metadata.Entity{doc, cat},
		Enums:    []*metadata.Enum{{Name: "Статус", Values: []string{"Новый", "Закрыт"}}},
		Reports:  []NamedTitle{{Name: "Продажи", Title: "Отчёт продаж"}},
	})
	for _, sub := range []string{
		"Документы:", "Заказ", "(проводится)", "ТЧ Товары", "Количество",
		"формы: ФормаЗаказа (object)", "Справочники:", "Клиент", "Наименование",
		"Перечисления:", "Статус", "Закрыт", "Отчёты", "Продажи — Отчёт продаж",
	} {
		if !strings.Contains(txt, sub) {
			t.Errorf("в срезе нет %q:\n%s", sub, txt)
		}
	}
}

func TestSchemaText_Empty(t *testing.T) {
	if got := SchemaText(Input{}); !strings.Contains(got, "нет объектов") {
		t.Errorf("ожидалась заглушка для пустого Input, получено %q", got)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что не компилируется**

Run: `go test ./internal/aicontext/ -count=1`
Expected: FAIL — пакета `aicontext` ещё нет.

- [ ] **Step 3: Реализовать пакет**

Создать `internal/aicontext/aicontext.go`:

```go
// Package aicontext строит компактный текстовый срез конфигурации для системного
// промпта ИИ (пользовательский чат и конфигуратор). Зависит только от metadata —
// чтобы и runtime.Registry, и project.Project могли заполнить общий Input.
package aicontext

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// NamedTitle — имя + синоним для объектов, у которых в срез идут только они
// (отчёты, обработки). Позволяет не тащить пакеты report/processor в aicontext.
type NamedTitle struct{ Name, Title string }

// Input — срезы метаданных для построения текстового контекста.
type Input struct {
	Entities         []*metadata.Entity
	Registers        []*metadata.Register
	InfoRegisters    []*metadata.InfoRegister
	AccountRegisters []*metadata.AccountRegister
	ChartsOfAccounts []*metadata.ChartOfAccounts
	Enums            []*metadata.Enum
	Constants        []*metadata.Constant
	Reports          []NamedTitle
	Processors       []NamedTitle
	Journals         []*metadata.Journal
	Subsystems       []*metadata.Subsystem
}

func fieldNames(fs []metadata.Field) string {
	names := make([]string, 0, len(fs))
	for _, f := range fs {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

// nameTitle — «Имя — Заголовок», если заголовок задан и отличается от имени.
func nameTitle(name, title string) string {
	if title != "" && title != name {
		return name + " — " + title
	}
	return name
}

// SchemaText возвращает компактный текстовый срез конфигурации.
func SchemaText(in Input) string {
	var b strings.Builder
	var catalogs, documents []*metadata.Entity
	for _, e := range in.Entities {
		switch e.Kind {
		case metadata.KindCatalog:
			catalogs = append(catalogs, e)
		case metadata.KindDocument:
			documents = append(documents, e)
		}
	}
	writeEntity := func(e *metadata.Entity, markPosting bool) {
		head := "  " + e.Name
		if markPosting && e.Posting {
			head += " (проводится)"
		}
		fmt.Fprintf(&b, "%s: %s\n", head, fieldNames(e.Fields))
		for _, tp := range e.TableParts {
			fmt.Fprintf(&b, "    ТЧ %s: %s\n", tp.Name, fieldNames(tp.Fields))
		}
		if len(e.Forms) > 0 {
			parts := make([]string, 0, len(e.Forms))
			for _, f := range e.Forms {
				if f.Kind != "" {
					parts = append(parts, f.Name+" ("+f.Kind+")")
				} else {
					parts = append(parts, f.Name)
				}
			}
			fmt.Fprintf(&b, "    формы: %s\n", strings.Join(parts, ", "))
		}
	}
	if len(catalogs) > 0 {
		b.WriteString("Справочники:\n")
		for _, e := range catalogs {
			writeEntity(e, false)
		}
	}
	if len(documents) > 0 {
		b.WriteString("Документы:\n")
		for _, e := range documents {
			writeEntity(e, true)
		}
	}
	if len(in.Registers) > 0 {
		b.WriteString("Регистры накопления (доступны .Остатки/.Обороты):\n")
		for _, rg := range in.Registers {
			fmt.Fprintf(&b, "  %s: измерения [%s]; ресурсы [%s]\n", rg.Name, fieldNames(rg.Dimensions), fieldNames(rg.Resources))
		}
	}
	if len(in.InfoRegisters) > 0 {
		b.WriteString("Регистры сведений (доступен .СрезПоследних):\n")
		for _, ir := range in.InfoRegisters {
			fmt.Fprintf(&b, "  %s: измерения [%s]; ресурсы [%s]\n", ir.Name, fieldNames(ir.Dimensions), fieldNames(ir.Resources))
		}
	}
	if len(in.ChartsOfAccounts) > 0 {
		b.WriteString("Планы счетов:\n")
		for _, ch := range in.ChartsOfAccounts {
			codes := make([]string, 0, len(ch.Accounts))
			for _, a := range ch.Accounts {
				codes = append(codes, a.Code)
			}
			fmt.Fprintf(&b, "  %s: счета %s\n", nameTitle(ch.Name, ch.Title), strings.Join(codes, ", "))
		}
	}
	if len(in.AccountRegisters) > 0 {
		b.WriteString("Регистры бухгалтерии (доступны .Остатки/.Обороты по счетам и субконто):\n")
		for _, ar := range in.AccountRegisters {
			fmt.Fprintf(&b, "  %s: ресурсы [%s]; субконто [%s]; план счетов %s\n", nameTitle(ar.Name, ar.Title), fieldNames(ar.Resources), fieldNames(ar.Subconto), ar.Accounts)
		}
	}
	if len(in.Enums) > 0 {
		b.WriteString("Перечисления:\n")
		for _, en := range in.Enums {
			fmt.Fprintf(&b, "  %s: %s\n", en.Name, strings.Join(en.Values, ", "))
		}
	}
	if len(in.Constants) > 0 {
		names := make([]string, 0, len(in.Constants))
		for _, c := range in.Constants {
			names = append(names, c.Name)
		}
		fmt.Fprintf(&b, "Константы: %s\n", strings.Join(names, ", "))
	}
	if len(in.Reports) > 0 {
		b.WriteString("Отчёты (готовые, открываются в интерфейсе; не используются как таблицы в запросах):\n")
		for _, rp := range in.Reports {
			fmt.Fprintf(&b, "  %s\n", nameTitle(rp.Name, rp.Title))
		}
	}
	if len(in.Processors) > 0 {
		b.WriteString("Обработки (запускаются в интерфейсе):\n")
		for _, p := range in.Processors {
			fmt.Fprintf(&b, "  %s\n", nameTitle(p.Name, p.Title))
		}
	}
	if len(in.Journals) > 0 {
		b.WriteString("Журналы документов:\n")
		for _, j := range in.Journals {
			fmt.Fprintf(&b, "  %s: документы [%s]\n", nameTitle(j.Name, j.Title), strings.Join(j.Documents, ", "))
		}
	}
	if len(in.Subsystems) > 0 {
		b.WriteString("Подсистемы (разделы интерфейса):\n")
		for _, sub := range in.Subsystems {
			fmt.Fprintf(&b, "  %s\n", nameTitle(sub.Name, sub.Title))
		}
	}
	if b.Len() == 0 {
		return "В конфигурации нет объектов для запроса."
	}
	return b.String()
}
```

IMPORTANT перед написанием: открой `internal/metadata/types.go` и подтверди поля —
`Entity{Name,Kind,Fields,TableParts,Posting,Forms}`, `TablePart{Name,Fields}`,
`FormModule{Name,Kind}`, `Register{Name,Dimensions,Resources}`,
`InfoRegister{Name,Dimensions,Resources}`, `ChartOfAccounts{Name,Title,Accounts[].Code}`,
`AccountRegister{Name,Title,Resources,Subconto,Accounts}`, `Enum{Name,Values}`,
`Constant{Name}`, `Journal{Name,Title,Documents}`, `Subsystem{Name,Title}`. Если имя
поля отличается — STOP, report NEEDS_CONTEXT.

- [ ] **Step 4: Запустить тест — PASS**

Run: `go test ./internal/aicontext/ -count=1`
Expected: PASS (оба теста).

- [ ] **Step 5: Commit**

```
git add internal/aicontext/
git commit -m "feat(aicontext): пакет компактного среза конфигурации для ИИ (план 57, этап 1)"
```

---

## Task 2: `ui.aiSchemaText` делегирует в `aicontext`

**Files:**
- Modify: `internal/ui/ai_tools.go`
- Test: `internal/ui/ai_tools_test.go`

- [ ] **Step 1: Написать падающий тест**

Добавить в `internal/ui/ai_tools_test.go` (рядом с другими; импорты `metadata`/`strings` уже есть):

```go
// Делегирование в aicontext: ui-путь теперь отдаёт ТЧ и пометку проведения.
func TestAISchemaText_TablePartsAndPosting(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}},
		},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	txt := s.aiSchemaText()
	for _, sub := range []string{"Заказ", "(проводится)", "ТЧ Товары", "Количество"} {
		if !strings.Contains(txt, sub) {
			t.Fatalf("в срезе нет %q: %s", sub, txt)
		}
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что FAIL**

Run: `go test ./internal/ui/ -run TestAISchemaText -count=1`
Expected: FAIL — текущий `aiSchemaText` не выводит ТЧ/проведение.

- [ ] **Step 3: Заменить тело `aiSchemaText`**

В `internal/ui/ai_tools.go` заменить всю функцию `aiSchemaText` (от `func (s *Server) aiSchemaText() string {` до её закрывающей `}`) на делегирование:

```go
// aiSchemaText кратко описывает доступные объекты конфигурации для модели.
func (s *Server) aiSchemaText() string {
	reports := make([]aicontext.NamedTitle, 0)
	for _, rp := range s.reg.Reports() {
		reports = append(reports, aicontext.NamedTitle{Name: rp.Name, Title: rp.Title})
	}
	procs := make([]aicontext.NamedTitle, 0)
	for _, p := range s.reg.Processors() {
		procs = append(procs, aicontext.NamedTitle{Name: p.Name, Title: p.Title})
	}
	return aicontext.SchemaText(aicontext.Input{
		Entities:         s.reg.Entities(),
		Registers:        s.reg.Registers(),
		InfoRegisters:    s.reg.InfoRegisters(),
		AccountRegisters: s.reg.AccountRegisters(),
		ChartsOfAccounts: s.reg.ChartsOfAccounts(),
		Enums:            s.reg.Enums(),
		Constants:        s.reg.Constants(),
		Reports:          reports,
		Processors:       procs,
		Journals:         s.reg.Journals(),
		Subsystems:       s.reg.Subsystems(),
	})
}
```

Затем добавить импорт `"github.com/ivantit66/onebase/internal/aicontext"` и запустить
`goimports`/`go build` — удалить ставшие неиспользуемыми импорты (вероятно `metadata`,
а `fmt` оставить только если используется в другом месте файла). Если `go build`
ругается на неиспользуемый импорт — убрать его.

- [ ] **Step 4: Запустить тесты — PASS**

Run: `go test ./internal/ui/ -run TestAISchemaText -count=1`
Expected: PASS (новый тест + существующие `TestAISchemaText`, `TestAISchemaText_NonDataObjects` — формат сохранён).

- [ ] **Step 5: Полный пакет ui — без регрессий**

Run: `go test ./internal/ui/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```
git add internal/ui/ai_tools.go internal/ui/ai_tools_test.go
git commit -m "refactor(ui): aiSchemaText делегирует в aicontext (+ ТЧ/проведение/формы) (план 57, этап 1)"
```

---

## Task 3: срез конфигурации в конфигураторе (`cfgAIAssist`)

**Files:**
- Modify: `internal/launcher/ai_assist.go`
- Test: `internal/launcher/ai_assist_test.go` (создать)

- [ ] **Step 1: Написать падающий тест**

Создать `internal/launcher/ai_assist_test.go`:

```go
package launcher

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
)

// projectSchemaText строит срез из загруженного проекта — проверяем на литерале.
func TestProjectSchemaText(t *testing.T) {
	proj := &project.Project{
		Entities: []*metadata.Entity{{
			Name: "Заявка", Kind: metadata.KindDocument, Posting: true,
			Fields: []metadata.Field{{Name: "Дата", Type: metadata.FieldTypeDate}},
			TableParts: []metadata.TablePart{
				{Name: "Строки", Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}}},
			},
		}},
		Reports: []*report.Report{{Name: "Сводка", Title: "Сводный отчёт"}},
	}
	txt := projectSchemaText(proj)
	for _, sub := range []string{"Заявка", "(проводится)", "ТЧ Строки", "Сводка — Сводный отчёт"} {
		if !strings.Contains(txt, sub) {
			t.Fatalf("в срезе нет %q: %s", sub, txt)
		}
	}
}
```

- [ ] **Step 2: Запустить — FAIL (нет projectSchemaText)**

Run: `go test ./internal/launcher/ -run TestProjectSchemaText -count=1`
Expected: FAIL — функции `projectSchemaText` нет.

- [ ] **Step 3: Добавить `projectSchemaText` и `configSchemaText`**

В `internal/launcher/ai_assist.go` добавить импорты `"context"` (уже есть),
`"github.com/ivantit66/onebase/internal/aicontext"`,
`"github.com/ivantit66/onebase/internal/project"` и функции:

```go
// projectSchemaText строит текстовый срез конфигурации из загруженного проекта.
func projectSchemaText(proj *project.Project) string {
	reports := make([]aicontext.NamedTitle, 0, len(proj.Reports))
	for _, rp := range proj.Reports {
		reports = append(reports, aicontext.NamedTitle{Name: rp.Name, Title: rp.Title})
	}
	procs := make([]aicontext.NamedTitle, 0, len(proj.Processors))
	for _, p := range proj.Processors {
		procs = append(procs, aicontext.NamedTitle{Name: p.Name, Title: p.Title})
	}
	return aicontext.SchemaText(aicontext.Input{
		Entities:         proj.Entities,
		Registers:        proj.Registers,
		InfoRegisters:    proj.InfoRegisters,
		AccountRegisters: proj.AccountRegisters,
		ChartsOfAccounts: proj.ChartsOfAccounts,
		Enums:            proj.Enums,
		Constants:        proj.Constants,
		Reports:          reports,
		Processors:       procs,
		Journals:         proj.Journals,
		Subsystems:       proj.Subsystems,
	})
}

// configSchemaText грузит метаданные базы и строит срез для системного промпта.
// Best-effort: при любой ошибке возвращает "" (помощник работает по builtin-списку).
func (h *handler) configSchemaText(ctx context.Context, b *Base) string {
	dir, cleanup, err := materializeProject(ctx, h, b)
	if err != nil {
		return ""
	}
	if cleanup != nil {
		defer cleanup()
	}
	proj, err := project.Load(dir)
	if err != nil {
		return ""
	}
	defer proj.Close()
	return projectSchemaText(proj)
}
```

IMPORTANT: подтверди по `internal/launcher/check_handlers.go:60,92` сигнатуру
`materializeProject(ctx context.Context, h *handler, b *Base) (dir string, cleanup func(), err error)`
и по `internal/project/loader.go:30-50` поля `Project` (Entities/Registers/.../ChartsOfAccounts/
AccountRegisters/Reports/Processors/Journals/Subsystems). Если расходится — STOP, NEEDS_CONTEXT.

- [ ] **Step 4: Проводка в `cfgAIAssist`**

В `internal/launcher/ai_assist.go`, в `cfgAIAssist`, найти место формирования запроса
(`runner.Run(ctx, "конфигуратор", llm.ChatRequest{System: aiAssistSystem, ...})`) и
заменить так, чтобы system строился per-request со срезом:

Перед `runner := llm.New(cfg, nil)` добавить:
```go
	system := aiAssistSystem
	if schema := h.configSchemaText(r.Context(), b); schema != "" {
		system += "\n\nТекущая конфигурация базы (объекты, поля, ТЧ, формы):\n" + schema
	}
```
И в вызове `runner.Run(...)` заменить `System: aiAssistSystem` на `System: system`.

- [ ] **Step 5: Запустить тест — PASS**

Run: `go test ./internal/launcher/ -run TestProjectSchemaText -count=1`
Expected: PASS.

- [ ] **Step 6: Полный пакет launcher + сборка**

Run: `go test ./internal/launcher/ -count=1`
Expected: PASS.
Run: `go build ./cmd/onebase`
Expected: успех (предварительно `taskkill /IM onebase.exe /F`, если бинарь залочен).

- [ ] **Step 7: Commit**

```
git add internal/launcher/ai_assist.go internal/launcher/ai_assist_test.go
git commit -m "feat(configurator): срез конфигурации в системном промпте ИИ-помощника (план 57, этап 1)"
```

---

## Task 4: Верификация и статус

**Files:**
- Modify: `Plans/57-stage1-describe-context.md`

- [ ] **Step 1: Полный прогон + vet + сборка**

Run: `go test ./... -count=1` → PASS (без FAIL).
Run: `go vet ./...` → чисто.
Run: `gofmt -d internal/aicontext/aicontext.go internal/ui/ai_tools.go internal/launcher/ai_assist.go` → пусто (CRLF-артефакты игнорировать: реальная правка — точечные отступы, не весь файл).

- [ ] **Step 2: Обновить статус**

В `Plans/57-stage1-describe-context.md` заменить строку `**Статус:** дизайн утверждён, ожидает плана реализации` на `**Статус:** ✅ Реализовано (этап 1)`.

- [ ] **Step 3: Commit**

```
git add Plans/57-stage1-describe-context.md
git commit -m "docs(plans): этап 1 плана 57 (describe-контекст) реализован"
```

---

## Self-Review

**Spec coverage:**
- Пакет `aicontext` (Input/NamedTitle/SchemaText) с ТЧ/проведением/формами → Task 1.
- `ui.aiSchemaText` делегирует → Task 2 (адаптеры reports/processors, существующий формат сохранён).
- Конфигуратор получает срез (materializeProject+project.Load, best-effort) → Task 3.
- Формат — текст; YAGNI (без модулей/полного YAML, проводки = флаг) — соблюдено.

**Placeholder scan:** заглушек нет; весь код приведён, команды и ожидаемый результат указаны. Места, где имена полей надо подтвердить чтением, помечены явно (NEEDS_CONTEXT при расхождении).

**Type consistency:** `aicontext.Input`/`NamedTitle`/`SchemaText` едины во всех трёх задачах; адаптеры `[]NamedTitle` собираются одинаково в ui (из `s.reg`) и launcher (из `proj`). Поля `project.Project` и `runtime.Registry` дают одинаковую метамодель (`[]*metadata.*`).
