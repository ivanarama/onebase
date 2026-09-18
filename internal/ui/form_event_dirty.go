package ui

import (
	"bytes"
	"encoding/json"
	"strings"
)

// snapshotWriteState captures values, not pointers to mutable table rows. Keep
// full numeric/date precision and reference identity; display strings can make
// distinct values look equal. Lowercase assignments take precedence just as
// they do in the form-event response.
func (f *formObjectThis) snapshotWriteState() []byte {
	fields := func(src map[string]any) map[string]any {
		out := make(map[string]any, len(src))
		for key, value := range src {
			key = strings.ToLower(key)
			if assigned, ok := src[key]; ok {
				value = assigned
			}
			if ref, ok := value.(interface{ GetRefUUID() string }); ok {
				value = ref.GetRefUUID()
			}
			out[key] = value
		}
		return out
	}
	tables := make(map[string][]map[string]any, len(f.obj.TablePartRows))
	for name, rows := range f.obj.TablePartRows {
		copyRows := make([]map[string]any, len(rows))
		for i, row := range rows {
			copyRows[i] = fields(row)
		}
		tables[name] = copyRows
	}
	state := struct {
		Fields map[string]any
		Tables map[string][]map[string]any
	}{fields(f.obj.Fields), tables}
	// An unsupported DSL value must retain the warning, never break a write
	// that has already succeeded or claim that the form is fully saved.
	snapshot, _ := json.Marshal(state)
	return snapshot
}

// dirtyAfterWrite is evaluated after transaction cleanup and before database
// refreshes: those refreshes only bring persisted values into the form. A write
// followed by an in-memory assignment must still warn, even on the error path.
func (f *formObjectThis) dirtyAfterWrite() *bool {
	if f == nil || !f.saved {
		return nil
	}
	current := f.snapshotWriteState()
	dirty := f.savedState == nil || current == nil || !bytes.Equal(f.savedState, current)
	return &dirty
}
