package onec_forms

import "fmt"

// Severity — уровень предупреждения.
type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// Warning — единичное диагностическое сообщение от конвертера/валидатора.
// Element/Field/Line опциональны.
type Warning struct {
	Severity Severity
	Code     string // W001..W050 (см. константы ниже)
	Element  string // имя элемента, если применимо
	Field    string // имя поля внутри элемента, если применимо
	Line     int    // номер строки в исходном XML/BSL, если применимо
	Message  string
	Suggest  string // рекомендация по ручной правке
}

// String — короткое представление для CLI/логов.
func (w Warning) String() string {
	loc := ""
	if w.Element != "" {
		loc = " [" + w.Element
		if w.Field != "" {
			loc += "." + w.Field
		}
		loc += "]"
	}
	if w.Line > 0 {
		loc += fmt.Sprintf(" (line %d)", w.Line)
	}
	out := fmt.Sprintf("%s %s%s: %s", w.Severity, w.Code, loc, w.Message)
	if w.Suggest != "" {
		out += " — " + w.Suggest
	}
	return out
}

// Коды предупреждений конвертера. Распределение диапазонов:
//
//	W001..W009 — общие (структура файлов, чтение/запись).
//	W010..W019 — элементы формы (типы, неизвестные узлы, неподдержка).
//	W020..W029 — типы реквизитов (composite, AnyRef, неизвестные xs:*).
//	W030..W039 — события (без 1:1 аналога, неизвестные).
//	W040..W049 — BSL-модуль (несовместимые конструкции).
//	W050      — общий "needs manual review".
const (
	W001_FileNotFound    = "W001"
	W002_InvalidXML      = "W002"
	W003_InvalidYAML     = "W003"
	W004_VersionMismatch = "W004"

	W010_UnknownElement    = "W010"
	W011_UnsupportedProp   = "W011"
	W012_MissingDataPath   = "W012"
	W013_ResourceMissing   = "W013"
	W014_CommandNoAction   = "W014" // команда без action — не попадёт ни в командную панель, ни на кнопку
	W015_FormLocalRefField = "W015" // form-local реквизит перечислимого типа в ПолеВвода не даёт выбор

	W020_CompositeType = "W020"
	W021_AnyRef        = "W021"
	W022_UnknownType   = "W022"

	W030_UnmappedEvent    = "W030"
	W031_EventWithoutProc = "W031"

	W040_BSLNotInDSL      = "W040" // конструкция BSL без аналога в DSL OneBase
	W041_DSLNotInBSL      = "W041" // конструкция DSL OneBase без аналога в BSL
	W042_DirectiveMissing = "W042"

	W050_NeedsReview = "W050"
)

// Warnings — удобный alias для слайса.
type Warnings []Warning

// Add — добавить предупреждение.
func (ws *Warnings) Add(w Warning) {
	*ws = append(*ws, w)
}

// HasErrors — true, если в списке есть хотя бы одно error-предупреждение.
func (ws Warnings) HasErrors() bool {
	for _, w := range ws {
		if w.Severity == SeverityError {
			return true
		}
	}
	return false
}
