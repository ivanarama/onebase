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

type GenTraceEntry struct {
	Tool   string         `json:"tool"`
	Input  map[string]any `json:"input,omitempty"`
	Result string         `json:"result"`
	Error  bool           `json:"error,omitempty"`
}

// genSession — staging-оверлей конфигурации + накопленные изменения одной генерации.
type genSession struct {
	srcDir  string
	overlay string
	changed map[string]bool // относительные пути (slash) созданных/изменённых файлов
	trace   []GenTraceEntry
}

const (
	genReadFileLimit    = 40000
	genListFileLimit    = 300
	genTraceValueLimit  = 600
	genTraceResultLimit = 2000
)

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
	if n == "." || strings.ContainsAny(n, "/\\") || strings.Contains(n, "..") {
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
	return g.createFile(rel, yamlText)
}

func (g *genSession) createFile(rel, content string) error {
	rel = strings.TrimSpace(rel)
	if err := safeConfigPath(rel); err != nil {
		return err
	}
	full := filepath.Join(g.overlay, filepath.FromSlash(rel))
	cleanOverlay := filepath.Clean(g.overlay)
	if !strings.HasPrefix(filepath.Clean(full), cleanOverlay+string(os.PathSeparator)) {
		return fmt.Errorf("путь вне overlay: %q", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	data, err := formatConfigContent(rel, []byte(content))
	if err != nil {
		return err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return err
	}
	g.changed[rel] = true
	return nil
}

func (g *genSession) readFile(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if err := safeConfigPath(rel); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(g.overlay, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	text := string(data)
	runes := []rune(text)
	if len(runes) > genReadFileLimit {
		return string(runes[:genReadFileLimit]) + "\n... [файл обрезан]", nil
	}
	return text, nil
}

func (g *genSession) listFiles() string {
	var rels []string
	_ = filepath.WalkDir(g.overlay, func(full string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(g.overlay, full)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if safeConfigPath(rel) == nil {
			rels = append(rels, rel)
		}
		return nil
	})
	sort.Strings(rels)
	total := len(rels)
	if total > genListFileLimit {
		rels = append(rels[:genListFileLimit], fmt.Sprintf("... ещё %d файлов", total-genListFileLimit))
	}
	if len(rels) == 0 {
		return "Нет файлов в разрешённых путях."
	}
	return strings.Join(rels, "\n")
}

func (g *genSession) formatChanged() (int, error) {
	var changed int
	for rel := range g.changed {
		full := filepath.Join(g.overlay, filepath.FromSlash(rel))
		before, err := os.ReadFile(full)
		if err != nil {
			return changed, err
		}
		after, err := formatConfigContent(rel, before)
		if err != nil {
			return changed, err
		}
		if string(before) == string(after) {
			continue
		}
		if err := os.WriteFile(full, after, 0o644); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// check валидирует overlay без исполнения пользовательского кода. Возвращает
// человекочитаемый текст для модели.
func (g *genSession) check() string {
	issues, warnings := configcheck.CheckDir(g.overlay)
	if proj, err := project.Load(g.overlay); err == nil {
		proj.Close()
	} else if !configcheck.AlreadyReported(issues, err.Error()) {
		issues = append(issues, configcheck.Issue{Message: "Project.Load: " + err.Error()})
	}
	return renderGenCheckText(issues, warnings)
}

func (g *genSession) checkFull() string {
	res := configcheck.RunFull(g.overlay)
	return renderGenCheckText(res.Issues, res.Warnings)
}

func renderGenCheckText(issues, warnings []configcheck.Issue) string {
	if len(issues) == 0 && len(warnings) == 0 {
		return "Нет ошибок."
	}
	var b strings.Builder
	writeList := func(title string, list []configcheck.Issue) {
		if len(list) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(title)
		b.WriteByte('\n')
		for _, is := range list {
			writeGenIssue(&b, is)
		}
	}
	writeList("Найдены ошибки:", issues)
	writeList("Предупреждения:", warnings)
	return b.String()
}

func writeGenIssue(b *strings.Builder, is configcheck.Issue) {
	obj := is.Object
	if r, size := utf8.DecodeRuneInString(obj); size > 0 {
		obj = strings.ToUpper(string(r)) + obj[size:]
	}
	label := strings.TrimSpace(strings.TrimSpace(is.Kind + " " + obj))
	switch {
	case is.File != "" && label != "":
		fmt.Fprintf(b, "- %s (%s): %s\n", label, is.File, is.Message)
	case is.File != "":
		fmt.Fprintf(b, "- %s: %s\n", is.File, is.Message)
	case label != "":
		fmt.Fprintf(b, "- %s: %s\n", label, is.Message)
	default:
		fmt.Fprintf(b, "- %s\n", is.Message)
	}
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

func boolInput(call llm.ToolCall, key string) bool {
	v, ok := call.Input[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func (g *genSession) recordTool(call llm.ToolCall, res llm.ToolResult) {
	g.trace = append(g.trace, GenTraceEntry{
		Tool:   call.Name,
		Input:  summarizeToolInput(call.Input),
		Result: truncateRunes(res.Content, genTraceResultLimit),
		Error:  res.IsError,
	})
}

func summarizeToolInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		switch tv := v.(type) {
		case bool:
			out[k] = tv
		case float64:
			out[k] = tv
		case string:
			out[k] = truncateRunes(tv, genTraceValueLimit)
		default:
			out[k] = truncateRunes(fmt.Sprintf("%v", v), genTraceValueLimit)
		}
	}
	return out
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
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
			Description: "Проверить черновик конфигурации. полная=true включает полный check: cross-refs, query compile/prepare, pages/services/layout checks.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"полная": map[string]any{"type": "boolean"}},
			},
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
		{
			Name:        "создать_файл",
			Description: "Создать или изменить файл конфигурации в staging. Разрешены YAML-метаданные, forms/*.form.yaml, forms/*.form.os и src/*.os. Путь задавай slash-форматом, например reports/Продажи.yaml или src/Заказ.posting.os.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"путь":       map[string]any{"type": "string"},
					"содержимое": map[string]any{"type": "string"},
				},
				"required": []any{"путь", "содержимое"},
			},
		},
		{
			Name:        "прочитать_файл",
			Description: "Прочитать существующий файл конфигурации из staging по slash-пути.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"путь": map[string]any{"type": "string"}},
				"required":   []any{"путь"},
			},
		},
		{
			Name:        "список_файлов",
			Description: "Показать список доступных файлов конфигурации в разрешённых путях.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "форматировать",
			Description: "Отформатировать изменённые YAML-файлы в staging canonical formatter'ом.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	exec := func(_ context.Context, call llm.ToolCall) (res llm.ToolResult) {
		defer func() { g.recordTool(call, res) }()
		switch call.Name {
		case "создать_объект":
			if err := g.createObject(strInput(call, "тип"), strInput(call, "имя"), strInput(call, "yaml")); err != nil {
				return llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true}
			}
			return llm.ToolResult{ID: call.ID, Content: "создан объект " + strInput(call, "имя")}
		case "проверить_конфигурацию":
			if boolInput(call, "полная") {
				return llm.ToolResult{ID: call.ID, Content: g.checkFull()}
			}
			return llm.ToolResult{ID: call.ID, Content: g.check()}
		case "показать_объект":
			return llm.ToolResult{ID: call.ID, Content: g.showObject(strInput(call, "имя"))}
		case "создать_файл":
			if err := g.createFile(strInput(call, "путь"), strInput(call, "содержимое")); err != nil {
				return llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true}
			}
			return llm.ToolResult{ID: call.ID, Content: "записан файл " + strInput(call, "путь")}
		case "прочитать_файл":
			content, err := g.readFile(strInput(call, "путь"))
			if err != nil {
				return llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true}
			}
			return llm.ToolResult{ID: call.ID, Content: content}
		case "список_файлов":
			return llm.ToolResult{ID: call.ID, Content: g.listFiles()}
		case "форматировать":
			n, err := g.formatChanged()
			if err != nil {
				return llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true}
			}
			return llm.ToolResult{ID: call.ID, Content: fmt.Sprintf("отформатировано файлов: %d", n)}
		default:
			return llm.ToolResult{ID: call.ID, Content: "неизвестный инструмент: " + call.Name, IsError: true}
		}
	}
	return tools, exec
}

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

// aiGenerateSystem — роль генератора каркаса конфигурации.
var aiGenerateSystem = "Ты — генератор каркаса конфигурации OneBase (платформа учёта, похожая на 1С). " +
	"По описанию задачи на русском создавай рабочий вертикальный срез конфигурации. " +
	"Для простых метаданных можно использовать «создать_объект»; для отчётов, виджетов, форм, страниц, ролей, сервисов и DSL-модулей используй «создать_файл». " +
	"Разрешённые пути: catalogs/documents/registers/inforegs/enums/constants/accounts/accountregs/reports/processors/widgets/pages/services/subsystems/roles/scheduled/journals/*.yaml, " +
	"forms/*.form.yaml, forms/*/*.form.yaml, forms/*.form.os, forms/*/*.form.os, src/*.os, config/app.yaml, config/home_page.yaml. " +
	"Перед изменением существующего файла прочитай его через «прочитать_файл» или найди через «список_файлов». " +
	"После создания набора файлов обязательно вызывай «проверить_конфигурацию» с полная=true и исправляй ошибки. " +
	"Используй существующие объекты (через «показать_объект»/«прочитать_файл») вместо дублирования. " +
	"Имена и типы полей бери реальные; не выдумывай несуществующие типы. Известные функции: " + builtinReference +
	"\n\n" + metadataFormatGuide

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
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err), "changes": g.diff(), "trace": g.trace})
		return
	}
	changes := g.diff()
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-генерация", req.Prompt, genResponseSummary(resp.Text, changes), resp)
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model, "changes": changes, "trace": g.trace})
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
