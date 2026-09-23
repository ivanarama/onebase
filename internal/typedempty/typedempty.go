// Package typedempty owns the metadata-to-DSL empty-value table.
//
// It deliberately does not know about interpreter.Ref. Callers that have a
// registry provide a reference factory, keeping object-manager construction at
// the boundary where the target entity and execution context are available.
package typedempty

import (
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Descriptor is the immutable part of field metadata needed to materialize an
// empty DSL value. It is intentionally narrower than metadata.Field so the same
// contract can describe entity fields, form attributes and ValueTable columns.
type Descriptor struct {
	Type      metadata.FieldType
	RefEntity string
	EnumName  string
	Length    int
	Scale     int
}

// FromField copies the type-bearing part of a metadata field.
func FromField(field *metadata.Field) (Descriptor, bool) {
	if field == nil {
		return Descriptor{}, false
	}
	d := Descriptor{
		Type:      field.Type,
		RefEntity: field.RefEntity,
		EnumName:  field.EnumName,
		Length:    field.Length,
		Scale:     field.Scale,
	}
	typeRef := string(field.Type)
	lower := strings.ToLower(typeRef)
	if d.RefEntity == "" && strings.HasPrefix(lower, "reference:") {
		d.RefEntity = typeRef[len("reference:"):]
	}
	if d.EnumName == "" && strings.HasPrefix(lower, "enum:") {
		d.EnumName = typeRef[len("enum:"):]
	}
	return d, d.Type != "" || d.RefEntity != "" || d.EnumName != ""
}

// FromConstant copies the type-bearing part of a declared constant. Constants
// use the same metadata vocabulary as fields but are not metadata.Field values,
// so keeping the adapter here prevents the DSL boundary from growing a second
// empty-value table.
func FromConstant(constant *metadata.Constant) (Descriptor, bool) {
	if constant == nil {
		return Descriptor{}, false
	}
	d := Descriptor{
		Type:      constant.Type,
		RefEntity: constant.RefEntity,
		EnumName:  constant.EnumName,
		Length:    constant.Length,
		Scale:     constant.Scale,
	}
	return d, d.Type != "" || d.RefEntity != "" || d.EnumName != ""
}

// FromFormType parses the compact type spelling used by managed-form
// attributes (for example decimal(15,2), CatalogRef.Клиенты or string(40)).
func FromFormType(typeRef string, length, scale int) (Descriptor, bool) {
	typeRef = strings.TrimSpace(typeRef)
	if typeRef == "" || strings.EqualFold(typeRef, "ValueTable") {
		return Descriptor{}, false
	}
	lower := strings.ToLower(typeRef)
	d := Descriptor{Length: length, Scale: scale}
	switch {
	case strings.HasPrefix(lower, "catalogref."):
		d.RefEntity = strings.TrimSpace(typeRef[len("CatalogRef."):])
		d.Type = metadata.FieldType("reference:" + d.RefEntity)
	case strings.HasPrefix(lower, "documentref."):
		d.RefEntity = strings.TrimSpace(typeRef[len("DocumentRef."):])
		d.Type = metadata.FieldType("reference:" + d.RefEntity)
	case strings.HasPrefix(lower, "reference:"):
		d.RefEntity = strings.TrimSpace(typeRef[len("reference:"):])
		d.Type = metadata.FieldType("reference:" + d.RefEntity)
	case strings.HasPrefix(lower, "enumref."):
		d.EnumName = strings.TrimSpace(typeRef[len("EnumRef."):])
		d.Type = metadata.FieldType("enum:" + d.EnumName)
	case strings.HasPrefix(lower, "enum:"):
		d.EnumName = strings.TrimSpace(typeRef[len("enum:"):])
		d.Type = metadata.FieldType("enum:" + d.EnumName)
	case strings.HasPrefix(lower, "number"), strings.HasPrefix(lower, "decimal"):
		d.Type = metadata.FieldTypeNumber
		if parsedLength, parsedScale, ok := numberSpec(typeRef); ok {
			d.Length, d.Scale = parsedLength, parsedScale
		}
	case strings.HasPrefix(lower, "string"):
		d.Type = metadata.FieldTypeString
	case strings.HasPrefix(lower, "richtext"):
		d.Type = metadata.FieldTypeRichText
	case strings.HasPrefix(lower, "date"):
		d.Type = metadata.FieldTypeDate
	case strings.HasPrefix(lower, "bool"):
		d.Type = metadata.FieldTypeBool
	case strings.HasPrefix(lower, "image"):
		d.Type = metadata.FieldTypeImage
	default:
		return Descriptor{}, false
	}
	if d.RefEntity == "" && d.EnumName == "" && d.Type == "" {
		return Descriptor{}, false
	}
	return d, true
}

// Field converts a form descriptor into the metadata shape used by the common
// table-part row proxy. The returned value is a copy and is safe to retain.
func (d Descriptor) Field(name string) metadata.Field {
	return metadata.Field{
		Name:      name,
		Type:      d.Type,
		RefEntity: d.RefEntity,
		EnumName:  d.EnumName,
		Length:    d.Length,
		Scale:     d.Scale,
	}
}

// Value materializes the canonical empty value for a declared field. The
// factory is required only for references; it keeps this package independent
// from the DSL interpreter and its object managers.
func Value(d Descriptor, referenceFactory func(Descriptor) any) (any, bool) {
	if d.RefEntity != "" {
		if referenceFactory == nil {
			return nil, false
		}
		return referenceFactory(d), true
	}
	if d.EnumName != "" {
		return "", true
	}
	switch d.Type {
	case metadata.FieldTypeNumber:
		return decimal.Zero, true
	case metadata.FieldTypeBool:
		return false, true
	case metadata.FieldTypeString, metadata.FieldTypeRichText:
		return "", true
	case metadata.FieldTypeDate:
		return time.Time{}, true
	default:
		return nil, false
	}
}

// Normalize returns a DSL value with the declared primitive type. In
// particular, nil is materialized through Value without changing its owner.
func Normalize(d Descriptor, raw any, referenceFactory func(Descriptor) any) any {
	if text, ok := raw.(string); ok && strings.TrimSpace(text) == "" &&
		d.Type != metadata.FieldTypeString && d.Type != metadata.FieldTypeRichText && d.EnumName == "" {
		raw = nil
	}
	if raw == nil {
		if value, ok := Value(d, referenceFactory); ok {
			return value
		}
		return nil
	}
	switch d.Type {
	case metadata.FieldTypeNumber:
		switch value := raw.(type) {
		case decimal.Decimal:
			return value
		case float64:
			return decimal.NewFromFloat(value)
		case int64:
			return decimal.NewFromInt(value)
		case int:
			return decimal.NewFromInt(int64(value))
		case string:
			if parsed, err := decimal.NewFromString(strings.TrimSpace(value)); err == nil {
				return parsed
			}
		}
	case metadata.FieldTypeBool:
		switch value := raw.(type) {
		case bool:
			return value
		case int64:
			return value != 0
		case string:
			return value == "true" || value == "1" || strings.EqualFold(value, "да")
		}
	}
	return raw
}

func numberSpec(typeRef string) (length, scale int, ok bool) {
	open := strings.IndexByte(typeRef, '(')
	if open <= 0 || !strings.HasSuffix(typeRef, ")") {
		return 0, 0, false
	}
	parts := strings.Split(typeRef[open+1:len(typeRef)-1], ",")
	if len(parts) == 0 || len(parts) > 2 {
		return 0, 0, false
	}
	length, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || length <= 0 {
		return 0, 0, false
	}
	if len(parts) == 2 {
		scale, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || scale < 0 {
			return 0, 0, false
		}
	}
	return length, scale, true
}
