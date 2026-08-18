package ui

import (
	"fmt"
	"sort"
	"strings"
)

// Редактор колонки табличной части определяется ТИПОМ поля, а не «всё, что не
// ссылка и не число — строка» (#1010). Для enum это был обычный текстовый
// input: значение набиралось руками, опечатка молча ломала прикладную логику
// (`Стр.Вид = "Телефон"`), а список допустимых значений негде было увидеть.
// Для bool текстовый input ещё и терял данные: сервер отдавал в value то, что
// лежит в БД (SQLite — «1»), а разбор сохранения признаёт истиной только
// строку «true» — то есть простое пересохранение формы снимало флажок.
//
// Здесь — общие хелперы шаблонов для обоих рендеров (автоформа и DOM-таблица
// managed-формы). Данные берутся из уже готовых TPEnumLabels/TPEnumOrder
// контекста формы, чтобы не заводить третью карту и не трогать все хендлеры.

// tpEnumOption — один пункт <select> enum-колонки табличной части.
type tpEnumOption struct {
	Value    string
	Label    string
	Selected bool
	// Unknown — значение лежит в базе, но в перечислении его нет (перечисление
	// поменяли уже после записи). Такой пункт всё равно попадает в список:
	// без него браузер выбрал бы первый вариант, и открытие формы молча
	// подменило бы данные. Шаблон помечает его предупреждением.
	Unknown bool
}

// tpEnumOptions собирает варианты enum-колонки табличной части в порядке
// объявления values: перечисления. labels — TPEnumLabels (tp → поле → значение
// → подпись), order — TPEnumOrder (tp → поле → значения по порядку). Оба
// приходят из контекста шаблона как any: часть тестов подставляет туда
// map[string]any, и жёсткая сигнатура роняла бы рендер вместо пустого списка.
func tpEnumOptions(labels, order any, tpName, field string, current any) []tpEnumOption {
	labelMap := tpEnumLabelMap(labels, tpName, field)
	values := tpEnumValueOrder(order, tpName, field)
	if len(values) == 0 {
		values = make([]string, 0, len(labelMap))
		for v := range labelMap {
			values = append(values, v)
		}
		// Порядок карты в Go случаен: без сортировки список вариантов
		// перетасовывался бы при каждом рендере одной и той же формы.
		sort.Strings(values)
	}
	cur := tpCellString(current)
	out := make([]tpEnumOption, 0, len(values)+1)
	found := false
	for _, v := range values {
		label := v
		if l, ok := labelMap[v]; ok && l != "" {
			label = l
		}
		selected := v == cur
		if selected {
			found = true
		}
		out = append(out, tpEnumOption{Value: v, Label: label, Selected: selected})
	}
	if cur != "" && !found {
		out = append(out, tpEnumOption{Value: cur, Label: cur, Selected: true, Unknown: true})
	}
	return out
}

func tpEnumLabelMap(labels any, tpName, field string) map[string]string {
	switch typed := labels.(type) {
	case map[string]map[string]map[string]string:
		return typed[tpName][field]
	case map[string]any:
		byField, _ := typed[tpName].(map[string]map[string]string)
		if byField != nil {
			return byField[field]
		}
	}
	return nil
}

func tpEnumValueOrder(order any, tpName, field string) []string {
	switch typed := order.(type) {
	case map[string]map[string][]string:
		return typed[tpName][field]
	case map[string]any:
		byField, _ := typed[tpName].(map[string][]string)
		if byField != nil {
			return byField[field]
		}
	}
	return nil
}

// truthyCell — взведён ли флажок булевой колонки. Значение приезжает
// разнотипным: bool (PostgreSQL), int64/float64 1|0 (SQLite и JSON строк ТЧ),
// строка "true"/"1" (перерисовка формы после неудачного сохранения).
func truthyCell(v any) bool {
	switch typed := v.(type) {
	case nil:
		return false
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "да":
			return true
		}
		return false
	}
	return false
}

// tpCellString — значение ячейки как строка; nil и «<nil>» превращаются в
// пустую строку, иначе пункт «не выбрано» никогда бы не был выбран.
func tpCellString(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "<nil>" {
		return ""
	}
	return s
}
