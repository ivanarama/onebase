package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/project"
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
	// «:» блокируем тоже: на Windows-NTFS «имя:поток» пишет в alternate data
	// stream, а не в обычный .yaml — неожиданное поведение при записи в конфиг.
	if n == "." || strings.ContainsAny(n, "/\\:\x00") || strings.Contains(n, "..") {
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
		return "Нет ошибок."
	}
	var b strings.Builder
	b.WriteString("Найдены ошибки:\n")
	for _, is := range issues {
		// Capitalize object name for readability (e.g. "заявка" → "Заявка").
		obj := is.Object
		if r, size := utf8.DecodeRuneInString(obj); size > 0 {
			obj = strings.ToUpper(string(r)) + obj[size:]
		}
		if is.File != "" {
			fmt.Fprintf(&b, "- %s %s (%s): %s\n", is.Kind, obj, is.File, is.Message)
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

	// Срез конфигурации в промпт строим из уже материализованного dir (без
	// повторного экспорта, который сделал бы h.configSchemaText).
	system := aiGenerateSystem
	if proj, perr := project.Load(dir); perr == nil {
		if schema := projectSchemaText(proj); schema != "" {
			system += "\n\nТекущая конфигурация базы:\n" + schema
		}
		proj.Close()
	}

	tools, exec := g.tools()
	runner := llm.New(cfg, nil)
	resp, err := runner.RunWithTools(ctx, "конфигуратор", llm.ChatRequest{
		System:   system,
		Messages: []llm.Message{llm.UserText(req.Prompt)},
	}, tools, exec)
	if err != nil {
		// Отдаём уже созданные черновики даже при ошибке/исчерпании раундов —
		// иначе частичная работа модели теряется (по финальному ревью).
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err), "changes": g.diff()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model, "changes": g.diff()})
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
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		// Возвращаем ошибку Close — она ловит сбой сброса буфера (напр. диск
		// заполнен), иначе усечённая копия молча сошла бы за успех.
		return out.Close()
	})
}
