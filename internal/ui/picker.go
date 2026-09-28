package ui

import (
	"encoding/json"
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
	// Single — «выбрать ОДНУ строку»: переключатели вместо флажков, без
	// «выбрать всё» и без корзины. Диалог писался под подбор номенклатуры, где
	// строк отмечают много; там же, где значение ровно одно (подставить в
	// ссылочный реквизит), мультивыбор — не гибкость, а лишний способ ошибиться:
	// отметить две строки можно, а сделать с ними обеими нечего.
	Single bool `json:"single"`
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
//	ОдинВыбор}
//
//	(опционально).
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
				Title:       pickStr(dslField(args[2], "Заголовок", "Title")),
				SearchField: pickStr(dslField(args[2], "ПолеПоиска", "SearchField")),
				QtyField:    pickStr(dslField(args[2], "ПолеКоличества", "QtyField")),
				CheckAll:    pickBool(dslField(args[2], "ВыбратьВсе", "CheckAll")),
				Single:      pickBool(dslField(args[2], "ОдинВыбор", "Single")),
			}
		}
		*sink = p
		return nil, nil
	})
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
