package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/aicontract"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/project"
)

// builtinReference — отсортированный список известных функций DSL для контекста
// модели (чтобы она не выдумывала несуществующие). Считается один раз.
var builtinReference = func() string {
	names := make([]string, 0, 256)
	for n := range interpreter.KnownBuiltinNames() {
		if strings.HasPrefix(n, "__") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}()

var aiAssistSystem = "Ты — помощник разработчика конфигураций OneBase (платформа учёта, похожая на 1С). " +
	"Помогаешь писать и объяснять код на встроенном русскоязычном языке (DSL, файлы .os) и метаданные в YAML. " +
	"Синтаксис DSL близок к 1С: Процедура/КонецПроцедуры, Если/Тогда/КонецЕсли, Для Каждого/Цикл, объект Запрос, " +
	"обработчики событий форм (ПриОткрытии, ПриИзменении, ПередЗаписью и т.п.). " +
	"Если просят написать код — верни только код без лишних пояснений (если явно не просят объяснить). " +
	"Используй только функции из списка известных встроенных функций. Известные функции: " + builtinReference

// projectSchemaText строит текстовый срез конфигурации из загруженного проекта.
func projectSchemaText(proj *project.Project) string {
	return aicontract.ProjectSchemaText(proj)
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

// cfgAIEnabled сообщает конфигуратору, доступен ли помощник (настроен в базе).
func (h *handler) cfgAIEnabled(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	cfg, err := db.GetLLMConfig(r.Context())
	writeJSON(w, 200, map[string]any{"enabled": err == nil && cfg.Enabled && len(cfg.Models) > 0})
}

// cfgAIAssist — генерация/объяснение кода в конфигураторе. Принимает инструкцию и
// (необязательно) текущий фрагмент кода, возвращает ответ модели по профилю
// «конфигуратор».
func (h *handler) cfgAIAssist(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
		Code   string `json:"code"`
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

	prompt := req.Prompt
	if strings.TrimSpace(req.Code) != "" {
		prompt += "\n\nТекущий код:\n" + req.Code
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	system := aiAssistSystem
	if schema := h.configSchemaText(ctx, b); schema != "" {
		system += "\n\nТекущая конфигурация базы (объекты, поля, ТЧ, формы):\n" + schema
	}
	runner := llm.New(cfg, nil)
	resp, err := runner.Run(ctx, "конфигуратор", llm.ChatRequest{
		System:   system,
		Messages: []llm.Message{llm.UserText(prompt)},
	})
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": llm.SafeErr(err)})
		return
	}
	logCfgAI(r.Context(), db, cfg, cfgLogin(r.Context()), "конфигуратор-помощник", req.Prompt, resp.Text, resp)
	writeJSON(w, 200, map[string]any{"ok": true, "text": resp.Text, "model": resp.Model})
}
