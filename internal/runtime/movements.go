package runtime

import (
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// MovementsCollector accumulates register movement records written by DSL during OnWrite.
// Accessible in DSL as the "Движения" variable.
type MovementsCollector struct {
	DocType string
	DocID   uuid.UUID
	Period  *time.Time // auto-filled from document's first date field
	pending map[string][]map[string]any
}

func NewMovementsCollector(docType string, docID uuid.UUID) *MovementsCollector {
	return &MovementsCollector{
		DocType: docType,
		DocID:   docID,
		pending: make(map[string][]map[string]any),
	}
}

func (mc *MovementsCollector) SetPeriod(t time.Time) {
	mc.Period = &t
}

// Get implements interpreter.This — Движения.НазваниеРегистра returns a RegisterMovements.
func (mc *MovementsCollector) Get(name string) any {
	return &RegisterMovements{collector: mc, name: name}
}

func (mc *MovementsCollector) Set(name string, v any) {}

// All returns all pending movements keyed by register name.
func (mc *MovementsCollector) All() map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(mc.pending))
	for k, v := range mc.pending {
		out[k] = v
	}
	return out
}

// RegisterMovements is the per-register movements list returned by Движения.НазваниеРегистра.
type RegisterMovements struct {
	collector *MovementsCollector
	name      string
}

// Get implements interpreter.This (allows member access, though unused directly).
func (rm *RegisterMovements) Get(name string) any { return nil }
func (rm *RegisterMovements) Set(name string, v any) {}

// CallMethod implements interpreter.MethodCallable.
func (rm *RegisterMovements) CallMethod(method string, args []any) any {
	switch method {
	case "Добавить", "Add":
		row := make(map[string]any)
		rm.collector.pending[rm.name] = append(rm.collector.pending[rm.name], row)
		return &interpreter.MapThis{M: row}
	case "Очистить", "Clear":
		rm.collector.pending[rm.name] = nil
		return nil
	}
	return nil
}
