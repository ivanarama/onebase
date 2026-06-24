package launcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/llm"
)

const validCatalogYAML = "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n"

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

func TestGenCreateFile_WritesAllowedFiles(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createFile("reports/Продажи.yaml", "query: \"ВЫБРАТЬ 1 КАК Сумма\"\nname: Продажи\n"); err != nil {
		t.Fatalf("createFile report: %v", err)
	}
	if err := g.createFile("src/Продажи.rep.os", "Процедура Сформировать() Экспорт\nКонецПроцедуры\n"); err != nil {
		t.Fatalf("createFile os: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.overlay, "reports", "Продажи.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "name: Продажи\nquery: ВЫБРАТЬ 1 КАК Сумма\n" {
		t.Fatalf("report YAML не отформатирован:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(g.overlay, "src", "Продажи.rep.os")); err != nil {
		t.Fatalf("os-файл не создан: %v", err)
	}
}

func TestGenCreateFile_RejectsUnsafePath(t *testing.T) {
	g := newTestGenSession(t)
	for _, bad := range []string{"../evil.yaml", "secrets/x.yaml", "src/x.yaml", "forms/a.txt"} {
		if err := g.createFile(bad, "x"); err == nil {
			t.Fatalf("ожидалась ошибка для %q", bad)
		}
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

func TestGenCheck_ReportsInvalidMetadata(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createFile("documents/заявка.yaml", "fields:\n  - name: Номер\n    type: string\n"); err != nil {
		t.Fatalf("createFile: %v", err)
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

func TestGenReadAndListFiles(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createFile("widgets/Выручка.yaml", "name: Выручка\ntype: kpi\n"); err != nil {
		t.Fatal(err)
	}
	content, err := g.readFile("widgets/Выручка.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "name: Выручка") {
		t.Fatalf("readFile вернул неверный контент: %q", content)
	}
	if list := g.listFiles(); !strings.Contains(list, "widgets/Выручка.yaml") {
		t.Fatalf("listFiles не содержит виджет:\n%s", list)
	}
}

func TestGenCheckFullReportsQueryErrors(t *testing.T) {
	g := newTestGenSession(t)
	if err := g.createFile("reports/Плохой.yaml", "name: Плохой\nquery: \"ВЫБРАТЬ * ИЗ Неизвестный.Источник\"\n"); err != nil {
		t.Fatal(err)
	}
	out := g.checkFull()
	if !strings.Contains(out, "Найдены ошибки") {
		t.Fatalf("full check должен вернуть ошибку запроса, got:\n%s", out)
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

func TestGenTools_Dispatch(t *testing.T) {
	g := newTestGenSession(t)
	tools, exec := g.tools()
	if len(tools) != 7 {
		t.Fatalf("ожидалось 7 инструментов, получено %d", len(tools))
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
	file := exec(context.Background(), llm.ToolCall{
		ID:    "3",
		Name:  "создать_файл",
		Input: map[string]any{"путь": "src/Клиент.manager.os", "содержимое": "Процедура Тест() Экспорт\nКонецПроцедуры\n"},
	})
	if file.IsError {
		t.Fatalf("создать_файл вернул ошибку: %s", file.Content)
	}
	read := exec(context.Background(), llm.ToolCall{
		ID:    "4",
		Name:  "прочитать_файл",
		Input: map[string]any{"путь": "src/Клиент.manager.os"},
	})
	if read.IsError || !strings.Contains(read.Content, "Процедура Тест") {
		t.Fatalf("прочитать_файл вернул неверно: %+v", read)
	}
	if len(g.trace) != 4 || g.trace[0].Tool != "создать_объект" || g.trace[3].Tool != "прочитать_файл" {
		t.Fatalf("trace не записал вызовы инструментов: %+v", g.trace)
	}
}

func TestGenerateSystemPrompt_HasMetadataFormat(t *testing.T) {
	for _, want := range []string{"tableparts", "reference:", "type: number", "posting: true", "создать_файл", "src/*.os", "полная=true"} {
		if !strings.Contains(aiGenerateSystem, want) {
			t.Errorf("системный промпт генератора не содержит %q", want)
		}
	}
}
