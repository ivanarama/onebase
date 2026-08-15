package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
)

// aiActionsEntities — справочник + проводимый документ с ТЧ для тестов действий.
func aiActionsEntities() []*metadata.Entity {
	cat := &metadata.Entity{
		Name: "Контрагенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name:    "Заказ",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Контрагент", Type: metadata.FieldType("reference:Контрагенты"), RefEntity: "Контрагенты"},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	return []*metadata.Entity{cat, doc}
}

// stageCreateOrder прогоняет инструмент создать_документ и возвращает
// подготовленное действие.
func stageCreateOrder(t *testing.T, s *Server, pendingInput map[string]any) (aiAction, llm.ToolResult, *aiPendingActions) {
	t.Helper()
	r := httptest.NewRequest("POST", "/ui/ai/chat", nil)
	_, exec, pending := s.aiTools(r)
	if exec == nil || pending == nil {
		t.Fatal("ожидались инструменты и аккумулятор действий")
	}
	res := exec(context.Background(), llm.ToolCall{ID: "t1", Name: "создать_документ", Input: pendingInput})
	var action aiAction
	if len(pending.Actions) > 0 {
		action = pending.Actions[0]
	}
	return action, res, pending
}

func TestAIActions_StageCreateDocument(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	contraID := uuid.New()
	if err := s.store.Upsert(ctx, "Контрагенты", contraID, map[string]any{"Наименование": "Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}

	action, res, pending := stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля":     map[string]any{"Дата": "2026-07-17", "Контрагент": "Ромашка"},
		"табличные_части": map[string]any{
			"Товары": []any{map[string]any{"Наименование": "Стол", "Количество": float64(2)}},
		},
	})
	if res.IsError {
		t.Fatalf("инструмент вернул ошибку: %s", res.Content)
	}
	if len(pending.Actions) != 1 {
		t.Fatalf("ожидалось 1 действие, получено %d", len(pending.Actions))
	}
	if action.Type != "создать" || action.Entity != "Заказ" || action.Kind != "document" {
		t.Fatalf("неожиданное действие: %+v", action)
	}
	// Ссылка по имени разрешилась в UUID существующего контрагента.
	if got := fmt.Sprint(action.Fields["Контрагент"]); got != contraID.String() {
		t.Fatalf("контрагент не разрешён в UUID: %v", got)
	}
	if len(action.TPRows["Товары"]) != 1 {
		t.Fatalf("не перенесены строки ТЧ: %+v", action.TPRows)
	}
	if !strings.Contains(action.Label, "Заказ") || !strings.Contains(action.Label, "Ромашка") {
		t.Fatalf("подпись неинформативна: %q", action.Label)
	}
	// Ничего не записано до подтверждения.
	rows, err := s.store.QueryAll(ctx, "SELECT COUNT(*) AS c FROM заказ")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rows[0]["c"]) != "0" {
		t.Fatalf("документ создан без подтверждения: %+v", rows)
	}
}

func TestAIActions_StageCreate_Errors(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)

	// Неизвестная сущность.
	_, res, _ := stageCreateOrder(t, s, map[string]any{"сущность": "Продажа"})
	if !res.IsError || !strings.Contains(res.Content, "Заказ") {
		t.Fatalf("ожидалась ошибка со списком документов: %+v", res)
	}

	// Неизвестное поле.
	_, res, _ = stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля":     map[string]any{"Покупатель": "Ромашка"},
	})
	if !res.IsError || !strings.Contains(res.Content, "Покупатель") {
		t.Fatalf("ожидалась ошибка про неизвестное поле: %+v", res)
	}

	// Неоднозначная ссылка: два контрагента с одним именем.
	for i := 0; i < 2; i++ {
		if err := s.store.Upsert(ctx, "Контрагенты", uuid.New(), map[string]any{"Наименование": "Дубль"}, ents[0]); err != nil {
			t.Fatal(err)
		}
	}
	_, res, _ = stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля":     map[string]any{"Контрагент": "Дубль"},
	})
	if !res.IsError || !strings.Contains(res.Content, "неоднозначно") {
		t.Fatalf("ожидалась ошибка неоднозначности: %+v", res)
	}

	// Не найденная ссылка.
	_, res, _ = stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля":     map[string]any{"Контрагент": "Нет такого"},
	})
	if !res.IsError || !strings.Contains(res.Content, "не найдено") {
		t.Fatalf("ожидалась ошибка «не найдено»: %+v", res)
	}
}

func TestAIActions_ReferenceLabelsHonorFieldMasking(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	contraID := uuid.New()
	if err := s.store.Upsert(ctx, "Контрагенты", contraID, map[string]any{"Наименование": "Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{Login: "masked", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs: map[string][]string{"Контрагенты": {"read"}},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
			"Контрагенты": {"Наименование": {Read: "mask_all"}},
		}},
	}}}}
	userCtx := auth.ContextWithUser(ctx, user)

	display, ok := s.aiRefDisplay(userCtx, ents[0], contraID)
	if !ok || display != "••••••" {
		t.Fatalf("masked reference display = %q, %v", display, ok)
	}
	refField := ents[1].Fields[2]
	if _, _, err := s.aiNormalizeValue(userCtx, refField, "Ромашка"); err == nil || !strings.Contains(err.Error(), "защищённому полю") {
		t.Fatalf("name lookup over protected field must fail closed, got %v", err)
	}
	value, label, err := s.aiNormalizeValue(userCtx, refField, contraID.String())
	if err != nil || fmt.Sprint(value) != contraID.String() || label != "••••••" {
		t.Fatalf("UUID lookup must return masked label: value=%v label=%q err=%v", value, label, err)
	}
}

func TestAIRefDisplay_UsesPresentationFallback(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Артикул", "Наименование"},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	id := uuid.New()
	if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{
		"Артикул": "", "Наименование": "Стул",
	}, entity); err != nil {
		t.Fatal(err)
	}
	display, ok := s.aiRefDisplay(ctx, entity, id)
	if !ok || display != "Стул" {
		t.Fatalf("aiRefDisplay=%q, %v; ожидалось Стул", display, ok)
	}
}

// Явно заданный «Номер» не затирается автонумерацией (Д13, issue #866):
// локальная копия проверки пустоты читала obj.Fields["Номер"] напрямую, тогда
// как Object.Set хранит ключи в нижнем регистре, — условие «пусто» было
// истинным всегда, и ИИ-действие молча переписывало номер пользователя.
func TestAIActionRun_KeepsExplicitNumber(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	contraID := uuid.New()
	if err := s.store.Upsert(ctx, "Контрагенты", contraID, map[string]any{"Наименование": "Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	action, res, _ := stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля": map[string]any{
			"Номер":      "ЗК-777",
			"Дата":       "2026-07-17T10:30",
			"Контрагент": "Ромашка",
		},
	})
	if res.IsError {
		t.Fatalf("staging: %s", res.Content)
	}

	body, _ := json.Marshal(action)
	rr := httptest.NewRecorder()
	s.aiActionRun(rr, httptest.NewRequest("POST", "/ui/ai/action", strings.NewReader(string(body))))

	var out struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rr.Body.String())
	}
	if !out.OK {
		t.Fatalf("ожидался ok, получено: %s", out.Error)
	}
	id, err := uuid.Parse(out.ID)
	if err != nil {
		t.Fatalf("нет корректного id в ответе: %q", out.ID)
	}
	row, err := s.store.GetByID(ctx, "Заказ", id, ents[1])
	if err != nil {
		t.Fatalf("документ не найден: %v", err)
	}
	num := ""
	for k, v := range row {
		if strings.EqualFold(k, "Номер") && v != nil {
			num = fmt.Sprint(v)
		}
	}
	if num != "ЗК-777" {
		t.Fatalf("явный номер затёрт: got %q, want %q", num, "ЗК-777")
	}
}

func TestAIActionRun_CreatesDraft(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	contraID := uuid.New()
	if err := s.store.Upsert(ctx, "Контрагенты", contraID, map[string]any{"Наименование": "Ромашка"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	action, res, _ := stageCreateOrder(t, s, map[string]any{
		"сущность": "Заказ",
		"поля":     map[string]any{"Дата": "2026-07-17T10:30", "Контрагент": "Ромашка"},
		"табличные_части": map[string]any{
			"Товары": []any{
				map[string]any{"Наименование": "Стол", "Количество": float64(2)},
				map[string]any{"Наименование": "Стул", "Количество": "4"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("staging: %s", res.Content)
	}

	body, _ := json.Marshal(action)
	rr := httptest.NewRecorder()
	s.aiActionRun(rr, httptest.NewRequest("POST", "/ui/ai/action", strings.NewReader(string(body))))

	var out struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rr.Body.String())
	}
	if !out.OK {
		t.Fatalf("ожидался ok, получено: %s", out.Error)
	}
	id, err := uuid.Parse(out.ID)
	if err != nil {
		t.Fatalf("нет корректного id в ответе: %q", out.ID)
	}
	if !strings.Contains(out.URL, "/ui/_ref-open/") {
		t.Fatalf("нет ссылки на созданный объект: %q", out.URL)
	}

	row, err := s.store.GetByID(ctx, "Заказ", id, ents[1])
	if err != nil {
		t.Fatalf("документ не найден: %v", err)
	}
	// Черновик: документ не проведён.
	if v, ok := row["posted"]; ok {
		if sv := fmt.Sprint(v); sv == "1" || sv == "true" {
			t.Fatalf("документ оказался проведён: %v", v)
		}
	}
	// Автонумерация сработала.
	num := ""
	for k, v := range row {
		if strings.EqualFold(k, "Номер") && v != nil {
			num = fmt.Sprint(v)
		}
	}
	if strings.TrimSpace(num) == "" {
		t.Fatalf("номер не заполнен: %+v", row)
	}
	// Строки ТЧ записаны.
	tpRows, err := s.store.GetTablePartRows(ctx, "Заказ", "Товары", id, ents[1].TableParts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tpRows) != 2 {
		t.Fatalf("ожидалось 2 строки ТЧ, получено %d: %+v", len(tpRows), tpRows)
	}
}

func TestAIActionRun_RejectsUnknown(t *testing.T) {
	s, _ := newSubmitTestServer(t, aiActionsEntities())

	// Неизвестный тип действия.
	rr := httptest.NewRecorder()
	s.aiActionRun(rr, httptest.NewRequest("POST", "/ui/ai/action", strings.NewReader(`{"тип":"удалить","сущность":"Заказ"}`)))
	if rr.Code != 400 {
		t.Fatalf("ожидался 400 на неизвестный тип, получен %d", rr.Code)
	}

	// Неизвестный вид не должен молча превращаться в document.
	rr = httptest.NewRecorder()
	s.aiActionRun(rr, httptest.NewRequest("POST", "/ui/ai/action",
		strings.NewReader(`{"тип":"создать","вид":"report","сущность":"Заказ"}`)))
	if rr.Code != 400 {
		t.Fatalf("ожидался 400 на неизвестный вид, получен %d", rr.Code)
	}

	// Подделанное действие с несуществующим полем отклоняется повторной валидацией.
	rr = httptest.NewRecorder()
	s.aiActionRun(rr, httptest.NewRequest("POST", "/ui/ai/action",
		strings.NewReader(`{"тип":"создать","вид":"document","сущность":"Заказ","поля":{"Взлом":"x"}}`)))
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.OK || !strings.Contains(out.Error, "Взлом") {
		t.Fatalf("ожидалась ошибка валидации, получено: %+v", out)
	}
}

func TestAIActions_RLSHidesReferenceAndOpen(t *testing.T) {
	ents := aiActionsEntities()
	// Владелец нужен для строковой политики на справочнике ссылок.
	ents[0].Fields = append(ents[0].Fields, metadata.Field{Name: "Owner", Type: metadata.FieldTypeString})
	s, ctx := newSubmitTestServer(t, ents)
	allowedID := uuid.New()
	hiddenID := uuid.New()
	if err := s.store.Upsert(ctx, ents[0].Name, allowedID, map[string]any{"Наименование": "Доступный", "Owner": "u"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, ents[0].Name, hiddenID, map[string]any{"Наименование": "Скрытый", "Owner": "other"}, ents[0]); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{Login: "u", Roles: []*auth.Role{{Permissions: auth.Permission{
		Catalogs:  map[string][]string{ents[0].Name: {"read"}},
		Documents: map[string][]string{ents[1].Name: {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			ents[0].Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}},
		}},
	}}}}
	userCtx := auth.ContextWithUser(context.Background(), user)

	// Прямой UUID скрытой ссылки не должен раскрывать подпись и не должен
	// попадать в подготовленное действие.
	pending := &aiPendingActions{}
	res := s.aiStageCreate(userCtx, metadata.KindDocument, llm.ToolCall{ID: "c1", Input: map[string]any{
		"сущность": ents[1].Name,
		"поля":     map[string]any{"Контрагент": hiddenID.String()},
	}}, pending)
	if !res.IsError || strings.Contains(res.Content, "Скрытый") || len(pending.Actions) != 0 {
		t.Fatalf("скрытая RLS-ссылка прошла staging: result=%+v actions=%+v", res, pending.Actions)
	}

	// Тот же UUID нельзя использовать для карточки открытия формы: прежний код
	// читал GetByID напрямую и возвращал модели подпись скрытой записи.
	res = s.aiStageOpen(userCtx, llm.ToolCall{ID: "o1", Input: map[string]any{
		"вид": "catalog", "сущность": ents[0].Name, "id": hiddenID.String(),
	}}, pending)
	if !res.IsError || strings.Contains(res.Content, "Скрытый") {
		t.Fatalf("открытие раскрыло скрытую RLS-запись: %+v", res)
	}

	// Доступная строка по-прежнему разрешена.
	res = s.aiStageCreate(userCtx, metadata.KindDocument, llm.ToolCall{ID: "c2", Input: map[string]any{
		"сущность": ents[1].Name,
		"поля":     map[string]any{"Контрагент": allowedID.String()},
	}}, pending)
	if res.IsError || !strings.Contains(res.Content, "Доступный") {
		t.Fatalf("доступная RLS-ссылка отклонена: %+v", res)
	}
}

// TestAIChat_ActionsInResponse — весь путь: aiChat → tool-use (mock Anthropic
// просит создать_документ) → подготовленное действие попадает в JSON-ответ чата.
func TestAIChat_ActionsInResponse(t *testing.T) {
	ents := aiActionsEntities()
	s, ctx := newSubmitTestServer(t, ents)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "tool_result") {
			_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"Подтвердите создание"}],"usage":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"создать_документ",` +
			`"input":{"сущность":"Заказ","поля":{"Дата":"2026-07-17"}}}],"usage":{}}`))
	}))
	defer srv.Close()
	if err := s.store.SaveLLMConfig(ctx, chatConfig(srv.URL)); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.aiChat(rr, httptest.NewRequest("POST", "/ui/ai/chat",
		strings.NewReader(`{"messages":[{"role":"user","content":"создай заказ"}]}`)))

	var out struct {
		OK      bool       `json:"ok"`
		Text    string     `json:"text"`
		Actions []aiAction `json:"actions"`
		Error   string     `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rr.Body.String())
	}
	if !out.OK {
		t.Fatalf("ожидался ok, получено: %s", out.Error)
	}
	if len(out.Actions) != 1 || out.Actions[0].Type != "создать" || out.Actions[0].Entity != "Заказ" {
		t.Fatalf("действие не дошло до ответа чата: %+v", out.Actions)
	}
}

func TestAIActions_StageOpen(t *testing.T) {
	s, _ := newSubmitTestServer(t, aiActionsEntities())
	r := httptest.NewRequest("POST", "/ui/ai/chat", nil)
	_, exec, pending := s.aiTools(r)

	// Открыть список документов.
	res := exec(context.Background(), llm.ToolCall{ID: "o1", Name: "открыть_форму",
		Input: map[string]any{"вид": "document", "сущность": "Заказ"}})
	if res.IsError {
		t.Fatalf("открыть_форму: %s", res.Content)
	}
	if len(pending.Actions) != 1 || pending.Actions[0].Type != "открыть" || pending.Actions[0].Kind != "document" {
		t.Fatalf("неожиданное действие: %+v", pending.Actions)
	}

	// Несуществующий отчёт — ошибка.
	res = exec(context.Background(), llm.ToolCall{ID: "o2", Name: "открыть_форму",
		Input: map[string]any{"вид": "report", "сущность": "НетТакого"}})
	if !res.IsError {
		t.Fatalf("ожидалась ошибка на несуществующий отчёт: %+v", res)
	}

	// Некорректный id — ошибка.
	res = exec(context.Background(), llm.ToolCall{ID: "o3", Name: "открыть_форму",
		Input: map[string]any{"вид": "document", "сущность": "Заказ", "id": "не-uuid"}})
	if !res.IsError {
		t.Fatalf("ожидалась ошибка на некорректный id: %+v", res)
	}
}
