package ui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// ── Подбор: платформенный механизм диалога мультивыбора (план 46) ──────────
//
// Двухфазная схема поверх round-trip managed-форм:
//
//   Фаза 1 (событие Нажатие у кнопки ТЧ): DSL-обработчик собирает кандидатов
//   и вызывает билтин ПоказатьПодбор(Данные, Колонки, Конфиг). Билтин ничего
//   не открывает — он лишь складывает payload в side-channel (как Сообщить
//   копит сообщения). После interp.Run сервер кладёт payload в ответ
//   (pickerData), и клиентский JS открывает модальный диалог.
//
//   Фаза 2 (событие Выбор): пользователь отметил строки и нажал «Перенести»;
//   JS возвращает выбор как _pick_result (JSON). Сервер парсит его в
//   переменную ПодборРезультат (Массив структур), и DSL-обработчик события
//   Выбор добавляет строки в Объект.<ТЧ>.
//
// Семантика ПоказатьПодбор — терминальная: код DSL после вызова выполнится,
// но на UI не повлияет (показ диалога — это «выход» фазы 1, аналог
// асинхронного ОткрытьФорму(..., Оповещение) в 1С). Рекомендуется делать
// ПоказатьПодбор последней операцией обработчика фазы 1.

// pickerPayload — данные диалога подбора, сериализуются в JSON-ответ события.
type pickerPayload struct {
	Columns []pickerColumn `json:"columns"`
	Rows    []pickerRow    `json:"rows"`
	Config  pickerConfig   `json:"config"`
}

// pickerColumn — описание колонки диалога.
type pickerColumn struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Type     string `json:"type"`     // string | number | reference
	Editable bool   `json:"editable"` // редактируемое поле (обычно количество)
}

// pickerRow — строка диалога. ID — идентификатор записи (UUID ссылки),
// возвращается в ПодборРезультат для НайтиПоИдентификатору. Data — значения
// по колонкам.
type pickerRow struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

// pickerConfig — параметры диалога.
type pickerConfig struct {
	Title       string `json:"title"`       // заголовок окна
	SearchField string `json:"searchField"` // имя колонки для фильтра поиска
	QtyField    string `json:"qtyField"`    // имя редактируемой колонки количества
	CheckAll    bool   `json:"checkAll"`    // предвыбрать все строки
	// ServerSearch — строка поиска диалога спрашивает СЕРВЕР (событие Поиск),
	// а не фильтрует уже приехавшие строки. Нужен там, где клиентский фильтр
	// бессилен: выдача обрезана пределом, а искомое за ним, либо колонка
	// показана маской ПДн (план 88) и её текст искать бессмысленно.
	ServerSearch bool `json:"serverSearch"`
	// Single — «выбрать ОДНУ строку»: переключатели вместо флажков, без
	// «выбрать всё» и без корзины. Диалог писался под подбор номенклатуры, где
	// строк отмечают много; там же, где значение ровно одно (подставить в
	// ссылочный реквизит), мультивыбор — не гибкость, а лишний способ ошибиться:
	// отметить две строки можно, а сделать с ними обеими нечего.
	Single bool `json:"single"`
	// Filters — колонки, по которым над списком стоят выпадающие отборы
	// («Направление», «Филиал»). Строка поиска ищет подстроку во всех колонках
	// сразу и не отличает направление «СМ» от мастера «Смирнов»; отбор
	// сравнивает значение колонки целиком. Списки значений строятся из
	// приехавших строк и сужают друг друга.
	Filters []string `json:"filters,omitempty"`
}

// newPickerBuiltin создаёт билтин ПоказатьПодбор. Записывает собранный payload
// по адресу sink. Сигнатура DSL:
//
//	ПоказатьПодбор(Данные, Колонки [, Конфиг])
//
// Данные  — Массив структур: каждая строка содержит поле "Идентификатор" (или
//
//	"ID") с UUID и значения колонок (по их Имени).
//
// Колонки — Массив структур {Имя, Заголовок, Тип, Редактируемое}.
// Конфиг  — Структура {Заголовок, ПолеПоиска, ПолеКоличества, ВыбратьВсе,
//
//	ОдинВыбор, Отборы}
//
//	(опционально). Отборы — имена колонок строкой через запятую или Массивом.
func newPickerBuiltin(sink **pickerPayload) interpreter.BuiltinFunc {
	return interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		p := &pickerPayload{}
		// Колонки (args[1]).
		if len(args) > 1 {
			for _, c := range iterateAny(args[1]) {
				col := pickerColumn{
					Name:     pickStr(dslField(c, "Имя", "Name")),
					Title:    pickStr(dslField(c, "Заголовок", "Title")),
					Type:     strings.ToLower(pickStr(dslField(c, "Тип", "Type"))),
					Editable: pickBool(dslField(c, "Редактируемое", "Editable")),
				}
				if col.Title == "" {
					col.Title = col.Name
				}
				if col.Type == "" {
					col.Type = "string"
				}
				p.Columns = append(p.Columns, col)
			}
		}
		// Данные (args[0]).
		if len(args) > 0 {
			for _, r := range iterateAny(args[0]) {
				row := pickerRow{
					ID:   pickStr(dslField(r, "Идентификатор", "ID")),
					Data: map[string]any{},
				}
				for _, col := range p.Columns {
					row.Data[col.Name] = serializeValue(dslField(r, col.Name))
				}
				p.Rows = append(p.Rows, row)
			}
		}
		// Конфиг (args[2]).
		if len(args) > 2 && args[2] != nil {
			p.Config = pickerConfig{
				Title:        pickStr(dslField(args[2], "Заголовок", "Title")),
				SearchField:  pickStr(dslField(args[2], "ПолеПоиска", "SearchField")),
				QtyField:     pickStr(dslField(args[2], "ПолеКоличества", "QtyField")),
				CheckAll:     pickBool(dslField(args[2], "ВыбратьВсе", "CheckAll")),
				ServerSearch: pickBool(dslField(args[2], "ПоискНаСервере", "ServerSearch")),
				Single:       pickBool(dslField(args[2], "ОдинВыбор", "Single")),
			}
			filters, err := pickerFilters(dslField(args[2], "Отборы", "Filters"), p.Columns)
			if err != nil {
				return nil, err
			}
			p.Config.Filters = filters
		}
		*sink = p
		return nil, nil
	})
}

// pickerFilters разбирает Конфиг.Отборы: строку «Направление, Филиал» или
// Массив имён. Имя сверяется с колонками без учёта регистра и приводится к
// имени колонки — клиент ищет значения по нему. Неизвестная или редактируемая
// колонка — ошибка: молча пропущенный отбор выглядел бы как «отбор не работает».
func pickerFilters(v any, cols []pickerColumn) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	var names []string
	// Пустой Массив имеет nil-срез элементов, но остаётся коллекцией.
	switch v.(type) {
	case *interpreter.Array, []any:
		for _, it := range iterateAny(v) {
			names = append(names, pickStr(it))
		}
	default:
		names = strings.Split(pickStr(v), ",")
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		var col *pickerColumn
		for i := range cols {
			if strings.EqualFold(cols[i].Name, name) {
				col = &cols[i]
				break
			}
		}
		if col == nil {
			return nil, fmt.Errorf("ПоказатьПодбор: в Отборы указана колонка «%s», которой нет в Колонках", name)
		}
		if col.Editable {
			return nil, fmt.Errorf("ПоказатьПодбор: колонка «%s» редактируемая — отбор по ней невозможен", col.Name)
		}
		if !seen[col.Name] {
			seen[col.Name] = true
			out = append(out, col.Name)
		}
	}
	return out, nil
}

// parsePickResult разбирает _pick_result (JSON-массив объектов от диалога) в
// Массив DSL из MapThis — переменную ПодборРезультат для обработчика фазы 2.
// Каждый объект приходит как {id, <col>: val, ...}; ключи доступны
// регистронезависимо (MapThis.Get). Возвращает nil при пустом/битом JSON.
func parsePickResult(raw string) *interpreter.Array {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, &interpreter.MapThis{M: row})
	}
	return interpreter.NewArray(items)
}

// ── helper'ы чтения DSL-значений ──────────────────────────────────────────

// iterateAny возвращает элементы DSL-коллекции (Массив) или nil. Поддерживает
// *interpreter.Array и обычный []any на случай прямой передачи из Go.
func iterateAny(v any) []any {
	switch t := v.(type) {
	case *interpreter.Array:
		return t.Iterate()
	case []any:
		return t
	}
	return nil
}

// dslField читает поле из DSL-значения (Структура/MapThis) по одному из имён
// (первое непустое). Регистронезависимо.
func dslField(v any, names ...string) any {
	for _, name := range names {
		switch t := v.(type) {
		case *interpreter.Struct:
			if r := t.Get(name); r != nil {
				return r
			}
		case *interpreter.MapThis:
			if r := t.Get(name); r != nil {
				return r
			}
		case interpreter.This:
			if r := t.Get(name); r != nil {
				return r
			}
		}
	}
	return nil
}

func pickStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if r, ok := v.(interface{ GetRefUUID() string }); ok {
		if u := r.GetRefUUID(); u != "" {
			return u
		}
	}
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return strings.TrimSpace(strconv.FormatFloat(toFloatOr(v), 'f', -1, 64))
}

func pickBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "Да" || t == "1"
	case float64:
		return t != 0
	}
	return false
}

func toFloatOr(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	}
	return 0
}
