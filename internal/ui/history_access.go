package ui

// Авторизованное чтение истории объекта (план 121, раздел RBAC/маска).
//
// История — это те же значения реквизитов, только в прошедшем времени. Пока она
// читалась напрямую из журнала регистрации, она обходила две проверки, которым
// подчиняются список и форма: право на объект (построчный доступ, план 79) и
// политику полей (маска ПДн, план 88). Пользователь, которому телефон клиента
// показывают как «•••••1122», открывал историю того же клиента и видел там
// прежний и новый телефон целиком.
//
// Поэтому путь к истории один и он здесь: право на сущность → право на строку →
// редактирование значений по политике → и только потом обогащение
// (представления ссылок, форматирование дат).

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// loadAuthorizedRecordHistory читает журнал изменений объекта с применёнными
// правами и политиками. Второй результат false означает, что ответ уже
// отправлен (отказ или ошибка) и обработчику делать больше нечего.
func (s *Server) loadAuthorizedRecordHistory(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, id uuid.UUID) ([]*storage.AuditEntry, bool) {
	ctx := r.Context()
	if !s.can(r, string(entity.Kind), entity.Name, "read") {
		s.renderForbidden(w, r)
		return nil, false
	}
	// Построчный доступ проверяется по самому объекту: право видеть историю
	// строки — это право видеть строку. Объекта нет (удалён или чужой id) —
	// отказ, а не пустая история: пустая выдача сама по себе сообщает, что
	// такого объекта нет.
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		s.renderForbidden(w, r)
		return nil, false
	}
	if !s.rowAllowed(w, r, entity, "read", row) {
		return nil, false
	}
	entries, err := s.store.AuditByRecord(ctx, entity.Name, id)
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	entries, full := redactAuditEntries(s.fieldDecisions(ctx, entity), entity, entries)
	s.enrichAuditEntries(ctx, entity, entries, full)
	return entries, true
}

// redactAuditEntries применяет политику полей к записям журнала ОДНОЙ сущности.
//
// Возвращает отфильтрованные записи и набор полей, которым разрешено
// обогащение: маскированное значение нельзя ни форматировать как дату, ни
// разыменовывать как ссылку — разыменование маскированного UUID сходило бы за
// подсказку «а вот кто это был».
func redactAuditEntries(dec map[string]access.FieldDecision, entity *metadata.Entity, entries []*storage.AuditEntry) ([]*storage.AuditEntry, map[string]bool) {
	full := make(map[string]bool, len(entity.Fields))
	known := make(map[string]bool, len(entity.Fields))
	for _, f := range entity.Fields {
		known[f.Name] = true
	}
	out := entries[:0]
	for _, e := range entries {
		if e.Field == "" {
			// Событие без поля (создание, проведение, удаление) значений не несёт.
			out = append(out, e)
			continue
		}
		d, hasPolicy := dec[e.Field]
		switch {
		case hasPolicy && d.Hidden():
			// Скрытое поле не должно оставлять о себе даже строки: сам факт
			// «здесь что-то менялось» уже сообщает, что поле есть и заполнено.
			continue
		case hasPolicy && d.Masked():
			e.OldValue = access.MaskValue(d.Strategy, d.Keep, e.OldValue)
			e.NewValue = access.MaskValue(d.Strategy, d.Keep, e.NewValue)
		case !known[e.Field]:
			// Реквизита в метаданных больше нет: политику применить не к чему,
			// а значение могло быть чувствительным. Закрываем, но саму запись
			// оставляем — иначе из истории молча исчезал бы кусок прошлого.
			e.OldValue = access.MaskValue(access.FieldMaskAll, 0, e.OldValue)
			e.NewValue = access.MaskValue(access.FieldMaskAll, 0, e.NewValue)
		default:
			full[e.Field] = true
		}
		out = append(out, e)
	}
	return out, full
}

// redactAuditEntriesGlobal — то же для журнала регистрации, где записи
// принадлежат разным сущностям. Возвращает набор ключей «сущность|поле»,
// которым разрешено обогащение.
func (s *Server) redactAuditEntriesGlobal(ctx context.Context, entries []*storage.AuditEntry) ([]*storage.AuditEntry, map[string]bool) {
	full := map[string]bool{}
	decCache := map[string]map[string]access.FieldDecision{}
	out := entries[:0]
	for _, e := range entries {
		if e.Field == "" || e.EntityName == "" {
			out = append(out, e)
			continue
		}
		ent := s.reg.GetEntity(e.EntityName)
		if ent == nil {
			// Сущности больше нет — политику взять неоткуда.
			e.OldValue = access.MaskValue(access.FieldMaskAll, 0, e.OldValue)
			e.NewValue = access.MaskValue(access.FieldMaskAll, 0, e.NewValue)
			out = append(out, e)
			continue
		}
		dec, ok := decCache[e.EntityName]
		if !ok {
			dec = s.fieldDecisions(ctx, ent)
			decCache[e.EntityName] = dec
		}
		d, hasPolicy := dec[e.Field]
		switch {
		case hasPolicy && d.Hidden():
			continue
		case hasPolicy && d.Masked():
			e.OldValue = access.MaskValue(d.Strategy, d.Keep, e.OldValue)
			e.NewValue = access.MaskValue(d.Strategy, d.Keep, e.NewValue)
		default:
			full[e.EntityName+"|"+e.Field] = true
		}
		out = append(out, e)
	}
	return out, full
}
