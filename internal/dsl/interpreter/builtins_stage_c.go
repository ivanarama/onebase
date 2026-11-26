package interpreter

import (
	"fmt"
	"strings"
)

// init регистрирует глобальные функции Этапа C.
func init() {
	builtins["заполнитьзначениясвойств"] = fillPropertyValuesFn
	builtins["fillpropertyvalues"] = fillPropertyValuesFn
}

// ЗаполнитьЗначенияСвойств(Приёмник, Источник[, СписокСвойств][, ИсключаяСвойства])
// копирует одноимённые свойства из Источника в Приёмник.
//   - Источник: Структура, Соответствие или объект с Fields()+Get (например
//     результат Ссылка.ПолучитьОбъект()).
//   - Приёмник: любой объект с методом Set(имя, значение) — Структура, объект
//     справочника/документа.
//   - СписокСвойств — строка имён через запятую: копировать только их.
//   - ИсключаяСвойства — строка имён через запятую: пропустить их.
func fillPropertyValuesFn(args []any, _ string, _ int) (any, error) {
	if len(args) < 2 {
		return nil, nil
	}
	setter, ok := args[0].(interface{ Set(string, any) })
	if !ok {
		RaiseUserError("ЗаполнитьЗначенияСвойств: приёмник не поддерживает установку свойств")
	}
	names, get := sourceFields(args[1])
	if get == nil {
		RaiseUserError("ЗаполнитьЗначенияСвойств: источник должен быть Структурой, Соответствием или объектом")
	}

	// Пустой СписокСвойств трактуется как «все свойства» (как в 1С), поэтому
	// фильтр включения ставится только для непустой строки.
	var only, except map[string]bool
	if len(args) >= 3 {
		if s := strings.TrimSpace(strArg(args, 2)); s != "" {
			only = nameSet(s)
		}
	}
	if len(args) >= 4 {
		if s := strings.TrimSpace(strArg(args, 3)); s != "" {
			except = nameSet(s)
		}
	}

	for _, name := range names {
		ln := strings.ToLower(name)
		if only != nil && !only[ln] {
			continue
		}
		if except != nil && except[ln] {
			continue
		}
		setter.Set(name, get(name))
	}
	return nil, nil
}

// sourceFields возвращает список имён свойств источника и геттер значения.
// При неподдерживаемом типе get == nil.
func sourceFields(src any) (names []string, get func(string) any) {
	switch s := src.(type) {
	case *Struct:
		return s.Fields(), s.Get
	case *Map:
		s.compact()
		ns := make([]string, 0, len(s.keys))
		for _, k := range s.keys {
			ns = append(ns, fmt.Sprintf("%v", k))
		}
		return ns, func(name string) any { return s.Get(name) }
	case interface {
		Fields() []string
		Get(string) any
	}:
		return s.Fields(), s.Get
	}
	return nil, nil
}

// nameSet разбивает строку "А, Б, В" в множество имён в нижнем регистре.
func nameSet(csv string) map[string]bool {
	out := map[string]bool{}
	for _, p := range splitComma(csv) {
		out[strings.ToLower(p)] = true
	}
	return out
}
