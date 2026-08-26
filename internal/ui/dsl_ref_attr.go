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
	s   *Server
	ctx context.Context
	// ctxSrc — «живой» контекст DSL-исполнения (TxState). Менеджеры ссылок
	// строятся ДО того, как модуль откроет НачатьТранзакцию, и со статическим
	// контекстом их ПолучитьОбъект()/Записать() уходили за ВТОРЫМ соединением.
	// Пул SQLite — одно соединение, занятое транзакцией, поэтому такой вызов
	// висел до таймаута. nil — остаётся прежний статический контекст.
	ctxSrc docsCtxSource
	cache  map[string]map[string]map[string]any // entity -> uuid -> field name -> value
}

// Ctx реализует interpreter.CtxSource: менеджеры ссылок спрашивают контекст
// на КАЖДОМ вызове, поэтому ПолучитьОбъект() попадает в транзакцию, открытую
// уже после создания менеджера.
func (r *dslRefAttrResolver) Ctx() context.Context { return r.liveCtx() }

// liveCtx возвращает контекст с открытой DSL-транзакцией, если она есть.
func (r *dslRefAttrResolver) liveCtx() context.Context {
	if r == nil {
		return context.Background()
	}
	if r.ctxSrc != nil {
		if ctx := r.ctxSrc.Ctx(); ctx != nil {
			return ctx
		}
	}
	return r.ctx
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
	return s.newFormObjectThisLive(ctx, nil, obj, entity, form, false)
}

// newFormObjectThisLive помечает объект как ещё не записанный (чтобы
// Объект.Записать() создал запись, а не пытался обновить несуществующую) и
// принимает «живой» источник контекста: без него ссылки объекта не видят
// открытую модулем DSL-транзакцию (см. dslRefAttrResolver.ctxSrc).
func (s *Server) newFormObjectThisLive(ctx context.Context, ctxSrc docsCtxSource, obj *runtime.Object, entity *metadata.Entity, form *metadata.FormModule, isNew bool) *formObjectThis {
	var resolver *dslRefAttrResolver
	if s != nil && s.store != nil && s.reg != nil {
		resolver = s.newDSLRefAttrResolver(ctx)
		resolver.ctxSrc = ctxSrc
		resolver.attachObject(entity, obj)
	}
	return &formObjectThis{obj: obj, entity: entity, form: form, refResolver: resolver,
		srv: s, ctx: ctx, ctxSrc: ctxSrc, isNew: isNew}
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
	// План 88E: разыменование чужой записи (this.Клиент.Телефон) — такой же путь
	// чтения, как форма или REST, и обязано подчиняться полевой политике роли.
	return r.s.maskDSLValue(r.ctx, entity, fd.Name, row[fd.Name]), true
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
	if ref.Kind == "" && r.s != nil {
		ref.Kind = refKind(r.s.reg.GetEntity(ref.Type))
	}
	if ref.Manager == nil && r.s != nil {
		ref.Manager = r.s.refManagerForSrc(r.s.reg.GetEntity(ref.Type), r, r.liveCtx())
	}
	ref.AttrResolver = r
	return ref
}

// bindRefToContext returns a hook-local copy whose manager uses the resolver's
// context. References stored in runtime.Object may have been enriched before
// entityservice opened its transaction, so reusing their manager from OnPost
// would make ПолучитьОбъект()/Записать() use a second connection and deadlock
// against the transaction currently running the hook. A copy keeps the object
// value reusable after the hook instead of leaving it bound to a completed tx.
func (r *dslRefAttrResolver) bindRefToContext(ref *interpreter.Ref, entityName string) *interpreter.Ref {
	if r == nil || ref == nil {
		return ref
	}
	bound := *ref
	if bound.Type == "" {
		bound.Type = entityName
	}
	if bound.Kind == "" && r.s != nil {
		bound.Kind = refKind(r.s.reg.GetEntity(bound.Type))
	}
	if r.s != nil {
		bound.Manager = r.s.refManagerForSrc(r.s.reg.GetEntity(bound.Type), r, r.liveCtx())
	}
	bound.AttrResolver = r
	return &bound
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
	// Только uniqueObjectFields(entity.Fields): displayField возвращает поле,
	// уже входящее в entity.Fields (дедуп его выкидывал), а append к слайсу
	// реестра при cap>len писал в разделяемый backing array метаданных из
	// конкурентных запросов (гонка под -race).
	fields := uniqueObjectFields(entity.Fields)
	rows, err := r.s.store.GetFieldsByIDs(r.liveCtx(), entity, missing, fields)
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
	refNames := r.s.bulkReferenceNames(r.liveCtx(), rows, entity.Fields)
	for idStr, row := range rows {
		vals := make(map[string]any, len(entity.Fields))
		for _, fd := range entity.Fields {
			raw := row[fd.Name]
			if fd.RefEntity != "" {
				vals[fd.Name] = r.s.refFromValueCached(r.liveCtx(), fd.RefEntity, raw, refNames[fd.RefEntity])
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
	for k, v := range m {
		if strings.EqualFold(k, name) {
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
	if tp == nil {
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
	for k := range m.row {
		if strings.EqualFold(k, name) {
			m.row[k] = v
			return
		}
	}
	// Новую колонку пишем с каноническим именем метаданных. Это важно и без
	// ref-resolver (например, для ValueTable): обычный MapThis нормализует новый
	// ключ в lowercase, а browser response адресует колонки точными именами.
	if fd := tablePartField(m.tp, name); fd != nil {
		m.row[fd.Name] = v
		return
	}
	m.row[name] = v
}

func (m *refAwareMapThis) wrapValue(name string, v any) any {
	fd := tablePartField(m.tp, name)
	if fd == nil || fd.RefEntity == "" {
		return v
	}
	if m.resolver == nil {
		return v
	}
	if ref, ok := v.(*interpreter.Ref); ok {
		return m.resolver.bindRefToContext(ref, fd.RefEntity)
	}
	idStr, _, ok := uuidFromValue(v)
	if !ok {
		return v
	}
	return m.resolver.attachRef(&interpreter.Ref{
		UUID:    idStr,
		Name:    idStr,
		Type:    fd.RefEntity,
		Kind:    refKind(m.resolver.s.reg.GetEntity(fd.RefEntity)),
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
