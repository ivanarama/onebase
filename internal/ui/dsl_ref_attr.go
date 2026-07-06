package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

type dslRefAttrResolver struct {
	s     *Server
	ctx   context.Context
	cache map[string]map[string]map[string]any // entity -> uuid -> field name -> value
}

func (s *Server) newDSLRefAttrResolver(ctx context.Context) *dslRefAttrResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return &dslRefAttrResolver{
		s:     s,
		ctx:   ctx,
		cache: map[string]map[string]map[string]any{},
	}
}

func (s *Server) newFormObjectThis(ctx context.Context, obj *runtime.Object, entity *metadata.Entity, form *metadata.FormModule) *formObjectThis {
	var resolver *dslRefAttrResolver
	if s != nil && s.store != nil && s.reg != nil {
		resolver = s.newDSLRefAttrResolver(ctx)
		resolver.attachObject(entity, obj)
	}
	return &formObjectThis{obj: obj, entity: entity, form: form, refResolver: resolver}
}

func (r *dslRefAttrResolver) ResolveRefAttr(ref *interpreter.Ref, field string) (any, bool) {
	if r == nil || r.s == nil || ref == nil || strings.TrimSpace(ref.UUID) == "" || strings.TrimSpace(ref.Type) == "" {
		return nil, false
	}
	entity := r.s.reg.GetEntity(ref.Type)
	if entity == nil {
		return nil, false
	}
	fd := findObjectAttributeField(entity, field)
	if fd == nil {
		return nil, false
	}
	idStr, id, ok := uuidFromValue(ref.UUID)
	if !ok {
		return nil, true
	}
	if !r.hasRow(entity.Name, idStr) {
		if err := r.preloadIDs(entity, []uuid.UUID{id}); err != nil {
			interpreter.RaiseUserError("Чтение реквизита " + entity.Name + "." + fd.Name + ": " + err.Error())
		}
	}
	row := r.cache[entity.Name][idStr]
	if row == nil {
		return nil, true
	}
	return row[fd.Name], true
}

func (r *dslRefAttrResolver) attachObject(entity *metadata.Entity, obj *runtime.Object) {
	if r == nil || entity == nil || obj == nil {
		return
	}
	batch := map[string]map[string]uuid.UUID{}
	r.collectRefValue(batch, entity.Name, obj.Get("Ссылка"))
	r.collectRefValue(batch, entity.Name, obj.Get("Reference"))

	for _, fd := range entity.Fields {
		if fd.RefEntity == "" {
			continue
		}
		if _, v, ok := lookupMapCI(obj.Fields, fd.Name); ok {
			r.collectRefValue(batch, fd.RefEntity, v)
		}
	}
	for _, tp := range entity.TableParts {
		rows := obj.TablePartRows[tp.Name]
		for _, fd := range tp.Fields {
			if fd.RefEntity == "" {
				continue
			}
			for _, row := range rows {
				if _, v, ok := lookupMapCI(row, fd.Name); ok {
					r.collectRefValue(batch, fd.RefEntity, v)
				}
			}
		}
	}
	r.preloadBatch(batch)
}

func (r *dslRefAttrResolver) collectRefValue(batch map[string]map[string]uuid.UUID, entityName string, raw any) {
	entityName = strings.TrimSpace(entityName)
	if entityName == "" {
		return
	}
	if ref, ok := raw.(*interpreter.Ref); ok {
		r.attachRef(ref, entityName)
	}
	idStr, id, ok := uuidFromValue(raw)
	if !ok {
		return
	}
	if batch[entityName] == nil {
		batch[entityName] = map[string]uuid.UUID{}
	}
	batch[entityName][idStr] = id
}

func (r *dslRefAttrResolver) attachRef(ref *interpreter.Ref, entityName string) *interpreter.Ref {
	if r == nil || ref == nil {
		return ref
	}
	if ref.Type == "" {
		ref.Type = entityName
	}
	if ref.Manager == nil && r.s != nil {
		ref.Manager = r.s.refManagerFor(r.s.reg.GetEntity(ref.Type), r.ctx)
	}
	ref.AttrResolver = r
	return ref
}

func (r *dslRefAttrResolver) preloadBatch(batch map[string]map[string]uuid.UUID) {
	for entityName, idsByString := range batch {
		entity := r.s.reg.GetEntity(entityName)
		if entity == nil || len(idsByString) == 0 {
			continue
		}
		ids := make([]uuid.UUID, 0, len(idsByString))
		for _, id := range idsByString {
			ids = append(ids, id)
		}
		if err := r.preloadIDs(entity, ids); err != nil {
			interpreter.RaiseUserError("Предзагрузка реквизитов " + entity.Name + ": " + err.Error())
		}
	}
}

func (r *dslRefAttrResolver) preloadIDs(entity *metadata.Entity, ids []uuid.UUID) error {
	if r == nil || r.s == nil || entity == nil || len(ids) == 0 {
		return nil
	}
	missing := make([]uuid.UUID, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		idStr := id.String()
		if seen[idStr] || r.hasRow(entity.Name, idStr) {
			continue
		}
		seen[idStr] = true
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return nil
	}
	fields := uniqueObjectFields(append(entity.Fields, displayField(entity)...))
	rows, err := r.s.store.GetFieldsByIDs(r.ctx, entity, missing, fields)
	if err != nil {
		return fmt.Errorf("%s: %w", entity.Name, err)
	}
	r.cacheRows(entity, rows)
	return nil
}

func (r *dslRefAttrResolver) cacheRows(entity *metadata.Entity, rows map[string]map[string]any) {
	if r.cache[entity.Name] == nil {
		r.cache[entity.Name] = map[string]map[string]any{}
	}
	refNames := r.s.bulkReferenceNames(r.ctx, rows, entity.Fields)
	for idStr, row := range rows {
		vals := make(map[string]any, len(entity.Fields))
		for _, fd := range entity.Fields {
			raw := row[fd.Name]
			if fd.RefEntity != "" {
				vals[fd.Name] = r.s.refFromValueCached(r.ctx, fd.RefEntity, raw, refNames[fd.RefEntity])
			} else {
				vals[fd.Name] = normalizeAttrValue(fd.Type, raw)
			}
		}
		r.cache[entity.Name][idStr] = vals
	}
}

func (r *dslRefAttrResolver) hasRow(entityName, idStr string) bool {
	return r != nil && r.cache[entityName] != nil && r.cache[entityName][idStr] != nil
}

func lookupMapCI(m map[string]any, name string) (string, any, bool) {
	low := strings.ToLower(name)
	for k, v := range m {
		if strings.ToLower(k) == low {
			return k, v, true
		}
	}
	return "", nil, false
}

type refAwareMapThis struct {
	row      map[string]any
	tp       *metadata.TablePart
	resolver *dslRefAttrResolver
}

func newRefAwareMapThis(row map[string]any, tp *metadata.TablePart, resolver *dslRefAttrResolver) interpreter.This {
	if resolver == nil || tp == nil {
		return &interpreter.MapThis{M: row}
	}
	return &refAwareMapThis{row: row, tp: tp, resolver: resolver}
}

func (m *refAwareMapThis) Get(name string) any {
	if m == nil {
		return nil
	}
	_, v, ok := lookupMapCI(m.row, name)
	if !ok {
		return nil
	}
	return m.wrapValue(name, v)
}

func (m *refAwareMapThis) Set(name string, v any) {
	if m == nil {
		return
	}
	low := strings.ToLower(name)
	for k := range m.row {
		if strings.ToLower(k) == low {
			m.row[k] = v
			return
		}
	}
	m.row[low] = v
}

func (m *refAwareMapThis) wrapValue(name string, v any) any {
	fd := tablePartField(m.tp, name)
	if fd == nil || fd.RefEntity == "" {
		return v
	}
	if ref, ok := v.(*interpreter.Ref); ok {
		return m.resolver.attachRef(ref, fd.RefEntity)
	}
	idStr, _, ok := uuidFromValue(v)
	if !ok {
		return v
	}
	return m.resolver.attachRef(&interpreter.Ref{
		UUID:    idStr,
		Name:    idStr,
		Type:    fd.RefEntity,
		Manager: m.resolver.s.refManagerFor(m.resolver.s.reg.GetEntity(fd.RefEntity), m.resolver.ctx),
	}, fd.RefEntity)
}

func tablePartField(tp *metadata.TablePart, name string) *metadata.Field {
	if tp == nil {
		return nil
	}
	for i := range tp.Fields {
		if strings.EqualFold(tp.Fields[i].Name, name) {
			return &tp.Fields[i]
		}
	}
	return nil
}
