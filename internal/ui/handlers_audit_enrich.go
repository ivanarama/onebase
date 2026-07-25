package ui

// Обогащение записей журнала регистрации (подмена UUID на представления)
// для страниц аудита.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// enrichAuditEntries resolves reference UUIDs and formats dates in audit
// OldValue/NewValue so that the history page shows human-readable values.
func (s *Server) enrichAuditEntries(ctx context.Context, entity *metadata.Entity, entries []*storage.AuditEntry) {
	refFields := map[string]string{}
	dateFields := map[string]bool{}
	for _, f := range entity.Fields {
		if f.RefEntity != "" {
			refFields[f.Name] = f.RefEntity
		}
		if f.Type == metadata.FieldTypeDate {
			dateFields[f.Name] = true
		}
	}
	if len(refFields) == 0 && len(dateFields) == 0 {
		return
	}
	refLabels := map[string]string{}
	for _, e := range entries {
		refEntityName, isRef := refFields[e.Field]
		if !isRef {
			continue
		}
		for _, val := range []any{e.OldValue, e.NewValue} {
			if val == nil {
				continue
			}
			idStr := extractUUIDFromAuditVal(val)
			key := refEntityName + ":" + idStr
			if _, ok := refLabels[key]; ok {
				continue
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refEntity := s.reg.GetEntity(refEntityName)
			if refEntity == nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntityName, id, refEntity)
			if err != nil {
				continue
			}
			refLabels[key] = s.maskedRecordLabel(ctx, refEntity, refRow)
		}
	}
	for _, e := range entries {
		if refEntityName, isRef := refFields[e.Field]; isRef {
			if e.OldValue != nil {
				if label, ok := refLabels[refEntityName+":"+extractUUIDFromAuditVal(e.OldValue)]; ok {
					e.OldValue = label
				}
			}
			if e.NewValue != nil {
				if label, ok := refLabels[refEntityName+":"+extractUUIDFromAuditVal(e.NewValue)]; ok {
					e.NewValue = label
				}
			}
		}
		if dateFields[e.Field] {
			e.OldValue = formatAuditDate(e.OldValue)
			e.NewValue = formatAuditDate(e.NewValue)
		}
	}
}

// enrichAuditEntriesGlobal resolves UUIDs in audit entries that span multiple entities
// (used by the global audit journal). For each entry it looks up the entity by name
// and resolves reference field UUIDs to display names.
func (s *Server) enrichAuditEntriesGlobal(ctx context.Context, entries []*storage.AuditEntry) {
	type entInfo struct {
		refFields  map[string]string
		dateFields map[string]bool
	}
	entityCache := map[string]*entInfo{}
	refLabels := map[string]string{}

	for _, e := range entries {
		if e.Field == "" || e.EntityName == "" {
			continue
		}
		info, ok := entityCache[e.EntityName]
		if !ok {
			ent := s.reg.GetEntity(e.EntityName)
			if ent == nil {
				entityCache[e.EntityName] = nil
				continue
			}
			info = &entInfo{
				refFields:  map[string]string{},
				dateFields: map[string]bool{},
			}
			for _, f := range ent.Fields {
				if f.RefEntity != "" {
					info.refFields[f.Name] = f.RefEntity
				}
				if f.Type == metadata.FieldTypeDate {
					info.dateFields[f.Name] = true
				}
			}
			entityCache[e.EntityName] = info
		}
		if info == nil {
			continue
		}
		refEntityName, isRef := info.refFields[e.Field]
		if !isRef {
			if info.dateFields[e.Field] {
				e.OldValue = formatAuditDate(e.OldValue)
				e.NewValue = formatAuditDate(e.NewValue)
			}
			continue
		}
		for _, val := range []any{e.OldValue, e.NewValue} {
			if val == nil {
				continue
			}
			idStr := extractUUIDFromAuditVal(val)
			if idStr == "" {
				continue
			}
			key := refEntityName + ":" + idStr
			if _, ok := refLabels[key]; ok {
				continue
			}
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refEntity := s.reg.GetEntity(refEntityName)
			if refEntity == nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntityName, id, refEntity)
			if err != nil {
				continue
			}
			refLabels[key] = s.maskedRecordLabel(ctx, refEntity, refRow)
		}
		if e.OldValue != nil {
			if idStr := extractUUIDFromAuditVal(e.OldValue); idStr != "" {
				if label, ok := refLabels[refEntityName+":"+idStr]; ok {
					e.OldValue = label
				}
			}
		}
		if e.NewValue != nil {
			if idStr := extractUUIDFromAuditVal(e.NewValue); idStr != "" {
				if label, ok := refLabels[refEntityName+":"+idStr]; ok {
					e.NewValue = label
				}
			}
		}
	}
}

// extractUUIDFromAuditVal extracts a UUID string from an audit value.
// Handles plain UUID strings and JSON-encoded Ref objects like {"UUID":"abc",...}.
func extractUUIDFromAuditVal(v any) string {
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprintf("%v", v)
	}
	if _, err := uuid.Parse(s); err == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) == nil {
		if uid, ok2 := m["UUID"].(string); ok2 {
			return uid
		}
	}
	return ""
}
func formatAuditDate(v any) any {
	if v == nil {
		return nil
	}
	s := fmt.Sprintf("%v", v)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("02.01.2006")
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("02.01.2006")
	}
	return v
}
