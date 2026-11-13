package runtime

import (
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

type serverFieldAssignmentState struct {
	owner  *Object
	fields map[string]bool
}

// ServerFieldAssignments is opaque, one-attempt evidence minted by trusted DSL
// setters. It is never a field, form value, context flag or serialized token.
type ServerFieldAssignments struct {
	kind     metadata.Kind
	typ      string
	id       uuid.UUID
	fields   map[string]bool
	consumed atomic.Bool
}

// MarkServerFieldAssignment is called only after a trusted DSL setter. Ordinary
// Object.Set (including form input binding) revokes, rather than creates, proof.
func (o *Object) MarkServerFieldAssignment(name string) {
	if o.serverAssignments == nil || o.serverAssignments.owner != o {
		o.serverAssignments = &serverFieldAssignmentState{owner: o, fields: map[string]bool{}}
	}
	o.serverAssignments.fields[strings.ToLower(strings.TrimSpace(name))] = true
}

func (o *Object) ForgetServerFieldAssignment(name string) {
	// A shallow copy can still share Fields with its source. An input Set on
	// that alias must revoke the source's mark for the replaced value as well.
	if o.serverAssignments != nil {
		delete(o.serverAssignments.fields, strings.ToLower(strings.TrimSpace(name)))
	}
}

// TakeServerFieldAssignments consumes the setter journal at the Save boundary,
// including failed attempts. A shallow copy cannot borrow its source's journal.
func (o *Object) TakeServerFieldAssignments() *ServerFieldAssignments {
	if o == nil || o.serverAssignments == nil || o.serverAssignments.owner != o {
		return nil
	}
	fields := o.serverAssignments.fields
	o.serverAssignments = nil
	return &ServerFieldAssignments{kind: o.Kind, typ: o.Type, id: o.ID, fields: fields}
}

// Consume binds the one-attempt capsule to the exact logical Save target.
// No DSL or HTTP path can construct the capsule or access its fields.
func (p *ServerFieldAssignments) Consume(entity *metadata.Entity, id uuid.UUID) map[string]bool {
	if p == nil || !p.consumed.CompareAndSwap(false, true) || entity == nil || p.kind != entity.Kind || p.typ != entity.Name || p.id != id {
		return nil
	}
	fields := map[string]bool{}
	for _, f := range entity.Fields {
		name := strings.ToLower(strings.TrimSpace(f.Name))
		if p.fields[name] {
			fields[name] = true
		}
	}
	return fields
}
