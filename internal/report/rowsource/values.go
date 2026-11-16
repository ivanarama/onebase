package rowsource

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/shopspring/decimal"
	"math"
	"reflect"
	"strings"
	"time"
)

type originalRow struct {
	record *interpreter.Struct
	values map[string]any
}

func inputRows(rows []map[string]any, fields []resultfilter.Field, reg *runtime.Registry, aliases map[string]string) (*interpreter.Array, []originalRow, error) {
	if len(fields) == 0 {
		return nil, nil, fmt.Errorf("row source: вход без схемы")
	}
	items := make([]any, 0, len(rows))
	baseline := make([]originalRow, 0, len(rows))
	for _, row := range rows {
		if len(row) != len(fields) {
			return nil, nil, fmt.Errorf("row source: набор входных колонок изменился")
		}
		lower := map[string]any{}
		for key, value := range row {
			key = strings.ToLower(key)
			if _, exists := lower[key]; exists {
				return nil, nil, fmt.Errorf("row source: неоднозначная входная колонка")
			}
			lower[key] = value
		}
		converted := map[string]any{}
		saved := map[string]any{}
		for _, field := range fields {
			value, present := lower[strings.ToLower(field.Name)]
			if !present {
				return nil, nil, fmt.Errorf("row source: исходная колонка отсутствует")
			}
			typed, err := typedInput(value, field, reg)
			if err != nil {
				return nil, nil, err
			}
			converted[field.Name] = typed
			saved[strings.ToLower(field.Name)] = copyValue(typed)
		}
		for alias, target := range aliases {
			if _, exists := saved[alias]; exists {
				continue
			}
			value, exists := saved[target]
			if !exists {
				return nil, nil, fmt.Errorf("row source: DSL alias без исходного поля")
			}
			converted[alias] = copyValue(value)
			saved[alias] = copyValue(value)
		}
		record := interpreter.NewStructFromMap(converted)
		items = append(items, record)
		baseline = append(baseline, originalRow{record: record, values: saved})
	}
	return interpreter.NewArray(items), baseline, nil
}

func typedInput(value any, field resultfilter.Field, reg *runtime.Registry) (any, error) {
	if value == nil {
		return nil, nil
	}
	if _, err := valueBytes(value); err != nil {
		return nil, err
	}
	if field.RefEntity != "" {
		entity := reg.GetEntity(field.RefEntity)
		if entity == nil {
			return nil, fmt.Errorf("row source: неизвестный корень ссылки")
		}
		id, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("row source: неверный тип UUID")
		}
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return nil, fmt.Errorf("row source: неверный UUID")
			}
		}
		return &interpreter.Ref{UUID: id, Name: id, Type: entity.Name, Kind: entity.Kind}, nil
	}
	if field.EnumName != "" {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("row source: неверный тип перечисления")
		}
		enum := reg.GetEnum(field.EnumName)
		if enum == nil {
			return nil, fmt.Errorf("row source: неизвестное перечисление")
		}
		if text == "" {
			return text, nil
		}
		for _, allowed := range enum.Values {
			if text == allowed {
				return text, nil
			}
		}
		return nil, fmt.Errorf("row source: неизвестное значение перечисления")
	}
	switch field.Type {
	case "string":
		if text, ok := value.(string); ok {
			return text, nil
		}
	case "number":
		if number, ok := numberValue(value); ok {
			return number, nil
		}
	case "bool":
		switch v := value.(type) {
		case bool:
			return v, nil
		case int64:
			if v == 0 || v == 1 {
				return v == 1, nil
			}
		}
	case "date", "datetime":
		if date, ok := query.ToDateValue(value); ok {
			return date, nil
		}
	}
	return nil, fmt.Errorf("row source: значение не соответствует доказанному типу")
}

func copyValue(value any) any {
	if ref, ok := value.(*interpreter.Ref); ok {
		copyRef := *ref
		return &copyRef
	}
	return value
}
func sameValue(expected, actual any) bool {
	actual = interpreter.ToHostValue(actual)
	switch v := expected.(type) {
	case *interpreter.Ref:
		got, ok := actual.(*interpreter.Ref)
		return ok && got != nil && got.UUID == v.UUID && got.Type == v.Type && got.Kind == v.Kind && got.Name == v.Name && got.Manager == nil && got.AttrResolver == nil
	case decimal.Decimal:
		got, ok := actual.(decimal.Decimal)
		return ok && v.Equal(got)
	case time.Time:
		got, ok := actual.(time.Time)
		return ok && v == got
	default:
		return reflect.DeepEqual(expected, actual)
	}
}

func outputRows(returned any, original []map[string]any, baseline []originalRow, fields []resultfilter.Field, extras []Extra) ([]map[string]any, error) {
	array, ok := returned.(*interpreter.Array)
	if !ok || array == nil || len(array.Iterate()) != len(original) {
		return nil, fmt.Errorf("row source: изменено число или тип исходных строк")
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[strings.ToLower(field.Name)] = true
	}
	for _, row := range baseline {
		for key := range row.values {
			allowed[key] = true
		}
	}
	for _, extra := range extras {
		allowed[strings.ToLower(extra.Name)] = true
	}
	out := make([]map[string]any, 0, len(original))
	bytes := 0
	for i, item := range array.Iterate() {
		record, ok := item.(*interpreter.Struct)
		if !ok || record != baseline[i].record {
			return nil, fmt.Errorf("row source: изменена identity или очередность исходных строк")
		}
		present := map[string]bool{}
		for _, key := range record.Fields() {
			key = strings.ToLower(key)
			if !allowed[key] || present[key] {
				return nil, fmt.Errorf("row source: неизвестное выходное поле")
			}
			present[key] = true
		}
		for key, value := range baseline[i].values {
			if !present[key] || !sameValue(value, record.Get(key)) {
				return nil, fmt.Errorf("row source: изменено исходное поле или его тип")
			}
		}
		// Rebuild from host originals, never from returned DSL JSON/types.
		row := make(map[string]any, len(allowed))
		for key, value := range original[i] {
			row[key] = value
		}
		for _, extra := range extras {
			if !present[strings.ToLower(extra.Name)] {
				return nil, fmt.Errorf("row source: объявленное добавленное поле отсутствует")
			}
			value, err := extraValue(record.Get(extra.Name), extra.Type)
			if err != nil {
				return nil, err
			}
			row[extra.Name] = value
		}
		for _, value := range row {
			size, err := valueBytes(value)
			if err != nil {
				return nil, err
			}
			bytes += size
			if bytes > maxBytes {
				return nil, fmt.Errorf("row source: превышен размер результата")
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func extraValue(value any, kind string) (any, error) {
	value = interpreter.ToHostValue(value)
	if value == nil {
		return nil, nil
	}
	if _, err := valueBytes(value); err != nil {
		return nil, err
	}
	switch kind {
	case "string":
		if v, ok := value.(string); ok {
			return v, nil
		}
	case "bool":
		if v, ok := value.(bool); ok {
			return v, nil
		}
	case "number":
		if _, text := value.(string); text {
			return nil, fmt.Errorf("row source: строка не является числовым output")
		}
		if v, ok := numberValue(value); ok {
			return v, nil
		}
	case "date", "datetime":
		if v, ok := value.(time.Time); ok {
			return v, nil
		}
	}
	return nil, fmt.Errorf("row source: неверный тип добавленного поля")
}
func numberValue(value any) (decimal.Decimal, bool) {
	var number decimal.Decimal
	switch v := value.(type) {
	case decimal.Decimal:
		number = v
	case int:
		number = decimal.NewFromInt(int64(v))
	case int64:
		number = decimal.NewFromInt(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return decimal.Zero, false
		}
		number = decimal.NewFromFloat(v)
	case string:
		if len(v) > 4096 {
			return decimal.Zero, false
		}
		var err error
		number, err = decimal.NewFromString(v)
		if err != nil {
			return decimal.Zero, false
		}
	default:
		return decimal.Zero, false
	}
	return number, interpreter.DecimalWithinSafeBounds(number)
}
func valueBytes(value any) (int, error) {
	size := 32
	switch v := value.(type) {
	case nil, bool, int, int64, time.Time:
	case string:
		size = len(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("row source: недопустимое число")
		}
	case decimal.Decimal:
		if !interpreter.DecimalWithinSafeBounds(v) {
			return 0, fmt.Errorf("row source: число вне безопасного диапазона")
		}
		size = len(v.String())
	default:
		return 0, fmt.Errorf("row source: вход содержит неподдержанный тип или capability")
	}
	if size > maxValueBytes {
		return 0, fmt.Errorf("row source: превышен размер значения")
	}
	return size, nil
}
