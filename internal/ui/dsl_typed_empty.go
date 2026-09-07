package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
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
	return typedempty.Normalize(desc, raw, nil)
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
