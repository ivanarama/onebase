package interpreter

// Конвертация значений DSL в данные для html/template (план 125).
//
// Существующий valueToJSONSeen (json_builtins.go) для этого не годится: он
// приводит даты и decimal к JSON-представлению, а шаблону нужны родные типы —
// иначе функции «дата» и «число» получат строку и не смогут форматировать.
// Общей осталась защита от циклов и глубины: структура, ссылающаяся на себя,
// не должна вешать рендер.

import (
	"fmt"
	"reflect"
	"strings"
)

// maxTemplateDataDepth — предел вложенности конвертируемых данных.
const maxTemplateDataDepth = 32

// dslValueToTemplateData переводит значение DSL в структуру, понятную
// html/template: Массив → []any, Структура/Соответствие → map[string]any с
// ключами в НИЖНЕМ регистре (имена полей в шаблоне приводятся к нему же, см.
// lowerFieldNames). Примитивы — как есть, включая time.Time и decimal.
func dslValueToTemplateData(v any) any {
	return dslValueToTemplateDataSeen(v, make(map[templateDataVisit]bool), 0)
}

type templateDataVisit struct {
	kind byte
	ptr  uintptr
}

func dslValueToTemplateDataSeen(v any, stack map[templateDataVisit]bool, depth int) any {
	if depth > maxTemplateDataDepth {
		return nil
	}
	switch x := v.(type) {
	case nil:
		return nil
	case *Struct:
		if x == nil {
			return nil
		}
		visit := templateDataVisit{kind: 't', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return nil // цикл — обрываем ветку, а не рендер целиком
		}
		stack[visit] = true
		defer delete(stack, visit)
		obj := make(map[string]any, len(x.keys))
		for _, k := range x.keys {
			obj[strings.ToLower(k)] = dslValueToTemplateDataSeen(x.vals[k], stack, depth+1)
		}
		return obj
	case *Map:
		if x == nil {
			return nil
		}
		visit := templateDataVisit{kind: 'm', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return nil
		}
		stack[visit] = true
		defer delete(stack, visit)
		obj := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			// Ключи Соответствия произвольные; в шаблоне обращение идёт по
			// имени, поэтому приводим к строке и нижнему регистру. Побочный
			// эффект: ключи «Год» и «год» схлопываются в один.
			obj[strings.ToLower(fmt.Sprintf("%v", k))] = dslValueToTemplateDataSeen(x.vals[i], stack, depth+1)
		}
		return obj
	case *Array:
		if x == nil {
			return nil
		}
		visit := templateDataVisit{kind: 'a', ptr: reflect.ValueOf(x).Pointer()}
		if stack[visit] {
			return nil
		}
		stack[visit] = true
		defer delete(stack, visit)
		items := make([]any, len(x.items))
		for i, item := range x.items {
			items[i] = dslValueToTemplateDataSeen(item, stack, depth+1)
		}
		return items
	case []any:
		items := make([]any, len(x))
		for i, item := range x {
			items[i] = dslValueToTemplateDataSeen(item, stack, depth+1)
		}
		return items
	case map[string]any:
		obj := make(map[string]any, len(x))
		for k, val := range x {
			obj[strings.ToLower(k)] = dslValueToTemplateDataSeen(val, stack, depth+1)
		}
		return obj
	default:
		// Строки, числа (decimal), булево, даты, ссылки и прочие объекты DSL
		// уходят как есть: html/template напечатает их через String()/Stringer,
		// а функции «дата»/«число» получат родной тип.
		return v
	}
}
