package interpreter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/shopspring/decimal"
)

// CopyFormNotificationValues makes a detached data graph for an asynchronous
// form notification. One traversal covers parameter and source together, so
// aliases and cycles survive within a delivery, but no mutable data or host
// capabilities are shared with the sender or another delivery. bindRef, when
// supplied, attaches the recipient's live managers to otherwise inert refs.
// Stateful host objects cannot cross this boundary.
func CopyFormNotificationValues(bindRef func(*Ref) *Ref, values ...any) (out []any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(userError); ok {
				out = nil
				err = failure.asDSLError("", 0)
			} else {
				panic(recovered)
			}
		}
	}()
	c := notificationCopy{seen: make(map[notificationVisit]any), bindRef: bindRef}
	out = make([]any, len(values))
	for index, value := range values {
		out[index] = c.copy(value, 0)
	}
	return out, nil
}

type notificationVisit struct {
	typ reflect.Type
	ptr uintptr
	len int
}

type notificationCopy struct {
	seen    map[notificationVisit]any
	nodes   int
	bindRef func(*Ref) *Ref
}

func (c *notificationCopy) allowChildren(count int) {
	if count > 10000-c.nodes {
		RaiseUserError("Оповестить: параметр или источник превышает предел копирования")
	}
}

func (c *notificationCopy) copy(value any, depth int) any {
	c.nodes++
	if depth > 64 || c.nodes > 10000 {
		RaiseUserError("Оповестить: параметр или источник превышает предел копирования")
	}
	if IsUndefined(value) {
		return nil
	}
	switch v := value.(type) {
	case bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64, time.Time, time.Duration, decimal.Decimal, json.Number:
		return value
	case []byte:
		return append([]byte(nil), v...)
	}
	rv := reflect.ValueOf(value)
	visit := notificationVisit{typ: rv.Type()}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if rv.IsNil() {
			return nil
		}
		visit.ptr = rv.Pointer()
		if rv.Kind() == reflect.Slice {
			visit.len = rv.Len()
		}
	default:
		RaiseUserError(fmt.Sprintf("Оповестить: значение типа %T нельзя передать другой форме", value))
	}
	if cloned, ok := c.seen[visit]; ok {
		return cloned
	}
	switch v := value.(type) {
	case *Ref:
		out := &Ref{UUID: v.UUID, Name: v.Name, Type: v.Type, Kind: v.Kind}
		if c.bindRef != nil {
			out = c.bindRef(out)
		}
		c.seen[visit] = out
		return out
	case *Struct:
		c.allowChildren(len(v.vals))
		out := &Struct{keys: append([]string(nil), v.keys...), vals: make(map[string]any)}
		c.seen[visit] = out
		for key, item := range v.vals {
			out.vals[key] = c.copy(item, depth+1)
		}
		return out
	case *Array:
		c.allowChildren(len(v.items))
		out := &Array{}
		c.seen[visit] = out
		for _, item := range v.items {
			out.items = append(out.items, c.copy(item, depth+1))
		}
		return out
	case *Map:
		c.allowChildren(len(v.keys))
		c.allowChildren(len(v.vals))
		out := &Map{}
		c.seen[visit] = out
		for index, key := range v.keys {
			out.keys = append(out.keys, c.copy(key, depth+1))
			out.vals = append(out.vals, c.copy(v.vals[index], depth+1))
		}
		return out
	case *ValueTable:
		c.allowChildren(len(v.columns))
		c.allowChildren(len(v.rows))
		out := &ValueTable{columns: append([]string(nil), v.columns...)}
		c.seen[visit] = out
		for _, row := range v.rows {
			copied, _ := c.copy(row, depth+1).(map[string]any)
			out.rows = append(out.rows, copied)
		}
		return out
	case *MapThis:
		// Keep row data, never the sender's Read/Write callbacks.
		out := &MapThis{}
		c.seen[visit] = out
		out.M, _ = c.copy(v.M, depth+1).(map[string]any)
		return out
	case map[string]any:
		c.allowChildren(len(v))
		out := make(map[string]any)
		c.seen[visit] = out
		for key, item := range v {
			out[key] = c.copy(item, depth+1)
		}
		return out
	case []any:
		c.allowChildren(len(v))
		out := make([]any, len(v))
		c.seen[visit] = out
		for index, item := range v {
			out[index] = c.copy(item, depth+1)
		}
		return out
	default:
		RaiseUserError(fmt.Sprintf("Оповестить: значение типа %T нельзя передать другой форме", value))
		return nil
	}
}
