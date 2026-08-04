package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/aicontext"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// aiQueryRowLimit — потолок строк, отдаваемых модели одним инструментом (контроль
// размера контекста и стоимости).
const aiQueryRowLimit = 100

// aiDataAllowed решает, доступны ли запросу read-only инструменты данных ИИ-чата.
// Доступ имеют администраторы (как у консоли запросов), а в режимах rbac/all —
// пользователи с персональным флагом AIDataAccess или ролью с ai_data_access.
// В режиме all это даёт доступ к произвольным запросам на чтение в обход
// объектного RBAC — выдавать его осознанно.
func (s *Server) aiDataAllowed(r *http.Request) bool {
	if s.isAdmin(r) {
		return true
	}
	// Режим доступа ИИ к данным (план 54). В дефолтном admin_only флаг
	// AIDataAccess/роль не дают доступа — данные только администраторам. В
	// режимах rbac (с фильтрацией источников) и all этот opt-in работает.
	if s.store.GetAIDataScope(r.Context()) == storage.AIDataScopeAdminOnly {
		return false
	}
	u := auth.UserFromContext(r.Context())
	return u.AllowsAIDataAccess()
}

// aiTools формирует набор инструментов для tool-use чата и исполнитель:
// read-only доступ к данным плюс отложенные действия (создание черновиков,
// открытие форм — план 51, слой 2, фаза 2). Инструменты выдаются администратору
// и пользователям с флагом AIDataAccess (см. aiDataAllowed). Для остальных
// возвращается (nil, nil, nil) — чат отвечает без доступа к данным. Третье
// значение — аккумулятор действий, ожидающих подтверждения пользователем;
// aiChat отдаёт их клиенту вместе с ответом модели.
func (s *Server) aiTools(r *http.Request) ([]llm.Tool, llm.ToolExecutor, *aiPendingActions) {
	if !s.aiDataAllowed(r) {
		return nil, nil, nil
	}
	tools := []llm.Tool{
		{
			Name:        "описание_данных",
			Description: "Вернуть карту конфигурации: справочники, документы, регистры (накопления, сведений, бухгалтерии), планы счетов, перечисления и константы с их полями, а также готовые отчёты, обработки, журналы и подсистемы. Вызови первым, чтобы понять, что есть в системе и что можно запросить.",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name: "выполнить_запрос",
			Description: "Выполнить запрос на языке запросов OneBase (1С-подобный, только ВЫБРАТЬ) и получить строки результата. " +
				"Для остатков и оборотов используй виртуальные таблицы регистров: РегистрНакопления.Имя.Остатки(&НаДату) и .Обороты(&Нач,&Кон). " +
				"Параметры в тексте пишутся как &Имя и передаются в поле параметры.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"запрос":    map[string]any{"type": "string", "description": "текст запроса (ВЫБРАТЬ ...)"},
					"параметры": map[string]any{"type": "object", "description": "значения параметров &Имя, напр. {\"НаДату\":\"2026-06-01\"}"},
				},
				"required": []any{"запрос"},
			},
		},
	}

	tools = append(tools, aiMutationTools()...)

	pending := &aiPendingActions{}
	exec := func(ctx context.Context, call llm.ToolCall) llm.ToolResult {
		switch call.Name {
		case "описание_данных":
			return llm.ToolResult{ID: call.ID, Content: s.aiSchemaText(ctx)}
		case "выполнить_запрос":
			return s.aiRunQuery(ctx, call)
		case "создать_документ":
			return s.aiStageCreate(ctx, metadata.KindDocument, call, pending)
		case "создать_элемент_справочника":
			return s.aiStageCreate(ctx, metadata.KindCatalog, call, pending)
		case "открыть_форму":
			return s.aiStageOpen(ctx, call, pending)
		default:
			return llm.ToolResult{ID: call.ID, Content: "неизвестный инструмент: " + call.Name, IsError: true}
		}
	}
	return tools, exec, pending
}

// aiSchemaText кратко описывает доступные объекты конфигурации для модели. В
// режиме rbac (план 54) у не-администратора из схемы исключаются объекты-данные
// (справочники/документы/регистры/инфо-регистры/регбухи) без права read —
// согласованно с фильтрацией источников в выполнить_запрос.
func (s *Server) aiSchemaText(ctx context.Context) string {
	filter := s.store != nil && s.store.GetAIDataScope(ctx) == storage.AIDataScopeRBAC
	allow := func(kind, name string) bool { return !filter || s.canCtx(ctx, kind, name, "read") }

	ents := make([]*metadata.Entity, 0)
	for _, e := range s.reg.Entities() {
		if allow(string(e.Kind), e.Name) {
			ents = append(ents, e)
		}
	}
	regs := make([]*metadata.Register, 0)
	for _, rg := range s.reg.Registers() {
		if allow("register", rg.Name) {
			regs = append(regs, rg)
		}
	}
	iregs := make([]*metadata.InfoRegister, 0)
	for _, ir := range s.reg.InfoRegisters() {
		if allow("inforeg", ir.Name) {
			iregs = append(iregs, ir)
		}
	}
	aregs := make([]*metadata.AccountRegister, 0)
	for _, ar := range s.reg.AccountRegisters() {
		if allow("register", ar.Name) {
			aregs = append(aregs, ar)
		}
	}

	reports := make([]aicontext.NamedTitle, 0)
	for _, rp := range s.reg.Reports() {
		reports = append(reports, aicontext.NamedTitle{Name: rp.Name, Title: rp.Title})
	}
	procs := make([]aicontext.NamedTitle, 0)
	for _, p := range s.reg.Processors() {
		procs = append(procs, aicontext.NamedTitle{Name: p.Name, Title: p.Title})
	}
	return aicontext.SchemaText(aicontext.Input{
		Entities:         ents,
		Registers:        regs,
		InfoRegisters:    iregs,
		AccountRegisters: aregs,
		ChartsOfAccounts: s.reg.ChartsOfAccounts(),
		Enums:            s.reg.Enums(),
		Constants:        s.reg.Constants(),
		Reports:          reports,
		Processors:       procs,
		Journals:         s.reg.Journals(),
		Subsystems:       s.reg.Subsystems(),
	})
}

// aiDeniedSource возвращает имя первого объекта-источника, на чтение которого у
// пользователя нет прав (для режима rbac). Пусто — все источники разрешены.
// Источники неизвестного типа (Kind=="") разрешены только админу и в открытом
// деплое: canCtx с пустым kind → User.Has → false для не-админа.
func (s *Server) aiDeniedSource(ctx context.Context, sources []query.SourceRef) string {
	for _, src := range sources {
		if !s.canCtx(ctx, src.Kind, src.Name, "read") {
			return src.Name
		}
	}
	return ""
}

// aiRunQuery компилирует и выполняет запрос инструмента, возвращая строки в JSON.
func (s *Server) aiRunQuery(ctx context.Context, call llm.ToolCall) llm.ToolResult {
	qtext, _ := call.Input["запрос"].(string)
	qtext = stripQueryQuotes(strings.TrimSpace(qtext))
	if qtext == "" {
		return llm.ToolResult{ID: call.ID, Content: "пустой запрос", IsError: true}
	}
	var params map[string]any
	if p, ok := call.Input["параметры"].(map[string]any); ok {
		params = p
		coerceParams(params)
	}
	res, err := s.compileQueryWithRowAccess(ctx, qtext, params)
	if err != nil {
		return llm.ToolResult{ID: call.ID, Content: "ошибка компиляции запроса: " + err.Error(), IsError: true}
	}
	// Объектный RBAC (план 54): в режиме rbac до выполнения проверяем право чтения
	// на каждый объект-источник. Запрещённый объект → отказ модели (QueryAll не
	// выполняется), она переформулирует ответ.
	if s.store.GetAIDataScope(ctx) == storage.AIDataScopeRBAC {
		if denied := s.aiDeniedSource(ctx, res.Sources); denied != "" {
			return llm.ToolResult{ID: call.ID, Content: "нет доступа к объекту: " + denied, IsError: true}
		}
	}
	// План 88E: колонка с защищённым полем уходит в LLM замаскированной, а отбор
	// или агрегат по такому полю по-прежнему отклоняется — маска на выходе от
	// перебора условием не защищает.
	maskPlan := s.queryMaskPlan(ctx, res)
	if maskPlan.Denied != "" {
		return llm.ToolResult{ID: call.ID, Content: "нет доступа к защищённому полю: " + maskPlan.Denied, IsError: true}
	}
	rows, err := s.store.QueryAll(ctx, res.SQL, res.Args...)
	if err != nil {
		return llm.ToolResult{ID: call.ID, Content: "ошибка выполнения: " + err.Error(), IsError: true}
	}
	if err := maskPlan.Apply(rows); err != nil {
		return llm.ToolResult{ID: call.ID, Content: "нет доступа к защищённому полю: " + err.Error(), IsError: true}
	}
	truncated := false
	if len(rows) > aiQueryRowLimit {
		rows = rows[:aiQueryRowLimit]
		truncated = true
	}
	for _, row := range rows {
		for k, v := range row {
			if t, ok := v.(time.Time); ok {
				row[k] = t.Format("2006-01-02")
			}
		}
	}
	// Журнал ИИ (план 54): какой пользователь какой запрос выполнил через
	// ассистента и сколько строк ушло во внешний LLM.
	entry := storage.AIAuditEntry{Task: "чат-запрос", Query: qtext, Rows: len(rows)}
	if user := auth.UserFromContext(ctx); user != nil {
		entry.UserID, entry.UserLogin = user.ID, user.Login
	}
	s.store.LogAIQuery(ctx, entry)

	payload := map[string]any{"строк": len(rows), "данные": rows}
	if truncated {
		payload["усечено"] = true
		payload["примечание"] = fmt.Sprintf("показаны первые %d строк", aiQueryRowLimit)
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return llm.ToolResult{ID: call.ID, Content: "ошибка сериализации результата", IsError: true}
	}
	return llm.ToolResult{ID: call.ID, Content: string(out)}
}
