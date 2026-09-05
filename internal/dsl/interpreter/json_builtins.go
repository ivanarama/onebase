package interpreter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/shopspring/decimal"
)

func builtinReadJSON(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		panic(userError{Msg: "ПрочитатьJSON: ожидается 1 аргумент"})
	}
	text := fmt.Sprintf("%v", args[0])
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		panic(userError{Msg: "ПрочитатьJSON: " + err.Error()})
	}
	return jsonToValue(raw), nil
}

// JSONValueToDSL рекурсивно превращает разобранное JSON-значение (map[string]any/
// []any/скаляры из encoding/json) в DSL-значение (*Map/*Array/скаляры) — то же,
// что получает DSL из ПрочитатьJSON. Экспортировано для приёмки (план 90): конверт
// события отдаётся обработчику как привычный *Map.
func JSONValueToDSL(v any) any { return jsonToValue(v) }

func jsonToValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := &Map{}
		for k, val := range x {
			m.CallMethod("вставить", []any{k, jsonToValue(val)})
		}
		return m
	case []any:
		a := &Array{}
		for _, item := range x {
			a.items = append(a.items, jsonToValue(item))
		}
		return a
	case float64:
		// json.Unmarshal returns all numbers as float64
		if x == math.Trunc(x) && !math.IsInf(x, 0) && !math.IsNaN(x) {
			return int64(x)
		}
		return x
	case bool, string:
		return x
	default:
		return nil // null → Неопределено
	}
}

func builtinWriteJSON(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return "null", nil
	}
	data, err := json.Marshal(valueToJSON(args[0]))
	if err != nil {
		panic(userError{Msg: "ЗаписатьJSON: " + err.Error()})
	}
	return string(data), nil
}

// valueToJSON рекурсивно конвертирует DSL-значение в JSON-совместимый тип.
func valueToJSON(v any) any {
	return valueToJSONSeen(v, make(map[jsonValueVisit]bool), 0)
}

type jsonValueVisit struct {
	kind byte
	ptr  uintptr
	len  int
}

type jsonValueError string

func (e jsonValueError) MarshalJSON() ([]byte, error) {
	return nil, errors.New(string(e))
}

const maxJSONValueDepth = 1024

func valueToJSONSeen(v any, stack map[jsonValueVisit]bool, depth int) any {
	if depth > maxJSONValueDepth {
		return jsonValueError("JSON value exceeds maximum nesting depth")
	}
	switch x := v.(type) {
	case *Map:
		if x == nil {
			return nil
		}
		visit := jsonValueVisit{kind: 'm', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return jsonValueError("JSON value contains a cycle")
		}
		stack[visit] = true
		defer delete(stack, visit)
		obj := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			obj[fmt.Sprintf("%v", k)] = valueToJSONSeen(x.vals[i], stack, depth+1)
		}
		return obj
	case *Struct:
		if x == nil {
			return nil
		}
		visit := jsonValueVisit{kind: 't', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return jsonValueError("JSON value contains a cycle")
		}
		stack[visit] = true
		defer delete(stack, visit)
		obj := make(map[string]any, len(x.keys))
		for _, k := range x.keys {
			obj[k] = valueToJSONSeen(x.vals[k], stack, depth+1)
		}
		return obj
	case *Array:
		if x == nil {
			return nil
		}
		visit := jsonValueVisit{kind: 'a', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return jsonValueError("JSON value contains a cycle")
		}
		stack[visit] = true
		defer delete(stack, visit)
		items := make([]any, len(x.items))
		for i, item := range x.items {
			items[i] = valueToJSONSeen(item, stack, depth+1)
		}
		return items
	case []any:
		if x == nil {
			return nil
		}
		visit := jsonValueVisit{kind: 's', ptr: reflect.ValueOf(x).Pointer(), len: len(x)}
		if stack[visit] {
			return jsonValueError("JSON value contains a cycle")
		}
		stack[visit] = true
		defer delete(stack, visit)
		items := make([]any, len(x))
		for i, item := range x {
			items[i] = valueToJSONSeen(item, stack, depth+1)
		}
		return items
	case map[string]any:
		if x == nil {
			return nil
		}
		visit := jsonValueVisit{kind: 'r', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return jsonValueError("JSON value contains a cycle")
		}
		stack[visit] = true
		defer delete(stack, visit)
		obj := make(map[string]any, len(x))
		for key, item := range x {
			obj[key] = valueToJSONSeen(item, stack, depth+1)
		}
		return obj
	case *readOnlyThis:
		// Struct is the one native This value with an established WriteJSON
		// representation. Its read-only snapshot is safe to unwrap here; every
		// nested value was protected while the snapshot was built. Arbitrary
		// host This implementations remain opaque.
		if x != nil {
			if data, ok := x.inner.(*Struct); ok {
				return valueToJSONSeen(data, stack, depth)
			}
			return &readOnlyOpaque{ec: x.ec}
		}
		return nil
	case *Ref:
		// Ссылка в JSON — её идентификатор, а не разложенная по полям структура.
		// Без этой ветки json.Marshal вываливал наружу устройство *Ref
		// (`{"UUID":…,"Name":…,"Manager":null}`), включая поля, которых в DSL нет
		// вовсе. Заодно это делает обёртку ссылочных колонок запроса (#1150)
		// незаметной для интеграций: колонка отдавала UUID строкой и отдаёт её же.
		if x == nil {
			return nil
		}
		return x.UUID
	case decimal.Decimal:
		// json.Number маршалится как число без кавычек. По умолчанию shopspring
		// сериализует decimal строкой ("30"), что ломает совместимость с JSON-числами.
		return json.Number(x.String())
	default:
		return v // string, float64, int64, bool, nil — маршалятся напрямую
	}
}
