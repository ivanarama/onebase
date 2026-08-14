package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Действия ИИ-чата (план 51, слой 2, фаза 2): мутации — отдельные инструменты,
// только с подтверждением в UI. Инструмент-мутация ничего не записывает: он
// валидирует аргументы, нормализует значения (ссылки → UUID, перечисления →
// каноническое имя) и складывает «отложенное действие» в аккумулятор запроса.
// Действия уходят клиенту вместе с ответом модели; чат рисует карточку с
// кнопкой «Создать», и только её нажатие исполняет запись через aiActionRun —
// с повторной валидацией и под RBAC текущего пользователя. Создание всегда
// даёт ЧЕРНОВИК: проведение документов ИИ недоступно by design.

// aiAction — отложенное действие, ожидающее подтверждения пользователя.
// JSON-ключи русские — в одном стиле со схемами инструментов чата.
type aiAction struct {
	Type   string                      `json:"тип"`            // "создать" | "открыть"
	Kind   string                      `json:"вид"`            // document|catalog|report|processor
	Entity string                      `json:"сущность"`       // имя объекта конфигурации
	ID     string                      `json:"id,omitempty"`   // для "открыть": UUID записи
	Label  string                      `json:"подпись"`        // человекочитаемая сводка для карточки
	Fields map[string]any              `json:"поля,omitempty"` // нормализованные поля шапки
	TPRows map[string][]map[string]any `json:"тч,omitempty"`   // нормализованные строки ТЧ
}

// aiPendingActions — аккумулятор действий одного запроса чата. Наполняется
// исполнителем инструментов, вычитывается aiChat при формировании ответа.
type aiPendingActions struct {
	Actions []aiAction
}

// aiMutationTools — инструменты-мутации и команда открытия формы. Выдаются тем
// же пользователям, что и инструменты данных (aiDataAllowed): администраторам и
// пользователям с флагом доступа ИИ к данным.
func aiMutationTools() []llm.Tool {
	createSchema := func(what string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"сущность": map[string]any{"type": "string", "description": "имя " + what + " из описание_данных"},
				"поля": map[string]any{"type": "object", "description": "значения полей шапки. Ссылочные поля — UUID " +
					"(найди заранее через выполнить_запрос) или точное наименование; даты — ГГГГ-ММ-ДД или ГГГГ-ММ-ДДTЧЧ:ММ"},
				"табличные_части": map[string]any{"type": "object", "description": "строки табличных частей: {\"ИмяТЧ\":[{поле:значение,…},…]}"},
			},
			"required": []any{"сущность"},
		}
	}
	return []llm.Tool{
		{
			Name: "создать_документ",
			Description: "Подготовить создание нового документа (черновик, БЕЗ проведения). Запись произойдёт только " +
				"после того, как пользователь подтвердит действие кнопкой в чате. Сначала выясни структуру через " +
				"описание_данных и найди UUID ссылочных значений через выполнить_запрос.",
			Schema: createSchema("документа"),
		},
		{
			Name: "создать_элемент_справочника",
			Description: "Подготовить создание нового элемента справочника. Запись произойдёт только после " +
				"подтверждения пользователем кнопкой в чате.",
			Schema: createSchema("справочника"),
		},
		{
			Name: "открыть_форму",
			Description: "Показать пользователю кнопку открытия формы: список документов или справочника (без id), " +
				"форма конкретной записи (с id), форма отчёта или обработки.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"вид":      map[string]any{"type": "string", "enum": []any{"document", "catalog", "report", "processor"}},
					"сущность": map[string]any{"type": "string", "description": "имя объекта конфигурации"},
					"id":       map[string]any{"type": "string", "description": "UUID записи — открыть её форму вместо списка"},
				},
				"required": []any{"вид", "сущность"},
			},
		},
	}
}

func aiErr(call llm.ToolCall, msg string) llm.ToolResult {
	return llm.ToolResult{ID: call.ID, Content: msg, IsError: true}
}

// aiEntityNames — список имён сущностей вида для подсказки модели в ошибке.
func (s *Server) aiEntityNames(kind metadata.Kind) string {
	names := make([]string, 0)
	for _, e := range s.reg.Entities() {
		if e.Kind == kind {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// aiRefLookupField — по какому реквизиту резолвить ссылку, переданную именем:
// Наименование/Description, затем Номер, затем первый строковый реквизит.
func aiRefLookupField(entity *metadata.Entity) string {
	// Раньше здесь жила своя копия порядка предпочтения — единственная именная
	// среди трёх позиционных. Теперь правило одно на платформу (план 117,
	// решение №3): metadata.LabelFields.
	if f := metadata.LabelFields(entity); len(f) > 0 {
		return f[0].Name
	}
	return ""
}

// aiRefDisplay — представление доступной пользователю записи для подписи
// карточки. Чтение идёт с объектной и строковой проверкой: UUID из аргументов
// модели/клиента не должен раскрывать подпись скрытой RLS-записи.
func (s *Server) aiRefDisplay(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (string, bool) {
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || !s.rowAllowsSelected(ctx, entity, row) {
		return "", false
	}
	// Подпись ссылки уходит в tool result и затем модели, поэтому к ней применимы
	// те же field policies, что к карточке и REST. Hidden lookup исчезнет из row,
	// masked lookup вернётся только в замаскированном виде.
	s.maskRecord(ctx, entity, row)
	field := aiRefLookupField(entity)
	if field == "" {
		return id.String(), true
	}
	for k, v := range row {
		if strings.EqualFold(k, field) && v != nil {
			if txt := strings.TrimSpace(fmt.Sprint(v)); txt != "" {
				return txt, true
			}
		}
	}
	return id.String(), true
}

// aiDateLayouts — принимаемые форматы дат (те же, что у форм).
var aiDateLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"}

// aiNormalizeFields проверяет и нормализует значения полей из аргументов
// инструмента: имена приводит к каноническим из метаданных, ссылки — к UUID
// (имя резолвится по справочнику, неоднозначность — ошибка), перечисления — к
// каноническому значению, даты — к строке ГГГГ-ММ-ДДTЧЧ:ММ:СС. Результат
// JSON-безопасен (уходит клиенту и возвращается на исполнение). Вторым
// значением — строки «Поле: значение» для подписи карточки.
func (s *Server) aiNormalizeFields(ctx context.Context, fields []metadata.Field, in map[string]any) (map[string]any, []string, error) {
	out := make(map[string]any, len(in))
	labels := make([]string, 0, len(in))
	// Стабильный порядок подписи — по порядку полей в метаданных.
	for _, f := range fields {
		var raw any
		found := false
		for k, v := range in {
			if strings.EqualFold(k, f.Name) {
				raw, found = v, true
				break
			}
		}
		if !found || raw == nil {
			continue
		}
		val, label, err := s.aiNormalizeValue(ctx, f, raw)
		if err != nil {
			return nil, nil, err
		}
		out[f.Name] = val
		labels = append(labels, f.Name+": "+label)
	}
	// Поля, которых нет в метаданных, — ошибка (модель перепутала имя).
	for k := range in {
		known := false
		for _, f := range fields {
			if strings.EqualFold(k, f.Name) {
				known = true
				break
			}
		}
		if !known {
			names := make([]string, 0, len(fields))
			for _, f := range fields {
				names = append(names, f.Name)
			}
			return nil, nil, fmt.Errorf("неизвестное поле %q (доступны: %s)", k, strings.Join(names, ", "))
		}
	}
	return out, labels, nil
}

func (s *Server) aiNormalizeValue(ctx context.Context, f metadata.Field, raw any) (any, string, error) {
	str := strings.TrimSpace(fmt.Sprint(raw))
	switch {
	case metadata.IsReference(f.Type):
		refEntity := s.reg.GetEntity(f.RefEntity)
		if refEntity == nil {
			return nil, "", fmt.Errorf("поле %s: неизвестная целевая сущность ссылки %q", f.Name, f.RefEntity)
		}
		if !s.canCtx(ctx, string(refEntity.Kind), refEntity.Name, "read") {
			return nil, "", fmt.Errorf("поле %s: нет права на чтение %s", f.Name, refEntity.Name)
		}
		if id, err := uuid.Parse(str); err == nil {
			display, ok := s.aiRefDisplay(ctx, refEntity, id)
			if !ok {
				return nil, "", fmt.Errorf("поле %s: запись %s не найдена или недоступна", f.Name, refEntity.Name)
			}
			return id.String(), display, nil
		}
		// Резолв по имени. Под строковым доступом (RLS) поиск в обход политики
		// не делаем — модель должна найти UUID через выполнить_запрос (тот путь
		// фильтруется политикой).
		if s.rowAccessRestricted(ctx, refEntity, "read") {
			return nil, "", fmt.Errorf("поле %s: для %s действует строковый доступ — передай UUID, найденный через выполнить_запрос", f.Name, refEntity.Name)
		}
		lookup := aiRefLookupField(refEntity)
		if lookup == "" {
			return nil, "", fmt.Errorf("поле %s: у %s нет строкового реквизита для поиска по имени — передай UUID", f.Name, refEntity.Name)
		}
		for protected := range s.fieldDecisions(ctx, refEntity) {
			if strings.EqualFold(protected, lookup) {
				return nil, "", fmt.Errorf("поле %s: поиск %s по защищённому полю %s запрещён — передай UUID", f.Name, refEntity.Name, lookup)
			}
		}
		id, display, count, err := s.store.MatchCatalogByField(ctx, refEntity, lookup, str)
		if err != nil {
			return nil, "", fmt.Errorf("поле %s: поиск %q в %s: %w", f.Name, str, refEntity.Name, err)
		}
		switch count {
		case 0:
			return nil, "", fmt.Errorf("поле %s: %q не найдено в %s (поиск по %s) — уточни значение или найди UUID через выполнить_запрос", f.Name, str, refEntity.Name, lookup)
		case 1:
			return id, display, nil
		default:
			return nil, "", fmt.Errorf("поле %s: %q в %s неоднозначно (%d совпадений) — найди UUID нужной записи через выполнить_запрос", f.Name, str, refEntity.Name, count)
		}
	case metadata.IsEnum(f.Type):
		enumName := f.EnumName
		if enumName == "" {
			enumName = metadata.EnumTypeName(f.Type)
		}
		enum := s.reg.GetEnum(enumName)
		if enum == nil {
			return nil, "", fmt.Errorf("поле %s: неизвестное перечисление %q", f.Name, enumName)
		}
		for _, v := range enum.Values {
			if strings.EqualFold(v, str) {
				return v, v, nil
			}
		}
		return nil, "", fmt.Errorf("поле %s: значение %q не входит в перечисление %s (допустимы: %s)", f.Name, str, enumName, strings.Join(enum.Values, ", "))
	}
	switch f.Type {
	case metadata.FieldTypeDate:
		for _, layout := range aiDateLayouts {
			if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
				return t.Format("2006-01-02T15:04:05"), t.Format("2006-01-02 15:04"), nil
			}
		}
		return nil, "", fmt.Errorf("поле %s: не разобрана дата %q (нужен формат ГГГГ-ММ-ДД или ГГГГ-ММ-ДДTЧЧ:ММ)", f.Name, str)
	case metadata.FieldTypeNumber:
		if n, ok := raw.(float64); ok {
			return n, strconv.FormatFloat(n, 'f', -1, 64), nil
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(str, ",", "."), 64)
		if err != nil {
			return nil, "", fmt.Errorf("поле %s: не число: %q", f.Name, str)
		}
		return n, strconv.FormatFloat(n, 'f', -1, 64), nil
	case metadata.FieldTypeBool:
		if b, ok := raw.(bool); ok {
			return b, fmt.Sprint(b), nil
		}
		switch strings.ToLower(str) {
		case "true", "истина", "да":
			return true, "true", nil
		case "false", "ложь", "нет":
			return false, "false", nil
		}
		return nil, "", fmt.Errorf("поле %s: не булево значение: %q", f.Name, str)
	}
	// Строки и прочие текстовые типы. Санитизация richtext — на исполнении.
	label := str
	if len([]rune(label)) > 80 {
		label = string([]rune(label)[:77]) + "…"
	}
	return str, label, nil
}

// aiFindTablePart — ТЧ по имени без учёта регистра.
func aiFindTablePart(entity *metadata.Entity, name string) *metadata.TablePart {
	for i := range entity.TableParts {
		if strings.EqualFold(entity.TableParts[i].Name, name) {
			return &entity.TableParts[i]
		}
	}
	return nil
}

// aiNormalizeCreate валидирует аргументы создания (или возвращённое клиентом
// действие) и строит нормализованное действие. Общая точка стадии подготовки
// и исполнения: клиент мог прислать что угодно, поэтому на исполнении всё
// проверяется заново.
func (s *Server) aiNormalizeCreate(ctx context.Context, kind metadata.Kind, entityName string, fieldsIn map[string]any, tpIn map[string][]map[string]any) (*metadata.Entity, aiAction, error) {
	var action aiAction
	entity := s.reg.GetEntity(entityName)
	if entity == nil || entity.Kind != kind {
		what := "документ"
		if kind == metadata.KindCatalog {
			what = "справочник"
		}
		return nil, action, fmt.Errorf("%s %q не найден (есть: %s)", what, entityName, s.aiEntityNames(kind))
	}
	if !s.canCtx(ctx, string(entity.Kind), entity.Name, "write") {
		return nil, action, fmt.Errorf("у вас нет права на запись %s", entity.Name)
	}
	fields, labels, err := s.aiNormalizeFields(ctx, entity.Fields, fieldsIn)
	if err != nil {
		return nil, action, err
	}
	tpRows := make(map[string][]map[string]any)
	tpLabels := make([]string, 0, len(tpIn))
	for tpName, rows := range tpIn {
		tp := aiFindTablePart(entity, tpName)
		if tp == nil {
			names := make([]string, 0, len(entity.TableParts))
			for _, t := range entity.TableParts {
				names = append(names, t.Name)
			}
			return nil, action, fmt.Errorf("у %s нет табличной части %q (есть: %s)", entity.Name, tpName, strings.Join(names, ", "))
		}
		norm := make([]map[string]any, 0, len(rows))
		for i, row := range rows {
			nr, _, err := s.aiNormalizeFields(ctx, tp.Fields, row)
			if err != nil {
				return nil, action, fmt.Errorf("%s, строка %d: %w", tp.Name, i+1, err)
			}
			if len(nr) > 0 {
				norm = append(norm, nr)
			}
		}
		tpRows[tp.Name] = norm
		tpLabels = append(tpLabels, fmt.Sprintf("%s: %d стр.", tp.Name, len(norm)))
	}
	kindTitle := "Документ"
	if entity.Kind == metadata.KindCatalog {
		kindTitle = "Элемент справочника"
	}
	sort.Strings(tpLabels)
	lines := append([]string{kindTitle + " «" + entity.Name + "»"}, labels...)
	lines = append(lines, tpLabels...)
	action = aiAction{
		Type:   "создать",
		Kind:   string(entity.Kind),
		Entity: entity.Name,
		Label:  strings.Join(lines, "\n"),
		Fields: fields,
		TPRows: tpRows,
	}
	return entity, action, nil
}

// aiTPInput приводит аргумент «табличные_части» инструмента к типизированной
// карте: {"ИмяТЧ": [{поле: значение}]}.
func aiTPInput(raw any) (map[string][]map[string]any, error) {
	out := make(map[string][]map[string]any)
	if raw == nil {
		return out, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("табличные_части: ожидается объект {\"ИмяТЧ\":[строки]}")
	}
	for tpName, rowsRaw := range m {
		rowsAny, ok := rowsRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("табличные_части.%s: ожидается массив строк", tpName)
		}
		rows := make([]map[string]any, 0, len(rowsAny))
		for i, r := range rowsAny {
			rm, ok := r.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("табличные_части.%s[%d]: ожидается объект {поле: значение}", tpName, i)
			}
			rows = append(rows, rm)
		}
		out[tpName] = rows
	}
	return out, nil
}

// aiStageCreate — исполнение инструмента создать_документ/создать_элемент_справочника:
// валидация + нормализация + постановка действия в очередь на подтверждение.
func (s *Server) aiStageCreate(ctx context.Context, kind metadata.Kind, call llm.ToolCall, pending *aiPendingActions) llm.ToolResult {
	name, _ := call.Input["сущность"].(string)
	fieldsIn, _ := call.Input["поля"].(map[string]any)
	tpIn, err := aiTPInput(call.Input["табличные_части"])
	if err != nil {
		return aiErr(call, err.Error())
	}
	_, action, err := s.aiNormalizeCreate(ctx, kind, strings.TrimSpace(name), fieldsIn, tpIn)
	if err != nil {
		return aiErr(call, err.Error())
	}
	pending.Actions = append(pending.Actions, action)
	return llm.ToolResult{ID: call.ID, Content: "Черновик подготовлен и показан пользователю в чате с кнопкой «Создать». " +
		"Запись произойдёт только после подтверждения пользователем — не вызывай инструмент повторно для этого же объекта, " +
		"просто сообщи, что нужно подтвердить действие. Сводка:\n" + action.Label}
}

// aiStageOpen — исполнение инструмента открыть_форму: валидация и постановка
// команды навигации. Ничего не исполняет на сервере: клиент строит URL из
// провалидированных частей и открывает вкладку по нажатию кнопки.
func (s *Server) aiStageOpen(ctx context.Context, call llm.ToolCall, pending *aiPendingActions) llm.ToolResult {
	kind, _ := call.Input["вид"].(string)
	name, _ := call.Input["сущность"].(string)
	idStr, _ := call.Input["id"].(string)
	name = strings.TrimSpace(name)
	label := ""
	switch kind {
	case "document", "catalog":
		entity := s.reg.GetEntity(name)
		if entity == nil || string(entity.Kind) != kind {
			return aiErr(call, fmt.Sprintf("объект %q вида %s не найден", name, kind))
		}
		if !s.canCtx(ctx, string(entity.Kind), entity.Name, "read") {
			return aiErr(call, "у пользователя нет права на чтение "+entity.Name)
		}
		label = "Открыть список: " + entity.DisplayName("")
		if idStr != "" {
			id, err := uuid.Parse(idStr)
			if err != nil {
				return aiErr(call, "id должен быть UUID")
			}
			display, ok := s.aiRefDisplay(ctx, entity, id)
			if !ok {
				return aiErr(call, "запись не найдена или недоступна")
			}
			label = "Открыть: " + entity.DisplayName("") + " — " + display
			idStr = id.String()
		}
	case "report":
		if s.reg.GetReport(name) == nil {
			return aiErr(call, fmt.Sprintf("отчёт %q не найден", name))
		}
		if !s.canCtx(ctx, "report", name, "run") {
			return aiErr(call, "у пользователя нет права на запуск отчёта "+name)
		}
		idStr = ""
		label = "Открыть отчёт: " + name
	case "processor":
		if s.reg.GetProcessor(name) == nil {
			return aiErr(call, fmt.Sprintf("обработка %q не найдена", name))
		}
		if !s.canCtx(ctx, "processor", name, "run") {
			return aiErr(call, "у пользователя нет права на запуск обработки "+name)
		}
		idStr = ""
		label = "Открыть обработку: " + name
	default:
		return aiErr(call, "вид должен быть одним из: document, catalog, report, processor")
	}
	pending.Actions = append(pending.Actions, aiAction{Type: "открыть", Kind: kind, Entity: name, ID: idStr, Label: label})
	return llm.ToolResult{ID: call.ID, Content: "Кнопка открытия формы показана пользователю в чате: " + label}
}

// aiActionRun — POST /ui/ai/action: пользователь подтвердил действие кнопкой в
// чате. Клиент возвращает действие как есть; серверу оно не доверено — всё
// валидируется заново (метаданные, права, ссылки), затем создаётся черновик
// через entityservice.Save (OnWrite-хук отрабатывает, документ НЕ проводится).
func (s *Server) aiActionRun(w http.ResponseWriter, r *http.Request) {
	if !s.aiDataAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Действия ИИ-чата вам недоступны"})
		return
	}
	var req aiAction
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Некорректный запрос: " + err.Error()})
		return
	}
	if req.Type != "создать" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Неизвестный тип действия"})
		return
	}
	var kind metadata.Kind
	switch req.Kind {
	case string(metadata.KindDocument):
		kind = metadata.KindDocument
	case string(metadata.KindCatalog):
		kind = metadata.KindCatalog
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Неизвестный вид действия"})
		return
	}
	ctx := r.Context()
	entity, action, err := s.aiNormalizeCreate(ctx, kind, req.Entity, req.Fields, req.TPRows)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}

	obj := runtime.NewObject(entity.Name, entity.Kind)
	for k, v := range action.Fields {
		obj.Set(k, s.aiTypedValue(entity.Fields, k, v))
	}
	obj.TablePartRows = action.TPRows
	// Строковый доступ (план 79): автозаполнение обязательных полей политики и
	// проверка, что пользователь вправе записать такую строку.
	if err := s.autoFillRowAccessFields(ctx, entity, "write", obj.Fields); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Строковый доступ: " + err.Error()})
		return
	}
	if err := s.checkDSLRowAccess(ctx, entity, "write", uuid.Nil, obj.Fields); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	result, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity:        entity,
		ID:            obj.ID,
		IsNew:         true,
		Fields:        obj.Fields,
		TablePartRows: obj.TablePartRows,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "Ошибка записи: " + err.Error()})
		return
	}
	if result.DSLError != "" {
		writeJSON(w, http.StatusOK, map[string]any{"error": "Запись отклонена: " + result.DSLError})
		return
	}

	// Журнал ИИ (план 54): факт создания объекта через ассистента.
	entry := storage.AIAuditEntry{Task: "чат-создание", Query: entity.Name + " " + obj.ID.String(), Rows: 1}
	if user := auth.UserFromContext(ctx); user != nil {
		entry.UserID, entry.UserLogin = user.ID, user.Login
	}
	s.store.LogAIQuery(ctx, entry)

	display := obj.ID.String()
	if readable, ok := s.aiRefDisplay(ctx, entity, obj.ID); ok {
		display = readable
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"id":      obj.ID.String(),
		"подпись": entity.DisplayName("") + " " + display,
		"url":     "/ui/_ref-open/" + url.PathEscape(entity.Name) + "/" + obj.ID.String(),
	})
}

// aiTypedValue переводит нормализованное JSON-значение в типизированное для
// записи: даты — time.Time, richtext — санитизация; ссылки остаются
// UUID-строками (их обогащает PrepareHook entityservice, как у форм).
func (s *Server) aiTypedValue(fields []metadata.Field, name string, v any) any {
	for _, f := range fields {
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		if f.Type == metadata.FieldTypeDate {
			if str, ok := v.(string); ok {
				for _, layout := range aiDateLayouts {
					if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
						return t
					}
				}
			}
		}
		if metadata.IsRichText(f.Type) {
			if str, ok := v.(string); ok {
				return richtext.Sanitize(str)
			}
		}
		break
	}
	return v
}
