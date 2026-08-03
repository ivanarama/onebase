// Package langref — единый машиночитаемый справочник встроенного языка OneBase
// (функции, методы объектов, конструкции, язык запросов). Источник истины для
// ai-guide (AGENTS.md) и справки в конфигураторе (автодополнение/hover/окно).
// ВАЖНО: пакет interpreter этот пакет НЕ импортирует — циклов нет; связь с
// реестром функций живёт только в completeness_test.go.
package langref

import (
	"sort"
	"strings"
)

// Kind классифицирует запись для группировки, фильтрации и иконок в UI.
type Kind string

const (
	KindFunc    Kind = "func"    // глобальная функция: Сообщить(...)
	KindMethod  Kind = "method"  // метод объекта: Запрос.Выполнить()
	KindKeyword Kind = "keyword" // конструкция языка: Если … Тогда … КонецЕсли
	KindQuery   Kind = "query"   // язык запросов: ВЫБРАТЬ, .Остатки()
)

// Param — параметр функции/метода.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Doc      string `json:"doc,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

// Descriptor — одна запись справочника.
type Descriptor struct {
	Name      string   `json:"name"`              // канон. рус. имя (для матчинга сравнивается lower-case)
	Display   string   `json:"display"`           // как показывать: "СтрЗаменить"
	Aliases   []string `json:"aliases,omitempty"` // англ. синонимы: "StrReplace"
	Kind      Kind     `json:"kind"`
	Object    string   `json:"object,omitempty"` // для методов: "Запрос", "Массив"…
	Signature string   `json:"signature"`        // готовая строка сигнатуры
	Params    []Param  `json:"params,omitempty"`
	Returns   string   `json:"returns,omitempty"` // тип возврата ("" если нет)
	Doc       string   `json:"doc"`               // 1–3 предложения
	Example   string   `json:"example,omitempty"`
	Snippet   string   `json:"snippet,omitempty"` // шаблон вставки для автодополнения (Monaco snippet: ${1:…} табстопы, $0 — курсор)
	Group     string   `json:"group,omitempty"`   // для дерева функций: "Строки", "Даты"…
}

// All возвращает все дескрипторы из всех файлов пакета.
func All() []Descriptor {
	out := make([]Descriptor, 0,
		len(functionDescriptors)+len(keywordDescriptors)+len(queryDescriptors)+len(methodDescriptors))
	out = append(out, functionDescriptors...)
	out = append(out, keywordDescriptors...)
	out = append(out, queryDescriptors...)
	out = append(out, methodDescriptors...)
	return out
}

// ByName ищет дескриптор по имени или алиасу, регистронезависимо.
func ByName(name string) (Descriptor, bool) {
	ln := strings.ToLower(strings.TrimSpace(name))
	for _, d := range All() {
		if strings.ToLower(d.Name) == ln {
			return d, true
		}
		for _, a := range d.Aliases {
			if strings.ToLower(a) == ln {
				return d, true
			}
		}
	}
	return Descriptor{}, false
}

// Objects — уникальные имена объектов (для дерева методов), отсортированы.
func Objects() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range All() {
		if d.Kind == KindMethod && d.Object != "" && !seen[d.Object] {
			seen[d.Object] = true
			out = append(out, d.Object)
		}
	}
	sort.Strings(out)
	return out
}

// Groups — уникальные группы функций (для дерева функций), отсортированы.
func Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range All() {
		if d.Kind == KindFunc && d.Group != "" && !seen[d.Group] {
			seen[d.Group] = true
			out = append(out, d.Group)
		}
	}
	sort.Strings(out)
	return out
}
