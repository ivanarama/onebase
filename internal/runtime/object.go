package runtime

import (
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

type Object struct {
	Type   string
	Kind   metadata.Kind
	ID     uuid.UUID
	Fields map[string]any
}

func NewObject(entityType string, kind metadata.Kind) *Object {
	return &Object{
		Type:   entityType,
		Kind:   kind,
		ID:     uuid.New(),
		Fields: make(map[string]any),
	}
}

func (o *Object) Get(name string) any {
	return o.Fields[name]
}

func (o *Object) Set(name string, v any) {
	o.Fields[name] = v
}
