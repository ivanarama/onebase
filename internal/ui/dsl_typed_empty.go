package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/typedempty"
)

// declaredDSLValue is the single UI/runtime boundary for metadata-backed
// values. It never mutates the stored object: a SQL NULL remains nil in the
// runtime map and is materialized only for the current DSL read.
func declaredDSLValue(raw any, desc typedempty.Descriptor, resolver *dslRefAttrResolver) any {
	if text, ok := raw.(string); ok && strings.TrimSpace(text) == "" && desc.RefEntity != "" {
		raw = nil
	}
	if raw == nil {
		if value, ok := typedempty.Value(desc, func(d typedempty.Descriptor) any {
			ref := &interpreter.Ref{Type: d.RefEntity}
			if resolver != nil && resolver.s != nil {
				entity := resolver.s.reg.GetEntity(d.RefEntity)
				ref.Kind = refKind(entity)
				return resolver.bindRefToContext(ref, d.RefEntity)
			}
			return ref
		}); ok {
			return value
		}
		return nil
	}
	if desc.RefEntity != "" {
		if resolver != nil {
			if ref, ok := raw.(*interpreter.Ref); ok {
				return resolver.bindRefToContext(ref, desc.RefEntity)
			}
			if resolved := resolver.s.refFromValue(resolver.liveCtx(), desc.RefEntity, raw); resolved != nil {
				if ref, ok := resolved.(*interpreter.Ref); ok {
					return resolver.bindRefToContext(ref, desc.RefEntity)
				}
				return resolved
			}
		}
		return raw
	}
	if desc.Type == metadata.FieldTypeDate {
		if value, ok := raw.(string); ok {
			if parsed, parsedOK := storage.ParseRegPeriod(value); parsedOK {
				return parsed
			}
		}
	}
	return typedempty.Normalize(desc, raw, nil)
}

// declaredRowThis exposes a metadata-backed storage row without replacing its
// raw nil values. A read therefore cannot turn SQL NULL into an explicit zero
// on a later record-set write; only Set mutates the row.
type declaredRowThis struct {
	row       map[string]any
	fields    map[string]declaredRowField
	resolver  *dslRefAttrResolver
	decisions map[string]access.FieldDecision
}

type declaredRowField struct {
	field *metadata.Field
	key   string
}

func newDeclaredRowThis(
	row map[string]any,
	fields []metadata.Field,
	resolver *dslRefAttrResolver,
	decisions map[string]access.FieldDecision,
) *declaredRowThis {
	byName := make(map[string]declaredRowField, len(fields))
	for i := range fields {
		byName[strings.ToLower(fields[i].Name)] = declaredRowField{field: &fields[i], key: fields[i].Name}
	}
	return &declaredRowThis{row: row, fields: byName, resolver: resolver, decisions: decisions}
}

// addAlias binds a public DSL spelling to an explicit storage key and type.
// It is used for register system fields only; arbitrary names never acquire a
// type by resembling Период/Регистратор/ВидДвижения.
func (r *declaredRowThis) addAlias(alias, key string, field metadata.Field) {
	if r == nil {
		return
	}
	r.fields[strings.ToLower(alias)] = declaredRowField{field: &field, key: key}
}

func (r *declaredRowThis) Get(name string) any {
	binding, declared := r.fields[strings.ToLower(name)]
	lookupName := name
	if declared {
		lookupName = binding.key
	}
	key, raw, exists := lookupMapCI(r.row, lookupName)
	_ = key
	if !declared || binding.field == nil {
		if exists {
			return raw
		}
		return nil
	}
	field := binding.field
	if decision, ok := fieldDecisionByName(r.decisions, field.Name); ok && decision.Masked() {
		if !exists || decision.Hidden() {
			return nil
		}
		return access.MaskValue(decision.Strategy, decision.Keep, raw)
	}
	desc, ok := typedempty.FromField(field)
	if !ok {
		return raw
	}
	return declaredDSLValue(raw, desc, r.resolver)
}

func (r *declaredRowThis) Set(name string, value any) {
	if r == nil || r.row == nil {
		return
	}
	if binding, ok := r.fields[strings.ToLower(name)]; ok && binding.field != nil {
		r.row[binding.key] = value
		return
	}
	r.row[name] = value
}

func (r *declaredRowThis) Fields() []string {
	result := make([]string, 0, len(r.row))
	for name := range r.row {
		result = append(result, name)
	}
	return result
}

func (s *Server) declaredEntityFieldValue(
	field *metadata.Field,
	raw any,
	resolver *dslRefAttrResolver,
) any {
	if field == nil {
		return raw
	}
	desc, ok := typedempty.FromField(field)
	if !ok {
		return raw
	}
	return declaredDSLValue(raw, desc, resolver)
}

func (s *Server) dslFieldMasked(ctx context.Context, entity *metadata.Entity, field string) bool {
	decision, ok := s.fieldDecisions(ctx, entity)[canonicalDSLField(entity, field)]
	return ok && decision.Masked()
}

func formAttributeDescriptor(attr *metadata.FormAttribute) (typedempty.Descriptor, bool) {
	if attr == nil {
		return typedempty.Descriptor{}, false
	}
	return typedempty.FromFormType(attr.TypeRef, attr.Length, attr.Precision)
}

func findScalarFormAttribute(form *metadata.FormModule, name string) *metadata.FormAttribute {
	if form == nil {
		return nil
	}
	for _, attr := range form.Attributes {
		if formAttrIsScalar(attr) && strings.EqualFold(strings.TrimSpace(attr.Name), strings.TrimSpace(name)) {
			return attr
		}
	}
	return nil
}
