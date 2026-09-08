package ui

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// ── Отбор подбора: подчинённые справочники и связи параметров выбора ──────────
//
// Две задачи, один механизм.
//
// 1. ПОДЧИНЁННЫЙ СПРАВОЧНИК (`owner:` у справочника, 1С: «Владелец»). Договор
//    выбирается только из договоров своего контрагента, товарная группа — из
//    групп своего направления. Настраивать на форме нечего: подчинение —
//    свойство справочника, отбор появляется сам везде, где его выбирают, и
//    забыть его нельзя. Значение владельца платформа берёт из реквизита формы,
//    ссылающегося на справочник-владелец (он должен быть ровно один — иначе
//    нужен явный `choice_filter`, см. ниже).
//
// 2. СВЯЗИ ПАРАМЕТРОВ ВЫБОРА (`choice_filter:` у поля формы, 1С: «Связи
//    параметров выбора»). Отбор по ЛЮБОМУ реквизиту выбираемого справочника, а
//    не только по владельцу: договоры по организации, склады по подразделению.
//    Тоже без кода — одна строка в форме.
//
// Значение отбора берётся дважды и намеренно:
//   • на сервере (варианты <select> первой отрисовки) — из значений формы;
//   • на клиенте (диалог подбора и перестроение списка) — из живого поля на
//     странице: пользователь мог сменить контрагента секунду назад, до того как
//     форма съездила на сервер.
// Поэтому в разметку уезжает и ИМЯ поля-источника, и его текущее значение.
//
// Отбор объявлен, а значения нет (контрагент не выбран) — список ПУСТ, а не
// полон. Полный список в ответ на невыбранного владельца пользователь принимает
// за отсутствие отбора и выбирает чужой договор.

// refFilterSource — один элемент отбора в разметке: откуда брать значение
// (From — имя поля формы) и каким оно было на момент отрисовки (Value).
type refFilterSource struct {
	From  string `json:"from"`
	Value string `json:"value"`
}

// ownerHolderField возвращает реквизит сущности, из которого берётся владелец для
// подчинённого справочника ownerEntity. Единственность обязательна: если ссылок
// на справочник-владелец в объекте две («Контрагент» и «Прежний контрагент»),
// угадывать нельзя — отбор молча взял бы не то поле, и пользователь увидел бы
// чужой список, не понимая почему. Для таких случаев есть `choice_filter`.
func ownerHolderField(holder *metadata.Entity, ownerEntity string) (metadata.Field, bool) {
	if holder == nil || strings.TrimSpace(ownerEntity) == "" {
		return metadata.Field{}, false
	}
	var found metadata.Field
	count := 0
	for _, f := range holder.Fields {
		if f.RefEntity == ownerEntity {
			found = f
			count++
		}
	}
	if count != 1 {
		return metadata.Field{}, false
	}
	return found, true
}

// ownerValueFromValues — значение владельца из уже отрисованных значений формы.
func ownerValueFromValues(holder *metadata.Entity, ownerEntity string, values map[string]string) string {
	f, ok := ownerHolderField(holder, ownerEntity)
	if !ok {
		return ""
	}
	return strings.TrimSpace(values[f.Name])
}

// refFilterMap — отбор подбора для каждого поля формы: «имя поля» → JSON вида
// {"Владелец":{"from":"Контрагент","value":"<uuid>"}}. Уезжает в разметку
// атрибутом data-ref-filter, а дальше живёт на клиенте.
//
// Собирается в ЕДИНСТВЕННОЙ точке отрисовки форм: подчинение — свойство
// метаданных, и оно обязано доезжать одинаково на карточку, на копию и на форму
// с ошибкой валидации.
func (s *Server) refFilterMap(holder *metadata.Entity, form *metadata.FormModule, values map[string]string) map[string]string {
	if s.reg == nil {
		return nil
	}
	// поле формы → (реквизит справочника → источник значения)
	filters := map[string]map[string]refFilterSource{}

	add := func(field, target string, src refFilterSource) {
		if field == "" || target == "" || src.From == "" {
			return
		}
		if filters[field] == nil {
			filters[field] = map[string]refFilterSource{}
		}
		filters[field][target] = src
	}

	// Подчинение (owner:) — по реквизитам объекта.
	if holder != nil {
		for _, f := range holder.Fields {
			if f.RefEntity == "" || f.RefEntity == "_users" {
				continue
			}
			target := s.reg.GetEntity(f.RefEntity)
			if target == nil || strings.TrimSpace(target.Owner) == "" {
				continue
			}
			hf, ok := ownerHolderField(holder, target.Owner)
			if !ok {
				continue
			}
			add(f.Name, metadata.StandardOwnerField, refFilterSource{From: hf.Name, Value: strings.TrimSpace(values[hf.Name])})
		}
	}

	if form != nil {
		// Подчинение для реквизитов формы (save:false): значение владельца всё
		// равно берётся у объекта — форма выбирает в его контексте.
		for _, a := range form.Attributes {
			if a == nil {
				continue
			}
			refName := attrRefEntityName(a.TypeRef)
			if refName == "" {
				continue
			}
			target := s.reg.GetEntity(refName)
			if target == nil || strings.TrimSpace(target.Owner) == "" {
				continue
			}
			hf, ok := ownerHolderField(holder, target.Owner)
			if !ok {
				continue
			}
			add(a.Name, metadata.StandardOwnerField, refFilterSource{From: hf.Name, Value: strings.TrimSpace(values[hf.Name])})
		}
		// Явные связи параметров выбора. Идут ПОСЛЕ подчинения и перекрывают его:
		// раз связь написали руками, она и главнее — например когда ссылок на
		// владельца в объекте несколько и автоматика отказалась выбирать.
		form.Walk(func(el *metadata.FormElement) bool {
			if el == nil || len(el.ChoiceFilter) == 0 || strings.TrimSpace(el.DataPath) == "" {
				return true
			}
			field := dpFieldName(el.DataPath)
			for target, path := range el.ChoiceFilter {
				from := dpFieldName(strings.TrimSpace(path))
				add(field, strings.TrimSpace(target), refFilterSource{From: from, Value: strings.TrimSpace(values[from])})
			}
			return true
		})
	}

	if len(filters) == 0 {
		return nil
	}
	out := make(map[string]string, len(filters))
	for field, spec := range filters {
		raw, err := json.Marshal(spec)
		if err != nil {
			continue
		}
		out[field] = string(raw)
	}
	return out
}

// ownerFilterParams — параметры списка с отбором по владельцу.
//
// asked — спрашивают ли владельца НА ЭТОЙ форме (есть источник значения). Разница
// принципиальная. Источник есть, а значение пустое — выдача пуста: «сначала
// контрагент, потом договор». Источника нет вовсе (форма выбирает договор, не
// спрашивая контрагента) — отбирать не по чему, и список показывается целиком,
// как в 1С: подчинение без связи параметров выбора там тоже показывает всё.
// Иначе поле на такой форме стало бы навсегда пустым, а причина — невидимой.
func ownerFilterParams(target *metadata.Entity, ownerID string, asked bool, base storage.ListParams) (storage.ListParams, bool) {
	if target == nil || strings.TrimSpace(target.Owner) == "" || !asked {
		return base, true
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return base, false
	}
	if base.Filters == nil {
		base.Filters = map[string]storage.FilterValue{}
	}
	base.Filters[metadata.StandardOwnerField] = storage.FilterValue{Value: ownerID}
	return base, true
}

// refOptionsFilters — отбор из параметра запроса `flt` диалога подбора: JSON
// «реквизит справочника → значение». Неизвестные реквизиты отбрасываем молча:
// имена проверяет `onebase check` при сборке конфигурации, а рантайм не должен
// падать из-за устаревшей вкладки в браузере.
//
// ok=false означает «выдача обязана быть пустой»: у подчинённого справочника не
// прислали владельца.
func refOptionsFilters(target *metadata.Entity, raw string, base storage.ListParams) (storage.ListParams, bool) {
	spec := map[string]string{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			spec = map[string]string{}
		}
	}
	// Владельца СПРАШИВАЮТ, если поле прислало его в отборе — пусть и пустым.
	// Пустое значение при этом означает «ещё не выбрали» (выдача пуста), а
	// отсутствие ключа — «на этой форме владельца не спрашивают» (см.
	// ownerFilterParams).
	_, ownerAsked := spec[metadata.StandardOwnerField]
	for name, value := range spec {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		f, ok := entityFieldByName(target, name)
		if !ok {
			continue
		}
		if base.Filters == nil {
			base.Filters = map[string]storage.FilterValue{}
		}
		base.Filters[f.Name] = storage.FilterValue{Value: value}
	}
	// Подчинённый справочник, у которого владельца спросили, но не выбрали, —
	// пусто. Проверяем ПОСЛЕ разбора: владелец мог приехать и явной связью
	// (choice_filter: {Владелец: …}).
	if target != nil && strings.TrimSpace(target.Owner) != "" && ownerAsked {
		if base.Filters[metadata.StandardOwnerField].Value == "" {
			return base, false
		}
	}
	return base, true
}

// initialReferenceOptionsOwned — варианты <select> для ссылочного поля с учётом
// подчинения: у подчинённого справочника показываются только элементы владельца.
// Выбранное значение добавляется, как и раньше (appendSelectedRefOptions), даже
// если владелец сменился: сохранённое значение обязано остаться видимым, иначе
// поле выглядит пустым, хотя в базе оно заполнено.
func (s *Server) initialReferenceOptionsOwned(ctx context.Context, refEntity *metadata.Entity, mode refOptionsMode, selected []string, ownerID string, asked bool) ([]map[string]any, error) {
	params, ok := ownerFilterParams(refEntity, ownerID, asked, storage.ListParams{Limit: refPickerDefaultLimit})
	if !ok {
		return s.appendSelectedRefOptions(ctx, nil, refEntity, selected), nil
	}
	rows, err := s.referenceOptionsWithParams(ctx, refEntity, mode, params)
	if err != nil {
		return nil, err
	}
	return s.appendSelectedRefOptions(ctx, rows, refEntity, selected), nil
}
