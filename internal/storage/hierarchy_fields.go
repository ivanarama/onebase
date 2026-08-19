package storage

// Служебные поля иерархии в наборе значений записи (#1040).
//
// Признак группы и родитель хранятся колонками is_folder и parent_id, но
// прикладной слой пишет по-русски: «Объект.ЭтоГруппа = Истина»,
// «Объект.Родитель = СсылкаНаГруппу». Раньше такое присваивание не делало
// ничего — молча, без ошибки: ключ ложился в набор под своим именем и до
// колонки не доезжал. Конфигурации приходилось знать служебные латинские имена,
// а узнать о них было неоткуда.
//
// Здесь имена сводятся вместе. Заодно родитель принимает ссылку, а не только
// строку идентификатора: DSL кладёт в поле именно ссылку, и требовать от
// конфигуратора Строка(Х.УникальныйИдентификатор()) — значит требовать знания о
// внутреннем устройстве.

import (
	"fmt"
	"strings"
)

// hierarchyValue ищет значение служебного поля по любому из его имён.
// Второе значение — было ли поле вообще передано: «нет ключа» и «ключ с пустым
// значением» означают разное (не трогать против убрать).
func hierarchyValue(fields map[string]any, names ...string) (any, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	for _, name := range names {
		if v, ok := fields[name]; ok {
			return v, true
		}
		for k, v := range fields {
			if strings.EqualFold(k, name) {
				return v, true
			}
		}
	}
	return nil, false
}

// refUUIDString достаёт идентификатор из значения поля-родителя: строка с UUID,
// ссылка DSL (структура с полем UUID) или что-то, чьё строковое представление —
// UUID. Разбор ссылки живёт здесь, а не у вызывающего: иначе каждый путь записи
// сам решал бы, что считать ссылкой, — ровно то расхождение, из-за которого
// родитель из DSL молча не сохранялся.
func refUUIDString(v any) string {
	// refUUIDer уже объявлен в audit.go и реализован *interpreter.Ref: нижний
	// слой узнаёт ссылку, не импортируя интерпретатор.
	if r, ok := v.(refUUIDer); ok {
		return strings.TrimSpace(r.GetRefUUID())
	}
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
