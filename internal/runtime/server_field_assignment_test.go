package runtime

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestServerFieldAssignmentProof(t *testing.T) {
	e := &metadata.Entity{Name: "Task", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Executor", Type: metadata.FieldTypeString}, {Name: "Note", Type: metadata.FieldTypeString}}}
	makeObject := func() *Object { return &Object{Type: e.Name, Kind: e.Kind, ID: uuid.New(), Fields: map[string]any{}} }
	t.Run("copy-binding-revokes-source", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		o.MarkServerFieldAssignment("Executor")
		copy := *o
		copy.Set("Executor", "input")
		if o.TakeServerFieldAssignments().Consume(e, o.ID)["executor"] {
			t.Fatal("shared input map retained source proof")
		}
	})
	t.Run("binding-does-not-assign", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		if o.TakeServerFieldAssignments() != nil {
			t.Fatal("input minted proof")
		}
	})
	t.Run("one-save-field-scope", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		o.MarkServerFieldAssignment("EXECUTOR")
		p := o.TakeServerFieldAssignments()
		m := p.Consume(e, o.ID)
		if !m["executor"] || m["note"] {
			t.Fatal(m)
		}
		if p.Consume(e, o.ID) != nil || o.TakeServerFieldAssignments() != nil {
			t.Fatal("reused proof")
		}
	})
	t.Run("binding-overwrite-revokes", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		o.MarkServerFieldAssignment("Executor")
		o.Set("Executor", "forged")
		if m := o.TakeServerFieldAssignments().Consume(e, o.ID); m["executor"] {
			t.Fatal("binding retained proof")
		}
	})
	t.Run("copy-cannot-borrow", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		o.MarkServerFieldAssignment("Executor")
		copy := *o
		if copy.TakeServerFieldAssignments() != nil {
			t.Fatal("copy borrowed proof")
		}
		if !o.TakeServerFieldAssignments().Consume(e, o.ID)["executor"] {
			t.Fatal("copy consumed original proof")
		}
	})
	t.Run("wrong-object-consumes", func(t *testing.T) {
		o := makeObject()
		o.Set("Executor", "self")
		o.MarkServerFieldAssignment("Executor")
		p := o.TakeServerFieldAssignments()
		if p.Consume(e, uuid.New()) != nil || p.Consume(e, o.ID) != nil {
			t.Fatal("foreign or reused proof")
		}
	})
	t.Run("unknown-field-fails-closed", func(t *testing.T) {
		o := makeObject()
		o.MarkServerFieldAssignment("_event")
		if len(o.TakeServerFieldAssignments().Consume(e, o.ID)) != 0 {
			t.Fatal("unknown metadata field")
		}
	})
}
