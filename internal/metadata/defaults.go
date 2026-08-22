package metadata

import (
	"fmt"
	"strconv"
	"strings"
)

// Значения по умолчанию у реквизитов (план 153).
//
// Ключ `default:` реквизита сущности объявляет, чем заполняется поле при
// СОЗДАНИИ нового объекта — и только при создании. Разбор вынесен сюда, чтобы
// одна и та же строка одинаково читалась валидацией (`onebase check`) и
// применением (entityservice.ApplyDefaults): расхождение между «что проверили»
// и «что применили» — худший отказ такого механизма, потому что тихий.
//
// Список источников закрытый. Неизвестный источник — ошибка конфигурации, а не
// пустое поле: опечатка `константы.НашаОрганизация` вместо `константа.` иначе
// выглядела бы как «дефолт просто не сработал», и искать её пришлось бы в
// рантайме.

// DefaultKind — вид источника значения по умолчанию.
type DefaultKind string

const (
	// DefaultLiteral — значение записано в YAML как есть (строка, число,
	// булево, значение перечисления).
	DefaultLiteral DefaultKind = "literal"
	// DefaultToday — начало текущего дня (`сегодня` / `today`).
	DefaultToday DefaultKind = "today"
	// DefaultNow — текущие дата-время (`сейчас` / `now`).
	DefaultNow DefaultKind = "now"
	// DefaultConstant — значение константы (`константа.<Имя>` / `constant.<Имя>`).
	DefaultConstant DefaultKind = "constant"
	// DefaultCurrentUser — логин текущего пользователя (`текущийпользователь`).
	DefaultCurrentUser DefaultKind = "currentuser"
	// DefaultSingle — единственный доступный элемент справочника
	// (`единственный` / `single`), иначе поле остаётся пустым.
	DefaultSingle DefaultKind = "single"
)

// DefaultSpec — разобранное значение ключа `default:`.
type DefaultSpec struct {
	Kind DefaultKind
	// Raw — исходная строка из YAML (для литерала это и есть значение).
	Raw string
	// Constant — имя константы для DefaultConstant.
	Constant string
}

// префиксы источника «значение константы»; регистр не важен, как везде в DSL.
var constantPrefixes = []string{"константа.", "constant."}

// ParseDefault разбирает значение ключа `default:`. Пустая строка — «дефолта
// нет» (ok=false, ошибки нет).
func ParseDefault(raw string) (DefaultSpec, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultSpec{}, false, nil
	}
	low := strings.ToLower(trimmed)
	switch low {
	case "сегодня", "today":
		return DefaultSpec{Kind: DefaultToday, Raw: trimmed}, true, nil
	case "сейчас", "now":
		return DefaultSpec{Kind: DefaultNow, Raw: trimmed}, true, nil
	case "текущийпользователь", "currentuser":
		return DefaultSpec{Kind: DefaultCurrentUser, Raw: trimmed}, true, nil
	case "единственный", "single":
		return DefaultSpec{Kind: DefaultSingle, Raw: trimmed}, true, nil
	}
	for _, prefix := range constantPrefixes {
		if !strings.HasPrefix(low, prefix) {
			continue
		}
		name := strings.TrimSpace(trimmed[len(prefix):])
		if name == "" {
			return DefaultSpec{}, false, fmt.Errorf("default %q: не указано имя константы", raw)
		}
		return DefaultSpec{Kind: DefaultConstant, Raw: trimmed, Constant: name}, true, nil
	}
	// Похоже на обращение к объекту конфигурации, но префикс не тот —
	// это опечатка, а не литерал. Литерал с точкой (дробное число, домен,
	// имя файла) от неё отличается тем, что слева от точки не стоит слово,
	// которым конфигуратор явно целился в источник.
	if head, _, ok := strings.Cut(low, "."); ok {
		switch head {
		case "константы", "constants", "конст", "const":
			return DefaultSpec{}, false, fmt.Errorf(
				"default %q: неизвестный источник %q — константа объявляется как `константа.<Имя>`", raw, head)
		case "перечисление", "enum", "справочник", "справочники", "документ", "документы":
			return DefaultSpec{}, false, fmt.Errorf(
				"default %q: источник %q не поддерживается; значение перечисления пишется просто именем значения", raw, head)
		}
	}
	return DefaultSpec{Kind: DefaultLiteral, Raw: trimmed}, true, nil
}

// ValidateDefaults проверяет ключи `default:` всех реквизитов: источник
// известен, совместим с типом поля, а константа/значение перечисления —
// существуют.
//
// Вынесено из Validate отдельной функцией, потому что требует констант:
// Validate их не получает, а добавлять параметр в её сигнатуру значило бы
// править все точки вызова ради одной проверки.
func ValidateDefaults(entities []*Entity, enums []*Enum, constants []*Constant) error {
	enumByName := make(map[string]*Enum, len(enums))
	for _, en := range enums {
		enumByName[strings.ToLower(en.Name)] = en
	}
	constByName := make(map[string]*Constant, len(constants))
	for _, c := range constants {
		constByName[strings.ToLower(c.Name)] = c
	}
	for _, e := range entities {
		for _, f := range e.Fields {
			if err := validateFieldDefault(e.Name, "", f, enumByName, constByName); err != nil {
				return err
			}
		}
		// Реквизиты табличной части: ключ пока не поддерживается, и об этом
		// говорится прямо. Строка ТЧ добавляется уже после создания объекта —
		// из формы (SlickGrid), из DSL и из обработчика события формы, то есть
		// точек больше, чем у создания объекта, и общей среди них нет.
		// Принять ключ и не применять его значило бы отдать конфигуратору
		// молча неработающий дефолт — худший вид отказа для этого механизма.
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if strings.TrimSpace(f.Default) == "" {
					continue
				}
				return fmt.Errorf("%s: default пока не поддерживается в табличной части; "+
					"заполняйте значения новой строки в обработчике формы",
					defaultFieldPath(e.Name, tp.Name, f.Name))
			}
		}
	}
	return nil
}

func validateFieldDefault(entityName, tpName string, f Field, enums map[string]*Enum, constants map[string]*Constant) error {
	spec, ok, err := ParseDefault(f.Default)
	if err != nil {
		return fmt.Errorf("%s: %w", defaultFieldPath(entityName, tpName, f.Name), err)
	}
	if !ok {
		return nil
	}
	where := defaultFieldPath(entityName, tpName, f.Name)
	switch spec.Kind {
	case DefaultToday, DefaultNow:
		if f.Type != FieldTypeDate {
			return fmt.Errorf("%s: default %q применим только к реквизиту типа date (сейчас %s)", where, spec.Raw, f.Type)
		}
	case DefaultCurrentUser:
		// Учётка платформы и элемент прикладного справочника пользователей —
		// разные вещи, и связи между ними в движке нет: политики строкового
		// доступа сопоставляют их по логину (`user: login`). Поэтому источник
		// отдаёт логин и разрешён только для строкового реквизита; ссылку на
		// элемент справочника подставляет конфигурация в ПриСозданииНового.
		if f.Type != FieldTypeString {
			return fmt.Errorf("%s: default %q даёт логин пользователя и применим только к строковому реквизиту (сейчас %s); "+
				"ссылку на элемент справочника подставьте в ПриСозданииНового", where, spec.Raw, f.Type)
		}
	case DefaultSingle:
		if f.RefEntity == "" {
			return fmt.Errorf("%s: default %q применим только к ссылочному реквизиту (сейчас %s)", where, spec.Raw, f.Type)
		}
	case DefaultConstant:
		c, found := constants[strings.ToLower(spec.Constant)]
		if !found {
			return fmt.Errorf("%s: default %q ссылается на несуществующую константу %s", where, spec.Raw, spec.Constant)
		}
		if err := checkDefaultConstantType(where, spec, f, c); err != nil {
			return err
		}
	case DefaultLiteral:
		if err := checkDefaultLiteral(where, spec.Raw, f, enums); err != nil {
			return err
		}
	}
	return nil
}

// checkDefaultConstantType сверяет тип константы с типом реквизита. Сверяем
// именно тип, а не значение: значение живёт в базе и меняется без перезагрузки
// конфигурации, поэтому проверять его на `check` бессмысленно.
func checkDefaultConstantType(where string, spec DefaultSpec, f Field, c *Constant) error {
	if f.RefEntity != "" {
		if !strings.EqualFold(c.RefEntity, f.RefEntity) {
			return fmt.Errorf("%s: default %q — константа ссылается на %s, а реквизит на %s",
				where, spec.Raw, constantTypeName(c), f.RefEntity)
		}
		return nil
	}
	if f.EnumName != "" {
		if !strings.EqualFold(c.EnumName, f.EnumName) {
			return fmt.Errorf("%s: default %q — константа имеет тип %s, а реквизит enum:%s",
				where, spec.Raw, constantTypeName(c), f.EnumName)
		}
		return nil
	}
	if c.RefEntity != "" || c.EnumName != "" || c.Type != f.Type {
		return fmt.Errorf("%s: default %q — константа имеет тип %s, а реквизит %s",
			where, spec.Raw, constantTypeName(c), f.Type)
	}
	return nil
}

func constantTypeName(c *Constant) string {
	switch {
	case c.RefEntity != "":
		return "reference:" + c.RefEntity
	case c.EnumName != "":
		return "enum:" + c.EnumName
	default:
		return string(c.Type)
	}
}

func checkDefaultLiteral(where, raw string, f Field, enums map[string]*Enum) error {
	if f.EnumName != "" {
		en := enums[strings.ToLower(f.EnumName)]
		if en == nil {
			// Неизвестное перечисление ловит Validate; здесь молчим, чтобы не
			// выдавать две ошибки об одном и том же.
			return nil
		}
		for _, v := range en.Values {
			if strings.EqualFold(v, raw) {
				return nil
			}
		}
		return fmt.Errorf("%s: default %q — нет такого значения в перечислении %s", where, raw, f.EnumName)
	}
	switch f.Type {
	case FieldTypeNumber:
		if _, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64); err != nil {
			return fmt.Errorf("%s: default %q не число", where, raw)
		}
	case FieldTypeBool:
		if _, ok := ParseBoolLiteral(raw); !ok {
			return fmt.Errorf("%s: default %q не булево значение (Истина/Ложь, true/false)", where, raw)
		}
	case FieldTypeDate:
		return fmt.Errorf("%s: default %q — у реквизита-даты допустимы только `сегодня` и `сейчас`", where, raw)
	case FieldTypeImage, FieldTypeRichText:
		return fmt.Errorf("%s: default не поддерживается для реквизита типа %s", where, f.Type)
	case FieldTypeString:
		// Строке годится любой литерал.
	default:
		if f.RefEntity != "" {
			return fmt.Errorf("%s: default %q — у ссылочного реквизита допустимы `константа.<Имя>` и `единственный`", where, raw)
		}
	}
	return nil
}

// ParseBoolLiteral разбирает булев литерал в русском и английском написании.
// Отдельная функция, потому что YAML 1.2 читает `off`/`no` как строку, и
// значения из конфигураций приходят сюда в обоих написаниях.
func ParseBoolLiteral(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "истина", "true", "1", "да", "yes":
		return true, true
	case "ложь", "false", "0", "нет", "no":
		return false, true
	}
	return false, false
}

func defaultFieldPath(entityName, tpName, fieldName string) string {
	if tpName != "" {
		return fmt.Sprintf("entity %s: табличная часть %s, реквизит %s", entityName, tpName, fieldName)
	}
	return fmt.Sprintf("entity %s: реквизит %s", entityName, fieldName)
}
