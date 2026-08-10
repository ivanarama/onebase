package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// objectAttributeValue реализует DSL-функцию ЗначениеРеквизитаОбъекта(Ссылка,
// "Реквизит") — единичный запрос к БД за значением реквизита по ссылке.
// Ссылка несёт только UUID и наименование, поэтому остальные реквизиты
// доступны только так. Ссылочный реквизит возвращается как ссылка (Ref) —
// чтобы работали цепочки ЗначениеРеквизитаОбъекта(ЗначениеРеквизитаОбъекта(…)).
// Пустая ссылка / отсутствующая запись → nil; неверное имя реквизита →
// ошибка (иначе вернулся бы тихий nil — та самая боль).
func (s *Server) objectAttributeValue(ctx context.Context, args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: ожидаются ссылка и имя реквизита")
	}
	if args[0] == nil {
		return nil, nil
	}
	ref, ok := args[0].(*interpreter.Ref)
	if !ok {
		return nil, nil // не ссылка (например, строка из формы) — пропускаем
	}
	attrName := strings.TrimSpace(fmt.Sprint(args[1]))
	if attrName == "" {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: не указано имя реквизита")
	}
	if ref.UUID == "" {
		return nil, nil // пустая ссылка
	}
	if ref.Type == "" {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: у ссылки %q не определён тип объекта", ref.Name)
	}
	entity := s.reg.GetEntity(ref.Type)
	if entity == nil {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: неизвестный тип объекта %q", ref.Type)
	}
	field := findObjectAttributeField(entity, attrName)
	if field == nil {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: у объекта %s нет реквизита %q", ref.Type, attrName)
	}
	id, err := uuid.Parse(ref.UUID)
	if err != nil {
		return nil, fmt.Errorf("ЗначениеРеквизитаОбъекта: неверный идентификатор ссылки %q", ref.UUID)
	}
	if err := s.checkDSLRowAccess(ctx, entity, "read", id, nil); err != nil {
		if errors.Is(err, interpreter.ErrRowAccessDenied) {
			return nil, nil
		}
		return nil, err
	}
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || row == nil {
		return nil, nil // запись не найдена
	}
	// План 88E: читается ЧУЖАЯ сохранённая запись — тот же путь, что и
	// разыменование this.Клиент.Телефон (dsl_ref_attr.go), и он обязан
	// подчиняться полевой политике роли. Без маски обработка обходит её одной
	// строкой: Сообщить(ЗначениеРеквизитаОбъекта(Ссылка, "Телефон")).
	val := row[field.Name]
	var out any
	if field.RefEntity != "" {
		out = s.refFromValue(ctx, field.RefEntity, val)
	} else {
		out = normalizeAttrValue(field.Type, val)
	}
	return s.maskDSLValue(ctx, entity, field.Name, out), nil
}

type bulkObjectRef struct {
	key   *interpreter.Ref
	id    uuid.UUID
	idStr string
}

// objectAttributeValues реализует DSL-функцию
// ЗначенияРеквизитовОбъектов(Ссылки, "ТипОбъекта", ["Реквизит1", ...]).
// Возвращает Соответствие: Ссылка -> Структура с запрошенными реквизитами.
func (s *Server) objectAttributeValues(ctx context.Context, args []any) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: ожидаются ссылки, имя типа объекта и список реквизитов")
	}
	entityName := strings.TrimSpace(fmt.Sprint(args[1]))
	if entityName == "" {
		return nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: не указан тип объекта")
	}
	entity := s.reg.GetEntity(entityName)
	if entity == nil {
		return nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: неизвестный тип объекта %q", entityName)
	}
	fields, err := objectAttributeFields(entity, args[2], "ЗначенияРеквизитовОбъектов")
	if err != nil {
		return nil, err
	}
	refs, _, err := s.collectBulkObjectRefs(ctx, args[0], entity)
	if err != nil {
		return nil, err
	}
	refs, ids, err := s.filterReadableBulkObjectRefs(ctx, entity, refs)
	if err != nil {
		return nil, err
	}
	out := &interpreter.Map{}
	if len(refs) == 0 {
		return out, nil
	}

	queryFields := uniqueObjectFields(append(fields, displayField(entity)...))
	rows, err := s.store.GetFieldsByIDs(ctx, entity, ids, queryFields)
	if err != nil {
		return nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: %w", err)
	}
	refNames := s.bulkReferenceNames(ctx, rows, fields)
	for _, ref := range refs {
		row := rows[ref.idStr]
		if row == nil {
			continue
		}
		if ref.key.Name == "" || ref.key.Name == ref.key.UUID {
			ref.key.Name = s.maskedRecordLabel(ctx, entity, row)
		}
		vals := make(map[string]any, len(fields))
		for _, f := range fields {
			raw := row[f.Name]
			var v any
			if f.RefEntity != "" {
				v = s.refFromValueCached(ctx, f.RefEntity, raw, refNames[f.RefEntity])
			} else {
				v = normalizeAttrValue(f.Type, raw)
			}
			// Маска здесь, а не побочно: единственным гейтом был
			// maskedRecordLabel строкой выше, который маскирует row на месте и
			// вызывается ТОЛЬКО когда у ссылки ещё нет наименования. Ссылка с
			// уже заполненным именем (обычный случай из формы или запроса)
			// уносила реальные значения.
			vals[f.Name] = s.maskDSLValue(ctx, entity, f.Name, v)
		}
		out.CallMethod("вставить", []any{ref.key, interpreter.NewStructFromMap(vals)})
	}
	return out, nil
}

func (s *Server) collectBulkObjectRefs(ctx context.Context, raw any, entity *metadata.Entity) ([]bulkObjectRef, []uuid.UUID, error) {
	items, ok := valueItems(raw)
	if !ok {
		items = []any{raw}
	}
	refs := make([]bulkObjectRef, 0, len(items))
	ids := make([]uuid.UUID, 0, len(items))
	seen := map[string]bool{}
	manager := s.refManagerFor(entity, ctx)
	for _, item := range items {
		if item == nil {
			continue
		}
		var idStr, name, typ string
		switch v := item.(type) {
		case *interpreter.Ref:
			idStr, name, typ = strings.TrimSpace(v.UUID), v.Name, v.Type
		case interface{ GetRefUUID() string }:
			idStr = strings.TrimSpace(v.GetRefUUID())
		case string:
			idStr = strings.TrimSpace(v)
		default:
			return nil, nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: ожидается ссылка или UUID, получено %T", item)
		}
		if idStr == "" {
			continue
		}
		if typ == "" {
			typ = entity.Name
		}
		if !strings.EqualFold(typ, entity.Name) {
			return nil, nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: ссылка типа %q не соответствует типу %q", typ, entity.Name)
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, nil, fmt.Errorf("ЗначенияРеквизитовОбъектов: неверный идентификатор ссылки %q", idStr)
		}
		idStr = id.String()
		refs = append(refs, bulkObjectRef{
			key:   &interpreter.Ref{UUID: idStr, Name: name, Type: entity.Name, Manager: manager},
			id:    id,
			idStr: idStr,
		})
		if !seen[idStr] {
			ids = append(ids, id)
			seen[idStr] = true
		}
	}
	return refs, ids, nil
}

func (s *Server) filterReadableBulkObjectRefs(ctx context.Context, entity *metadata.Entity, refs []bulkObjectRef) ([]bulkObjectRef, []uuid.UUID, error) {
	filtered := make([]bulkObjectRef, 0, len(refs))
	ids := make([]uuid.UUID, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if err := s.checkDSLRowAccess(ctx, entity, "read", ref.id, nil); err != nil {
			if errors.Is(err, interpreter.ErrRowAccessDenied) {
				continue
			}
			return nil, nil, err
		}
		filtered = append(filtered, ref)
		if !seen[ref.idStr] {
			ids = append(ids, ref.id)
			seen[ref.idStr] = true
		}
	}
	return filtered, ids, nil
}

func objectAttributeFields(entity *metadata.Entity, raw any, funcName string) ([]metadata.Field, error) {
	items, ok := valueItems(raw)
	if !ok {
		if s, ok := raw.(string); ok {
			for _, part := range strings.Split(s, ",") {
				items = append(items, strings.TrimSpace(part))
			}
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s: список реквизитов пуст", funcName)
	}
	fields := make([]metadata.Field, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		name := strings.TrimSpace(fmt.Sprint(item))
		if name == "" {
			continue
		}
		field := findObjectAttributeField(entity, name)
		if field == nil {
			return nil, fmt.Errorf("%s: у объекта %s нет реквизита %q", funcName, entity.Name, name)
		}
		low := strings.ToLower(field.Name)
		if !seen[low] {
			fields = append(fields, *field)
			seen[low] = true
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%s: список реквизитов пуст", funcName)
	}
	return fields, nil
}

func valueItems(v any) ([]any, bool) {
	switch x := v.(type) {
	case *interpreter.Array:
		return x.Iterate(), true
	case []any:
		return x, true
	case []string:
		items := make([]any, 0, len(x))
		for _, s := range x {
			items = append(items, s)
		}
		return items, true
	default:
		return nil, false
	}
}

func findObjectAttributeField(entity *metadata.Entity, name string) *metadata.Field {
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i]
		}
	}
	return nil
}

func displayField(entity *metadata.Entity) []metadata.Field {
	// Тот же выбор, что у RowLabel: по имени реквизита, а не по позиции в YAML
	// (план 117, решение №3).
	if f := metadata.LabelFields(entity); len(f) > 0 {
		return f[:1]
	}
	return nil
}

func uniqueObjectFields(fields []metadata.Field) []metadata.Field {
	out := make([]metadata.Field, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		low := strings.ToLower(f.Name)
		if seen[low] {
			continue
		}
		out = append(out, f)
		seen[low] = true
	}
	return out
}

func (s *Server) bulkReferenceNames(ctx context.Context, rows map[string]map[string]any, fields []metadata.Field) map[string]map[string]string {
	idsByEntity := map[string]map[string]uuid.UUID{}
	for _, f := range fields {
		if f.RefEntity == "" {
			continue
		}
		if idsByEntity[f.RefEntity] == nil {
			idsByEntity[f.RefEntity] = map[string]uuid.UUID{}
		}
		for _, row := range rows {
			if idStr, id, ok := uuidFromValue(row[f.Name]); ok {
				idsByEntity[f.RefEntity][idStr] = id
			}
		}
	}
	names := map[string]map[string]string{}
	for entityName, idSet := range idsByEntity {
		refEntity := s.reg.GetEntity(entityName)
		if refEntity == nil || len(idSet) == 0 {
			continue
		}
		ids := make([]uuid.UUID, 0, len(idSet))
		for _, id := range idSet {
			if err := s.checkDSLRowAccess(ctx, refEntity, "read", id, nil); err != nil {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			continue
		}
		refRows, err := s.store.GetFieldsByIDs(ctx, refEntity, ids, displayField(refEntity))
		if err != nil {
			continue
		}
		names[entityName] = make(map[string]string, len(refRows))
		for idStr, row := range refRows {
			names[entityName][idStr] = s.maskedRecordLabel(ctx, refEntity, row)
		}
	}
	return names
}

func (s *Server) refFromValueCached(ctx context.Context, refEntityName string, raw any, names map[string]string) any {
	idStr, _, ok := uuidFromValue(raw)
	if !ok {
		return nil
	}
	name := idStr
	if names != nil && strings.TrimSpace(names[idStr]) != "" {
		name = names[idStr]
	}
	refEntity := s.reg.GetEntity(refEntityName)
	return &interpreter.Ref{
		UUID:    idStr,
		Name:    name,
		Type:    refEntityName,
		Manager: s.refManagerFor(refEntity, ctx),
	}
}

func uuidFromValue(raw any) (string, uuid.UUID, bool) {
	if raw == nil {
		return "", uuid.UUID{}, false
	}
	idStr := fmt.Sprint(raw)
	if up, ok := raw.(interface{ GetRefUUID() string }); ok {
		idStr = up.GetRefUUID()
	}
	idStr = strings.TrimSpace(idStr)
	if idStr == "" || idStr == "<nil>" {
		return "", uuid.UUID{}, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return "", uuid.UUID{}, false
	}
	return id.String(), id, true
}

// refFromValue строит ссылку на запись справочника/документа по UUID,
// подставляя наименование. Пустое значение → nil.
func (s *Server) refFromValue(ctx context.Context, refEntityName string, raw any) any {
	if raw == nil {
		return nil
	}
	idStr := fmt.Sprint(raw)
	if up, ok := raw.(interface{ GetRefUUID() string }); ok {
		idStr = up.GetRefUUID()
	}
	if idStr == "" || idStr == "<nil>" {
		return nil
	}
	name := idStr
	var refEntity = s.reg.GetEntity(refEntityName)
	if refEntity != nil {
		if id, err := uuid.Parse(idStr); err == nil {
			if err := s.checkDSLRowAccess(ctx, refEntity, "read", id, nil); err == nil {
				if rr, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity); err == nil && rr != nil {
					name = s.maskedRecordLabel(ctx, refEntity, rr)
				}
			}
		}
	}
	return &interpreter.Ref{
		UUID:    idStr,
		Name:    name,
		Type:    refEntityName,
		Manager: s.refManagerFor(refEntity, ctx),
	}
}

// normalizeAttrValue приводит значение реквизита к типу DSL независимо от
// бэкенда: SQLite хранит числа и булево как текст, PostgreSQL — нативно.
func normalizeAttrValue(ft metadata.FieldType, v any) any {
	if v == nil {
		return nil
	}
	switch ft {
	case metadata.FieldTypeNumber:
		switch n := v.(type) {
		case decimal.Decimal:
			return n
		case float64:
			return decimal.NewFromFloat(n)
		case int64:
			return decimal.NewFromInt(n)
		case int:
			return decimal.NewFromInt(int64(n))
		case string:
			if d, err := decimal.NewFromString(strings.TrimSpace(n)); err == nil {
				return d
			}
		}
	case metadata.FieldTypeBool:
		switch b := v.(type) {
		case bool:
			return b
		case int64:
			return b != 0
		case string:
			return b == "true" || b == "1" || strings.EqualFold(b, "да")
		}
	}
	return v
}
