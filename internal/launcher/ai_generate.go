package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/configfmt"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/project"
	querylang "github.com/ivantit66/onebase/internal/query"
)

// GenChange — один предложенный объект в diff генерации.
type GenChange struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // "новый" | "изменён"
	NewContent string `json:"newContent"`
	OldContent string `json:"oldContent,omitempty"`
}

type GenToolTrace struct {
	Name    string         `json:"name"`
	Input   map[string]any `json:"input,omitempty"`
	Result  string         `json:"result"`
	IsError bool           `json:"isError,omitempty"`
}

// genSession — staging-оверлей конфигурации + накопленные изменения одной генерации.
type genSession struct {
	srcDir  string
	overlay string
	changed map[string]bool // относительные пути (slash) созданных/изменённых файлов
	trace   []GenToolTrace
}

const (
	genRepairCheckLimit = 10000
	genMaxRepairRounds  = 2
)

type genLLMRunner interface {
	RunWithTools(ctx context.Context, task string, req llm.ChatRequest, tools []llm.Tool, exec llm.ToolExecutor) (llm.ChatResponse, error)
}

type genRunResult struct {
	Response     llm.ChatResponse
	Check        configcheck.Result
	CheckText    string
	RepairRounds int
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
	case "отчёт", "отчет", "report":
		return "reports", true
	case "виджет", "widget":
		return "widgets", true
	case "обработка", "processor":
		return "processors", true
	case "страница", "page":
		return "pages", true
	case "подсистема", "subsystem":
		return "subsystems", true
	case "роль", "role":
		return "roles", true
	case "http-сервис", "http service", "сервис", "service":
		return "services", true
	case "регламентное задание", "scheduled", "job":
		return "scheduled", true
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
		return fmt.Errorf("неизвестный тип объекта: %q", kind)
	}
	fname, err := safeFileName(name)
	if err != nil {
		return err
	}
	rel := subdir + "/" + fname
	full, err := safeGeneratedFullPath(g.overlay, rel)
	if err != nil {
		return err
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

// createFile записывает произвольный whitelist-файл в overlay. Используется
// генератором 2.0 для .os-модулей, форм, отчётов, виджетов, страниц и ролей.
func (g *genSession) createFile(rel, content string) error {
	full, err := safeGeneratedFullPath(g.overlay, rel)
	if err != nil {
		return err
	}
	if len(content) > 512*1024 {
		return fmt.Errorf("файл %s слишком большой для AI-generate (лимит 512 KiB)", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	g.changed[path.Clean(rel)] = true
	return nil
}

func (g *genSession) readFile(rel string) string {
	full, err := safeGeneratedFullPath(g.overlay, rel)
	if err != nil {
		return "ошибка: " + err.Error()
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "ошибка чтения: " + err.Error()
	}
	return string(data)
}

func (g *genSession) format(pathArg string) string {
	var rels []string
	if strings.TrimSpace(pathArg) != "" {
		rel := path.Clean(strings.TrimSpace(pathArg))
		full, err := safeGeneratedFullPath(g.overlay, rel)
		if err != nil {
			return "ошибка: " + err.Error()
		}
		if !configfmt.IsYAMLPath(rel) {
			return "ошибка: форматируются только YAML-файлы"
		}
		if _, err := os.Stat(full); err != nil {
			return "ошибка чтения: " + err.Error()
		}
		rels = []string{rel}
	} else {
		for rel := range g.changed {
			if configfmt.IsYAMLPath(rel) {
				rels = append(rels, rel)
			}
		}
		sort.Strings(rels)
	}
	if len(rels) == 0 {
		return "Нет изменённых YAML-файлов для форматирования."
	}
	var formatted []string
	for _, rel := range rels {
		full, err := safeGeneratedFullPath(g.overlay, rel)
		if err != nil {
			return "ошибка: " + err.Error()
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return "ошибка чтения " + rel + ": " + err.Error()
		}
		out, err := configfmt.FormatYAMLBytes(data)
		if err != nil {
			return "ошибка форматирования " + rel + ": " + err.Error()
		}
		if string(out) != string(data) {
			if err := os.WriteFile(full, out, 0o644); err != nil {
				return "ошибка записи " + rel + ": " + err.Error()
			}
			g.changed[rel] = true
			formatted = append(formatted, rel)
		}
	}
	if len(formatted) == 0 {
		return "YAML уже в каноническом формате."
	}
	return "Отформатировано: " + strings.Join(formatted, ", ")
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
	issues = configcheck.AnnotateIssues(issues)
	var b strings.Builder
	b.WriteString("Найдены ошибки:\n")
	for _, is := range issues {
		// Capitalize object name for readability (e.g. "заявка" → "Заявка").
		obj := is.Object
		if r, size := utf8.DecodeRuneInString(obj); size > 0 {
			obj = strings.ToUpper(string(r)) + obj[size:]
		}
		code := ""
		if is.Code != "" {
			code = " [" + is.Code + "]"
		}
		if is.File != "" {
			fmt.Fprintf(&b, "- %s%s %s (%s): %s\n", is.Kind, code, obj, is.File, is.Message)
		} else {
			fmt.Fprintf(&b, "-%s %s\n", code, is.Message)
		}
		if is.SuggestedFix != "" {
			fmt.Fprintf(&b, "  Подсказка: %s\n", is.SuggestedFix)
		}
	}
	return b.String()
}

func (g *genSession) checkFull() string {
	_, text := g.runFullCheck()
	return text
}

func (g *genSession) runFullCheck() (configcheck.Result, string) {
	res := configcheck.RunFull(g.overlay)
	return res, renderGenFullCheckText(res)
}

func renderGenFullCheckText(res configcheck.Result) string {
	if res.OK {
		if len(res.Warnings) > 0 {
			return fmt.Sprintf("Нет ошибок. Предупреждений: %d.", len(res.Warnings))
		}
		return "Нет ошибок."
	}
	var b strings.Builder
	b.WriteString("Найдены ошибки:\n")
	for _, is := range res.Issues {
		loc := is.File
		if is.Line > 0 {
			loc = fmt.Sprintf("%s:%d:%d", loc, is.Line, is.Column)
		}
		if loc == "" {
			loc = "(конфигурация)"
		}
		code := ""
		if is.Code != "" {
			code = " [" + is.Code + "]"
		}
		if is.Kind != "" {
			fmt.Fprintf(&b, "- %s [%s]%s %s\n", loc, is.Kind, code, is.Message)
		} else {
			fmt.Fprintf(&b, "- %s%s %s\n", loc, code, is.Message)
		}
		if is.SuggestedFix != "" {
			fmt.Fprintf(&b, "  Подсказка: %s\n", is.SuggestedFix)
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

func (g *genSession) listObjects() string {
	proj, err := project.Load(g.overlay)
	if err != nil {
		return "ошибка загрузки проекта: " + err.Error()
	}
	defer proj.Close()
	return projectSchemaText(proj)
}

func (g *genSession) impact(object, field, procedure string) string {
	needles := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			needles = append(needles, strings.ToLower(s))
		}
	}
	if object != "" && field != "" {
		add(object + "." + field)
	}
	add(object)
	add(field)
	add(procedure)
	if len(needles) == 0 {
		return "укажите объект, поле или процедуру"
	}
	type match struct {
		file string
		line int
		text string
	}
	var matches []match
	_ = filepath.WalkDir(g.overlay, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		lowPath := strings.ToLower(p)
		if !(strings.HasSuffix(lowPath, ".yaml") || strings.HasSuffix(lowPath, ".yml") || strings.HasSuffix(lowPath, ".os")) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(g.overlay, p)
		for i, line := range strings.Split(string(data), "\n") {
			low := strings.ToLower(line)
			for _, needle := range needles {
				if strings.Contains(low, needle) {
					matches = append(matches, match{file: filepath.ToSlash(rel), line: i + 1, text: strings.TrimSpace(line)})
					break
				}
			}
		}
		return nil
	})
	if len(matches) == 0 {
		return "Совпадений не найдено."
	}
	var b strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&b, "%s:%d %s\n", m.file, m.line, m.text)
	}
	return b.String()
}

func (g *genSession) runQuery(ctx context.Context, qtext string, params map[string]any, limit int) string {
	qtext = strings.TrimSpace(qtext)
	if qtext == "" {
		return "ошибка: пустой запрос"
	}
	if !isReadonlyOneBaseQuery(qtext) {
		return "ошибка: разрешены только read-only запросы ВЫБРАТЬ/SELECT"
	}
	proj, err := project.Load(g.overlay)
	if err != nil {
		return "ошибка загрузки проекта: " + err.Error()
	}
	defer proj.Close()
	db, closeDB, err := configcheck.BuildSchemaDB(proj)
	if err != nil {
		return "ошибка построения тестовой схемы: " + err.Error()
	}
	defer closeDB()
	compiled, err := querylang.Compile(qtext, querylang.CompileOpts{
		Params:      params,
		Entities:    proj.Entities,
		Registers:   proj.Registers,
		InfoRegs:    proj.InfoRegisters,
		AccountRegs: proj.AccountRegisters,
		Dialect:     db.Dialect(),
	})
	if err != nil {
		return "ошибка компиляции запроса: " + err.Error()
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	sqlText := fmt.Sprintf("SELECT * FROM (%s) _onebase_gen_q LIMIT %d", compiled.SQL, limit)
	rows, cols, err := db.RunQuery(ctx, sqlText, compiled.Args)
	if err != nil {
		return "ошибка выполнения запроса: " + err.Error()
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	if cols == nil {
		cols = []string{}
	}
	out := map[string]any{
		"sql":     sqlText,
		"args":    compiled.Args,
		"columns": cols,
		"rows":    rows,
		"count":   len(rows),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Sprintf("SQL:\n%s\nrows: %d", sqlText, len(rows))
	}
	return string(data)
}

func isReadonlyOneBaseQuery(text string) bool {
	up := strings.ToUpper(strings.TrimLeftFunc(text, func(r rune) bool {
		return r == '\ufeff' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	}))
	return strings.HasPrefix(up, "ВЫБРАТЬ") || strings.HasPrefix(up, "SELECT")
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

func intInput(call llm.ToolCall, key string, def int) int {
	v, ok := call.Input[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return int(i)
		}
	}
	return def
}

func mapInput(call llm.ToolCall, key string) map[string]any {
	v, ok := call.Input[key]
	if !ok || v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
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
			Name:        "создать_файл",
			Description: "Создать или заменить whitelist-файл в staging. Разрешены YAML в каталогах метаданных/forms/roles/services/widgets/reports/pages/processors/subsystems/scheduled и .os в src или forms/<объект>/.",
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
			Description: "Прочитать whitelist-файл из staging по относительному пути.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"путь": map[string]any{"type": "string"}},
				"required":   []any{"путь"},
			},
		},
		{
			Name:        "форматировать",
			Description: "Отформатировать изменённые YAML-файлы в staging каноническим writer'ом. Можно передать путь к одному YAML-файлу.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"путь": map[string]any{"type": "string"}},
			},
		},
		{
			Name:        "список_объектов",
			Description: "Показать компактный список объектов текущего staging-проекта.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "проверить_конфигурацию",
			Description: "Проверить черновик конфигурации. full=true включает полный configcheck.RunFull, включая запросы.",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"full": map[string]any{"type": "boolean"}},
			},
		},
		{
			Name:        "прогнать_запрос",
			Description: "Скомпилировать и выполнить read-only запрос OneBase на тестовой SQLite-схеме staging. Нужен для проверки отчётов/виджетов; возвращает SQL и sample rows.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"запрос":    map[string]any{"type": "string"},
					"параметры": map[string]any{"type": "object"},
					"лимит":     map[string]any{"type": "number"},
				},
				"required": []any{"запрос"},
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
			Name:        "объяснить_impact",
			Description: "Показать, где в staging встречается объект, поле или процедура.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"объект":    map[string]any{"type": "string"},
					"поле":      map[string]any{"type": "string"},
					"процедура": map[string]any{"type": "string"},
				},
			},
		},
	}
	exec := func(ctx context.Context, call llm.ToolCall) llm.ToolResult {
		record := func(res llm.ToolResult) llm.ToolResult {
			g.trace = append(g.trace, GenToolTrace{
				Name:    call.Name,
				Input:   call.Input,
				Result:  truncateText(res.Content, 600),
				IsError: res.IsError,
			})
			return res
		}
		switch call.Name {
		case "создать_объект":
			if err := g.createObject(strInput(call, "тип"), strInput(call, "имя"), strInput(call, "yaml")); err != nil {
				return record(llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true})
			}
			return record(llm.ToolResult{ID: call.ID, Content: "создан объект " + strInput(call, "имя")})
		case "создать_файл":
			if err := g.createFile(strInput(call, "путь"), strInput(call, "содержимое")); err != nil {
				return record(llm.ToolResult{ID: call.ID, Content: "ошибка: " + err.Error(), IsError: true})
			}
			return record(llm.ToolResult{ID: call.ID, Content: "записан файл " + strInput(call, "путь")})
		case "прочитать_файл":
			return record(llm.ToolResult{ID: call.ID, Content: g.readFile(strInput(call, "путь"))})
		case "форматировать":
			return record(llm.ToolResult{ID: call.ID, Content: g.format(strInput(call, "путь"))})
		case "список_объектов":
			return record(llm.ToolResult{ID: call.ID, Content: g.listObjects()})
		case "проверить_конфигурацию":
			if v, ok := call.Input["full"].(bool); ok && v {
				return record(llm.ToolResult{ID: call.ID, Content: g.checkFull()})
			}
			return record(llm.ToolResult{ID: call.ID, Content: g.check()})
		case "прогнать_запрос":
			return record(llm.ToolResult{ID: call.ID, Content: g.runQuery(ctx, strInput(call, "запрос"), mapInput(call, "параметры"), intInput(call, "лимит", 20))})
		case "показать_объект":
			return record(llm.ToolResult{ID: call.ID, Content: g.showObject(strInput(call, "имя"))})
		case "объяснить_impact":
			return record(llm.ToolResult{ID: call.ID, Content: g.impact(strInput(call, "объект"), strInput(call, "поле"), strInput(call, "процедура"))})
		default:
			return record(llm.ToolResult{ID: call.ID, Content: "неизвестный инструмент: " + call.Name, IsError: true})
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
	"По описанию задачи на русском создавай рабочий feature slice: YAML-метаданные через «создать_объект» " +
	"или «создать_файл», при необходимости .os-модули, формы, отчёты, виджеты, страницы, роли и сервисы. " +
	"После создания набора файлов обязательно вызывай «проверить_конфигурацию» с full=true и исправляй ошибки. " +
	"Перед финальным ответом вызывай «форматировать»; для отчётов и виджетов проверяй запросы через «прогнать_запрос». " +
	"Используй существующие объекты (через «список_объектов», «показать_объект», «прочитать_файл») вместо дублирования. " +
	"Имена и типы полей бери реальные; не выдумывай несуществующие типы. Известные функции: " + builtinReference +
	"\n\n" + metadataFormatGuide

func runGenWithCorrections(ctx context.Context, runner genLLMRunner, system, prompt string, tools []llm.Tool, exec llm.ToolExecutor, g *genSession) (genRunResult, error) {
	var out genRunResult
	resp, err := runner.RunWithTools(ctx, "конфигуратор", llm.ChatRequest{
		System:   system,
		Messages: []llm.Message{llm.UserText(prompt)},
	}, tools, exec)
	out.Response = resp
	out.Check, out.CheckText = g.runFullCheck()
	if err != nil {
		return out, err
	}
	for out.RepairRounds < genMaxRepairRounds && !out.Check.OK && len(g.diff()) > 0 {
		out.RepairRounds++
		resp, err = runner.RunWithTools(ctx, "конфигуратор", llm.ChatRequest{
			System:   system,
			Messages: []llm.Message{llm.UserText(genRepairPrompt(prompt, out.CheckText, g.diff(), out.RepairRounds))},
		}, tools, exec)
		if resp.Text != "" || resp.Model != "" {
			out.Response = resp
		}
		out.Check, out.CheckText = g.runFullCheck()
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func genRepairPrompt(original, checkText string, changes []GenChange, round int) string {
	var files []string
	for _, ch := range changes {
		files = append(files, ch.Path)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Раунд исправления %d.\n\n", round)
	b.WriteString("Исходное ТЗ:\n")
	b.WriteString(strings.TrimSpace(original))
	b.WriteString("\n\nПолная проверка staging нашла ошибки:\n")
	b.WriteString(truncateText(checkText, genRepairCheckLimit))
	if len(files) > 0 {
		b.WriteString("\n\nФайлы, которые уже изменены в staging:\n")
		for _, f := range files {
			b.WriteString("- " + f + "\n")
		}
	}
	b.WriteString("\nИсправь существующий staging через инструменты. Не создавай дубли объектов; перед изменением существующего файла прочитай его. После исправлений вызови «проверить_конфигурацию» с full=true.")
	return b.String()
}

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
	result, err := runGenWithCorrections(ctx, runner, system, req.Prompt, tools, exec, g)
	if err != nil {
		// Отдаём уже созданные черновики даже при ошибке/исчерпании раундов —
		// иначе частичная работа модели теряется (по финальному ревью).
		writeJSON(w, 200, map[string]any{
			"error":        llm.SafeErr(err),
			"changes":      g.diff(),
			"toolTrace":    g.trace,
			"check":        result.Check,
			"checkText":    result.CheckText,
			"repairRounds": result.RepairRounds,
		})
		return
	}
	changes := g.diff()
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-генерация", req.Prompt, genResponseSummary(result.Response.Text, changes, g.trace), result.Response)
	writeJSON(w, 200, map[string]any{
		"ok":           true,
		"text":         result.Response.Text,
		"model":        result.Response.Model,
		"changes":      changes,
		"toolTrace":    g.trace,
		"check":        result.Check,
		"checkText":    result.CheckText,
		"repairRounds": result.RepairRounds,
	})
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
