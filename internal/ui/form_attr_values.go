package ui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Реквизиты формы в событийном пути.
//
// formToFields строит Объект только из entity.Fields, а единственный разбор
// form.Attributes — parseValueTableRows и только для TypeRef == "ValueTable".
// Поэтому значение, выбранное на форме в реквизите формы (attributes с
// save:false), обработчику .form.os было не видно: Объект.Склад давал nil.
// После #522 такие реквизиты получили рабочий пикер, и это стало заметно —
// выбрать можно, прочитать нельзя.
//
// Здесь реквизиты формы домешиваются в obj.Fields с типизацией по TypeRef, а
// ссылочные заворачиваются в *interpreter.Ref — тем же способом, что и поля
// сущности в enrichHeaderRefs, чтобы работало Объект.Склад.Наименование.

// formAttrIsScalar отсеивает реквизиты, которые обрабатываются отдельно
// (таблицы значений) либо не приходят из формы скалярным значением.
func formAttrIsScalar(a *metadata.FormAttribute) bool {
	return a != nil && a.Name != "" && a.TypeRef != "ValueTable"
}

// typeFormAttrValue приводит строку из формы к типу реквизита. Разбор совпадает
// с formToFields для полей сущности: пустая строка — nil, дата в трёх раскладках,
// число через parseFormNumber, «true» для булева.
func typeFormAttrValue(typeRef, raw string) any {
	if raw == "" {
		return nil
	}
	lower := strings.ToLower(typeRef)
	switch {
	case strings.HasPrefix(lower, "bool"):
		return raw == "true"
	case strings.HasPrefix(lower, "date"):
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return t
			}
		}
		return raw
	case strings.HasPrefix(lower, "number"), strings.HasPrefix(lower, "decimal"):
		if n, err := parseFormNumber(raw); err == nil {
			return n
		}
		return raw
	}
	return raw
}

// mergeFormAttrValues домешивает в obj.Fields значения реквизитов формы,
// пришедшие в POST. Имена, совпадающие с полями сущности, пропускаются: там
// значение уже разобрано formToFields, и подмена сломала бы поле сущности.
func (s *Server) mergeFormAttrValues(
	ctx context.Context,
	r *http.Request,
	form *metadata.FormModule,
	entity *metadata.Entity,
	obj *runtime.Object,
) {
	if form == nil || obj == nil || obj.Fields == nil || len(form.Attributes) == 0 {
		return
	}
	submitted := submittedFormKeys(r)
	for _, a := range form.Attributes {
		if !formAttrIsScalar(a) {
			continue
		}
		if _, isEntityField := entityFieldByName(entity, a.Name); isEntityField {
			continue
		}
		if !formKeySubmitted(submitted, a.Name) {
			continue
		}
		val := typeFormAttrValue(a.TypeRef, strings.TrimSpace(r.FormValue(a.Name)))
		if val == nil {
			obj.Fields[a.Name] = nil
			continue
		}
		// Ссылочный реквизит — заворачиваем в Ref, чтобы в DSL работали и
		// сравнение, и обращение к реквизитам: Объект.Склад.Наименование.
		if refName := attrRefEntityName(a.TypeRef); refName != "" {
			if ref := s.formAttrRef(ctx, refName, fmt.Sprintf("%v", val)); ref != nil {
				obj.Fields[a.Name] = ref
				continue
			}
		}
		obj.Fields[a.Name] = val
	}
}

// addFormAttrVars публикует реквизиты формы как переменные с голым именем —
// так их видит модуль управляемой формы в 1С, где под «Объект» лежит только
// основной реквизит. Значение берётся у formObjectThis, поэтому ссылочный
// реквизит приходит уже как *Ref (работает .Код/.Наименование), а ValueTable —
// как прокси таблицы.
//
// Присваивание голому имени остаётся локальной переменной обработчика: обратно
// в форму уезжает только то, что записано через Объект.<Реквизит>.
func addFormAttrVars(form *metadata.FormModule, entity *metadata.Entity, this *formObjectThis, vars map[string]any) {
	if form == nil || this == nil || vars == nil {
		return
	}
	// Занятые имена (Объект, ЭтотОбъект, встроенные объекты доступа) переопределять
	// нельзя: реквизит с именем «Запрос» иначе убил бы билтин для всей процедуры.
	taken := make(map[string]bool, len(vars))
	for k := range vars {
		taken[strings.ToLower(k)] = true
	}
	for _, a := range form.Attributes {
		if a == nil || a.Name == "" || a.MainAttribute {
			continue
		}
		if taken[strings.ToLower(a.Name)] {
			continue
		}
		// Одноимённое поле сущности читается только как Объект.<Поле>: голое имя
		// было бы неоднозначным, а реквизита формы с таким именем по сути нет.
		if _, isEntityField := entityFieldByName(entity, a.Name); isEntityField {
			continue
		}
		vars[a.Name] = this.Get(a.Name)
	}
}

// setFormSelfRef кладёт в объект псевдо-реквизит «Ссылка» на саму запись —
// тот же контракт, что даёт entityservice.Save хукам ПриЗаписи/ОбработкаПроведения.
// Только для существующей записи: у новой ссылки ещё нет, и ПолучитьОбъект() по
// сгенерированному buildObjectFromForm uuid всё равно ничего не найдёт — пусть
// обработчик увидит Неопределено и скажет об этом явно.
func setFormSelfRef(r *http.Request, entity *metadata.Entity, obj *runtime.Object) {
	if entity == nil || obj == nil || strings.TrimSpace(r.FormValue("_id")) == "" {
		return
	}
	if obj.Fields == nil {
		obj.Fields = map[string]any{}
	}
	selfRef := &interpreter.Ref{UUID: obj.ID.String(), Type: entity.Name}
	obj.Fields["ссылка"] = selfRef
	obj.Fields["reference"] = selfRef
}

// formAttrRef строит *interpreter.Ref по имени сущности и строковому uuid.
// Образец — enrichHeaderRefs; nil означает «оставить как есть».
func (s *Server) formAttrRef(ctx context.Context, refEntityName, idStr string) *interpreter.Ref {
	refEntity := s.reg.GetEntity(refEntityName)
	if refEntity == nil {
		return nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil
	}
	refRows, err := s.store.GetFieldsByIDs(ctx, refEntity, []uuid.UUID{id}, displayField(refEntity))
	if err != nil {
		return nil
	}
	refRow := refRows[id.String()]
	if refRow == nil {
		return nil
	}
	return &interpreter.Ref{
		UUID:    idStr,
		Name:    s.maskedRecordLabel(ctx, refEntity, refRow),
		Type:    refEntity.Name,
		Manager: s.refManagerFor(refEntity, ctx),
	}
}

// normalizeFormAttrKeys возвращает ключам реквизитов формы оригинальный регистр.
// Присваивание в обработчике (Объект.Склад = …) идёт через Object.Set, который
// кладёт ключ в нижнем регистре, а клиентский applyValues ищет элемент по
// точному [name="Склад"] — без нормализации ответ до формы не доезжает.
// Поля сущности приводит к регистру метаданных сам serializeFieldsForEntity.
func normalizeFormAttrKeys(values map[string]any, form *metadata.FormModule, entity *metadata.Entity) map[string]any {
	if values == nil || form == nil || len(form.Attributes) == 0 {
		return values
	}
	for _, a := range form.Attributes {
		if !formAttrIsScalar(a) {
			continue
		}
		if _, isEntityField := entityFieldByName(entity, a.Name); isEntityField {
			continue
		}
		low := strings.ToLower(a.Name)
		if low == a.Name {
			continue
		}
		// При дубле побеждает мутация обработчика: она пишется через Object.Set
		// в нижнем регистре, тогда как исходное значение из формы лежит в
		// оригинальном. Тот же принцип, что и для полей сущности в
		// serializeFieldsForEntity, — иначе присваивание в .form.os не доезжает
		// до формы, потому что клиент читает ключ точного регистра.
		if v, ok := values[low]; ok {
			values[a.Name] = v
			delete(values, low)
		}
	}
	return values
}
