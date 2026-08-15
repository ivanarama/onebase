package entityservice

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Значение поля-перечисления не проверялось ничем: произвольная строка
// доезжала до БД как есть (#769).
//
// Для констант это чинили в #320/#321, но тот фикс закрыл ровно один хендлер.
// Обычные реквизиты справочников и документов остались без проверки, потому что
// её никогда не было в общем ядре записи. А через Save идут ВСЕ пути: браузерная
// форма (там `<select>` ограничивает выбор на клиенте — то есть ни на чём),
// REST v2 и синхронизация офлайн-очереди, где устаревшее значение из кэша
// проходит молча.
//
// Проверка живёт здесь, а не в каждом входе, ровно поэтому: одна копия на все
// пути. Пустое значение допустимо — «не выбрано» законно; обязательность поля
// проверяется отдельно и другим механизмом.

// enumChecker отдаёт метаданные перечислений. Интерфейс, а не *runtime.Registry,
// чтобы проверку можно было прогнать без поднятого реестра.
type enumChecker interface {
	GetEnum(name string) *metadata.Enum
	Enums() []*metadata.Enum
}

// validateEnumFields проверяет значения всех enum-реквизитов шапки и строк
// табличных частей. Возвращает пользовательскую ошибку (её показывают как
// DSLError — техническим сбоем это не является) либо пустую строку.
func validateEnumFields(reg enumChecker, entity *metadata.Entity, fields map[string]any,
	tpRows map[string][]map[string]any) string {
	if reg == nil || entity == nil {
		return ""
	}
	// Реестр без перечислений — не повод отклонять запись: проверять не с чем.
	// Ровно так же поступает metadata.Validate (`len(enums) > 0` в проверке
	// ссылки поля на перечисление): контексты вроде procrun и служебных
	// прогонов поднимают неполный реестр, и превращать это в отказ записи
	// значило бы сломать рабочие сценарии ради несуществующей защиты.
	if len(reg.Enums()) == 0 {
		return ""
	}
	for _, f := range entity.Fields {
		if f.EnumName == "" {
			continue
		}
		if msg := checkEnumValue(reg, entity.Name+"."+f.Name, f.EnumName, valueByNameFold(fields, f.Name)); msg != "" {
			return msg
		}
	}
	for _, tp := range entity.TableParts {
		rows := tpRows[tp.Name]
		for _, f := range tp.Fields {
			if f.EnumName == "" {
				continue
			}
			for i, row := range rows {
				where := fmt.Sprintf("%s.%s[%d].%s", entity.Name, tp.Name, i+1, f.Name)
				if msg := checkEnumValue(reg, where, f.EnumName, valueByNameFold(row, f.Name)); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

// checkEnumValue — одно значение. Ненайденное перечисление тоже ошибка:
// «проверить нечем» не повод записать что угодно.
func checkEnumValue(reg enumChecker, where, enumName string, value any) string {
	if value == nil {
		return ""
	}
	val := strings.TrimSpace(fmt.Sprintf("%v", value))
	if val == "" || val == "<nil>" {
		return ""
	}
	en := reg.GetEnum(enumName)
	if en == nil {
		return fmt.Sprintf("%s: перечисление %s не найдено", where, enumName)
	}
	for _, v := range en.Values {
		if v == val {
			return ""
		}
	}
	return fmt.Sprintf("%s: недопустимое значение «%s» (не входит в перечисление %s)", where, val, enumName)
}

// valueByNameFold достаёт значение поля без учёта регистра ключа: формы кладут
// PascalCase, Object.Set — lowercase.
func valueByNameFold(row map[string]any, name string) any {
	if row == nil {
		return nil
	}
	if v, ok := row[name]; ok {
		return v
	}
	for k, v := range row {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}
