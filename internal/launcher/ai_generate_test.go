package launcher

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/project"
)

const validCatalogYAML = "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n"

type fakeGenRunner struct {
	calls int
	run   func(call int, ctx context.Context, task string, req llm.ChatRequest, tools []llm.Tool, exec llm.ToolExecutor) (llm.ChatResponse, error)
}

func (f *fakeGenRunner) RunWithTools(ctx context.Context, task string, req llm.ChatRequest, tools []llm.Tool, exec llm.ToolExecutor) (llm.ChatResponse, error) {
	f.calls++
	if f.run != nil {
		return f.run(f.calls, ctx, task, req, tools, exec)
	}
	return llm.ChatResponse{Text: "ok", Model: "fake"}, nil
}

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

func TestGenTools_Dispatch(t *testing.T) {
	g := newTestGenSession(t)
	tools, exec := g.tools()
	if len(tools) < 9 {
		t.Fatalf("ожидалось расширенное число инструментов, получено %d", len(tools))
	}
	res := exec(context.Background(), llm.ToolCall{
		ID:    "1",
		Name:  "создать_объект",
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
	fmtRes := exec(context.Background(), llm.ToolCall{ID: "3", Name: "форматировать", Input: map[string]any{}})
	if fmtRes.IsError || !strings.Contains(fmtRes.Content, "YAML") && !strings.Contains(fmtRes.Content, "Отформатировано") {
		t.Errorf("форматировать вернул неожиданный результат: %+v", fmtRes)
	}
}

func TestGenSelfCorrectionRetriesUntilCheckOK(t *testing.T) {
	g := newTestGenSession(t)
	tools, exec := g.tools()
	runner := &fakeGenRunner{run: func(call int, ctx context.Context, task string, req llm.ChatRequest, tools []llm.Tool, exec llm.ToolExecutor) (llm.ChatResponse, error) {
		if call == 1 {
			res := exec(ctx, llm.ToolCall{
				ID:    "bad",
				Name:  "создать_файл",
				Input: map[string]any{"путь": "documents/заявка.yaml", "содержимое": "fields:\n  - name: Номер\n    type: string\n"},
			})
			if res.IsError {
				t.Fatalf("создать_файл bad: %s", res.Content)
			}
			return llm.ChatResponse{Text: "draft", Model: "fake"}, nil
		}
		res := exec(ctx, llm.ToolCall{
			ID:    "fix",
			Name:  "создать_файл",
			Input: map[string]any{"путь": "documents/заявка.yaml", "содержимое": "name: Заявка\nfields:\n  - name: Номер\n    type: string\n"},
		})
		if res.IsError {
			t.Fatalf("создать_файл fix: %s", res.Content)
		}
		return llm.ChatResponse{Text: "fixed", Model: "fake"}, nil
	}}

	out, err := runGenWithCorrections(context.Background(), runner, "system", "создай документ", tools, exec, g)
	if err != nil {
		t.Fatalf("runGenWithCorrections: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("ожидалось 2 вызова модели, получено %d", runner.calls)
	}
	if out.RepairRounds != 1 {
		t.Fatalf("ожидался 1 раунд исправления, получено %d", out.RepairRounds)
	}
	if !out.Check.OK {
		t.Fatalf("после исправления check должен быть OK:\n%s", out.CheckText)
	}
	changes := g.diff()
	if len(changes) != 1 || !strings.Contains(changes[0].NewContent, "name: Заявка") {
		t.Fatalf("исправленный diff неверный: %+v", changes)
	}
}

func TestGenSelfCorrectionReturnsCheckOnRunnerError(t *testing.T) {
	g := newTestGenSession(t)
	tools, exec := g.tools()
	runner := &fakeGenRunner{run: func(call int, ctx context.Context, task string, req llm.ChatRequest, tools []llm.Tool, exec llm.ToolExecutor) (llm.ChatResponse, error) {
		res := exec(ctx, llm.ToolCall{
			ID:    "bad",
			Name:  "создать_файл",
			Input: map[string]any{"путь": "documents/заявка.yaml", "содержимое": "fields:\n  - name: Номер\n    type: string\n"},
		})
		if res.IsError {
			t.Fatalf("создать_файл bad: %s", res.Content)
		}
		return llm.ChatResponse{Text: "partial", Model: "fake"}, context.Canceled
	}}

	out, err := runGenWithCorrections(context.Background(), runner, "system", "создай документ", tools, exec, g)
	if err == nil {
		t.Fatal("ожидалась ошибка модели")
	}
	if out.Check.OK || !strings.Contains(out.CheckText, "Найдены ошибки") {
		t.Fatalf("ожидался красный итоговый check, got ok=%v text:\n%s", out.Check.OK, out.CheckText)
	}
}

func TestGenCreateFile_WritesWhitelistedOS(t *testing.T) {
	g := newTestGenSession(t)
	content := "Процедура Выполнить()\nКонецПроцедуры\n"
	if err := g.createFile("src/тест.proc.os", content); err != nil {
		t.Fatalf("createFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.overlay, "src", "тест.proc.os"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content mismatch: %q", got)
	}
	if err := g.createFile("../evil.os", content); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if err := g.createFile("src/evil.txt", content); err == nil {
		t.Fatal("expected disallowed extension to be rejected")
	}
}

func TestGenerateSystemPrompt_HasMetadataFormat(t *testing.T) {
	for _, want := range []string{"tableparts", "reference:", "type: number", "posting: true", "создать_файл", "full=true", "форматировать", "прогнать_запрос"} {
		if !strings.Contains(aiGenerateSystem, want) {
			t.Errorf("системный промпт генератора не содержит %q", want)
		}
	}
}

func TestGenGoldenScenario_DocumentRegisterPosting(t *testing.T) {
	g := newTestGenSession(t)
	_, exec := g.tools()

	mustGenTool(t, exec, "создать_объект", map[string]any{
		"тип": "справочник",
		"имя": "Товар",
		"yaml": `name: Товар
fields:
  - name: Наименование
    type: string
  - name: Цена
    type: number
`,
	})
	mustGenTool(t, exec, "создать_объект", map[string]any{
		"тип": "документ",
		"имя": "ЗаказКлиента",
		"yaml": `name: ЗаказКлиента
posting: true
fields:
  - name: Номер
    type: string
  - name: Дата
    type: date
tableparts:
  - name: Товары
    fields:
      - name: Товар
        type: reference:Товар
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
`,
	})
	mustGenTool(t, exec, "создать_объект", map[string]any{
		"тип": "регистр накопления",
		"имя": "Продажи",
		"yaml": `name: Продажи
dimensions:
  - name: Товар
    type: reference:Товар
resources:
  - name: Количество
    type: number
  - name: Сумма
    type: number
`,
	})
	mustGenTool(t, exec, "создать_файл", map[string]any{
		"путь": "src/заказклиента.posting.os",
		"содержимое": `Процедура ОбработкаПроведения()
  Движения.Продажи.Очистить();
  Для Каждого Строка Из this.Товары Цикл
    Дв = Движения.Продажи.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Товар = Строка.Товар;
    Дв.Количество = Строка.Количество;
    Дв.Сумма = Строка.Количество * Строка.Цена;
  КонецЦикла;
КонецПроцедуры
`,
	})
	mustGenTool(t, exec, "форматировать", map[string]any{})
	check := mustGenTool(t, exec, "проверить_конфигурацию", map[string]any{"full": true})
	if !strings.Contains(check.Content, "Нет ошибок") {
		t.Fatalf("golden document/register scenario is not clean:\n%s", check.Content)
	}

	assertGenDiffPaths(t, g.diff(), []string{
		"catalogs/товар.yaml",
		"documents/заказклиента.yaml",
		"registers/продажи.yaml",
		"src/заказклиента.posting.os",
	})
	assertGenTraceHas(t, g.trace, "создать_объект", "создать_файл", "форматировать", "проверить_конфигурацию")
}

func TestGenGoldenScenario_ReportWidgetQuery(t *testing.T) {
	g := newTestGenSession(t)
	_, exec := g.tools()
	query := `ВЫБРАТЬ
  Наименование,
  Цена
ИЗ Справочник.Номенклатура`

	mustGenTool(t, exec, "создать_объект", map[string]any{
		"тип": "справочник",
		"имя": "Номенклатура",
		"yaml": `name: Номенклатура
fields:
  - name: Наименование
    type: string
  - name: Цена
    type: number
`,
	})
	mustGenTool(t, exec, "создать_файл", map[string]any{
		"путь": "reports/цены_номенклатуры.yaml",
		"содержимое": `name: ЦеныНоменклатуры
title: Цены номенклатуры
query: |
  ` + strings.ReplaceAll(query, "\n", "\n  ") + `
`,
	})
	mustGenTool(t, exec, "создать_файл", map[string]any{
		"путь": "widgets/цены_номенклатуры.yaml",
		"содержимое": `name: ЦеныНоменклатуры
type: list
title: Цены номенклатуры
limit: 5
columns:
  - field: Наименование
    label: Номенклатура
  - field: Цена
    label: Цена
    format: money
query: |
  ` + strings.ReplaceAll(query, "\n", "\n  ") + `
`,
	})
	run := mustGenTool(t, exec, "прогнать_запрос", map[string]any{"запрос": query, "лимит": 3})
	if strings.Contains(run.Content, "ошибка") || !strings.Contains(run.Content, `"columns"`) {
		t.Fatalf("query tool returned unexpected output:\n%s", run.Content)
	}
	mustGenTool(t, exec, "форматировать", map[string]any{})
	check := mustGenTool(t, exec, "проверить_конфигурацию", map[string]any{"full": true})
	if !strings.Contains(check.Content, "Нет ошибок") {
		t.Fatalf("golden report/widget scenario is not clean:\n%s", check.Content)
	}

	assertGenDiffPaths(t, g.diff(), []string{
		"catalogs/номенклатура.yaml",
		"reports/цены_номенклатуры.yaml",
		"widgets/цены_номенклатуры.yaml",
	})
	assertGenTraceHas(t, g.trace, "прогнать_запрос", "проверить_конфигурацию")
}

func TestGenGoldenScenario_ManagedFormWithEvent(t *testing.T) {
	g := newTestGenSession(t)
	_, exec := g.tools()

	mustGenTool(t, exec, "создать_объект", map[string]any{
		"тип": "справочник",
		"имя": "Контрагент",
		"yaml": `name: Контрагент
fields:
  - name: Наименование
    type: string
  - name: ИНН
    type: string
`,
	})
	mustGenTool(t, exec, "создать_файл", map[string]any{
		"путь": "forms/контрагент/объекта.form.yaml",
		"содержимое": `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
  title:
    ru: Карточка контрагента
events:
  ПриОткрытии: ПриОткрытии
elements:
  - kind: ГруппаФормы
    name: Реквизиты
    children:
      - kind: ПолеВвода
        name: ПолеНаименование
        data_path: Объект.Наименование
        required: true
      - kind: ПолеВвода
        name: ПолеИНН
        data_path: Объект.ИНН
`,
	})
	mustGenTool(t, exec, "создать_файл", map[string]any{
		"путь": "forms/контрагент/объекта.form.os",
		"содержимое": `Процедура ПриОткрытии()
КонецПроцедуры
`,
	})
	mustGenTool(t, exec, "форматировать", map[string]any{})
	check := mustGenTool(t, exec, "проверить_конфигурацию", map[string]any{"full": true})
	if !strings.Contains(check.Content, "Нет ошибок") {
		t.Fatalf("golden managed form scenario is not clean:\n%s", check.Content)
	}

	proj, err := project.Load(g.overlay)
	if err != nil {
		t.Fatalf("project.Load overlay: %v", err)
	}
	defer proj.Close()
	var found bool
	for _, e := range proj.Entities {
		if e.Name != "Контрагент" {
			continue
		}
		for _, f := range e.Forms {
			if f.Name == "ФормаОбъекта" && f.IsManaged() && f.Procedures["ПриОткрытии"] != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("managed form with attached procedure was not loaded")
	}
	assertGenDiffPaths(t, g.diff(), []string{
		"catalogs/контрагент.yaml",
		"forms/контрагент/объекта.form.os",
		"forms/контрагент/объекта.form.yaml",
	})
	assertGenTraceHas(t, g.trace, "создать_файл", "проверить_конфигурацию")
}

func mustGenTool(t *testing.T, exec llm.ToolExecutor, name string, input map[string]any) llm.ToolResult {
	t.Helper()
	res := exec(context.Background(), llm.ToolCall{ID: name, Name: name, Input: input})
	if res.IsError {
		t.Fatalf("%s returned error: %s", name, res.Content)
	}
	return res
}

func assertGenDiffPaths(t *testing.T, changes []GenChange, want []string) {
	t.Helper()
	got := make([]string, 0, len(changes))
	for _, ch := range changes {
		got = append(got, ch.Path)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff paths mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func assertGenTraceHas(t *testing.T, trace []GenToolTrace, names ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, tr := range trace {
		if tr.IsError {
			t.Fatalf("unexpected error in tool trace %s: %s", tr.Name, tr.Result)
		}
		seen[tr.Name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("tool trace does not contain %q: %+v", name, trace)
		}
	}
}
