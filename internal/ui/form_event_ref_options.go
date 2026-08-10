package ui

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// eventRefOptions собирает <option> для ссылочных значений, присвоенных
// обработчиком формы, — по одному на каждое значение из ответа события.
//
// Зачем. <select> ссылочного реквизита рисуется первой страницей справочника
// (refPickerDefaultLimit = 50), а клиентский applyValues делает inp.value = val.
// Для <select>, где такого <option> нет, браузер молча ставит selectedIndex=-1:
// поле становится пустым, и следующая запись затирает ссылку в базе. То есть
// присваивание из .form.os теряется тем вернее, чем больше справочник, и без
// единого сообщения (#615). Пятьдесят элементов справочник перерастает почти
// сразу, так что «редкий случай» — это скорее обычный.
//
// Строка догружается тем же путём, что и выбранное значение при отрисовке
// (appendSelectedRefOptions): там уже стоят проверка доступа rowAllowsSelected
// и маска ПДн. Читать запись мимо них ради подписи в <option> нельзя — иначе
// маскированное поле утекло бы в HTML в обход плана 88.
func (s *Server) eventRefOptions(ctx context.Context, form *metadata.FormModule, entity *metadata.Entity, values map[string]any) map[string][]map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]map[string]any)
	for key, raw := range values {
		id := strings.TrimSpace(refValueString(raw))
		if id == "" {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		refEntity := s.reg.GetEntity(eventRefEntityName(form, entity, key))
		if refEntity == nil {
			continue
		}
		if rows := s.appendSelectedRefOptions(ctx, nil, refEntity, []string{id}); len(rows) > 0 {
			out[key] = rows
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// eventRefEntityName — имя справочника, на который ссылается ключ ответа: либо
// поле сущности, либо реквизит формы (save:false), одноимённого поля которому
// в сущности нет.
//
// Порядок тот же, что в mergeFormLocalRefOptions, и по той же причине: имя
// реквизита формы ничем не связано с именами полей сущности и вполне может
// совпасть, а шаблон в этом случае рисует поле сущности.
//
// Служебное `_users` отсеивается само: это не сущность конфигурации, GetEntity
// её не знает. Отдельного лечения оно и не требует — usersForSelection отдаёт
// всех пользователей без предела, поэтому текущее значение из списка не
// выпадает.
func eventRefEntityName(form *metadata.FormModule, entity *metadata.Entity, key string) string {
	if f, ok := entityFieldByName(entity, key); ok {
		return f.RefEntity
	}
	if form == nil {
		return ""
	}
	for _, a := range form.Attributes {
		if a != nil && strings.EqualFold(a.Name, key) {
			return attrRefEntityName(a.TypeRef)
		}
	}
	return ""
}
