package ui

import (
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// formObjectThis — обёртка над *runtime.Object, используемая как this/Объект
// в рантайме событий управляемых форм (план 37, этап 8).
//
// Разница с прямой передачей *runtime.Object в interp.Run:
//
//  1. `Объект.Товары` возвращает *formTpProxy, который умеет CallMethod
//     "Добавить"/"Очистить"/"Количество". Без этого `Объект.Товары.Добавить()`
//     в DSL ничего не делает — на пустой slice метод не вызывается.
//
//  2. Set по реквизитам формы (например, Объект.КешИтога) кладёт значение в
//     Fields, как и Object.Set; рантайм событий не знает про form-attributes
//     отдельно от полей сущности — это упрощение MVP, реквизиты формы
//     неотличимы от полей объекта.
//
// formObjectThis передаётся в interp.Run и под именем «Объект», и под
// «ЭтотОбъект» — потому что в 1С-управляемых формах принято писать
// `Объект.Поле`, а в обработках OnPost — `ЭтотОбъект.Поле`. Делаем оба
// варианта рабочими.
type formObjectThis struct {
	obj         *runtime.Object
	entity      *metadata.Entity
	form        *metadata.FormModule
	refResolver *dslRefAttrResolver
}

func (f *formObjectThis) Get(name string) any {
	if f == nil || f.obj == nil {
		return nil
	}
	nameLower := strings.ToLower(name)
	// Сначала — табличные части. Возвращаем прокси даже если slice ещё nil,
	// чтобы .Добавить() мог создать первую строку.
	if f.entity != nil {
		for i := range f.entity.TableParts {
			tp := &f.entity.TableParts[i]
			if strings.ToLower(tp.Name) == nameLower {
				return &formTpProxy{obj: f.obj, tpName: tp.Name, tp: tp, refResolver: f.refResolver}
			}
		}
	}
	// Формовые атрибуты-таблицы (ValueTable). Если имя не найдено среди ТЧ сущности,
	// ищем формовый атрибут ValueTable и возвращаем для него тот же formTpProxy.
	if f.form != nil {
		for _, attr := range f.form.Attributes {
			if strings.EqualFold(attr.Name, name) && strings.EqualFold(attr.TypeRef, "ValueTable") {
				return &formTpProxy{obj: f.obj, tpName: attr.Name, refResolver: f.refResolver}
			}
		}
	}
	// Дальше — обычные поля (через Object.Get который ищет в Fields).
	v := f.obj.Get(name)
	if ref, ok := v.(*interpreter.Ref); ok && f.refResolver != nil {
		if fd := entityField(f.entity, name); fd != nil && fd.RefEntity != "" {
			return f.refResolver.attachRef(ref, fd.RefEntity)
		}
		if f.entity != nil && (strings.EqualFold(name, "Ссылка") || strings.EqualFold(name, "Reference")) {
			return f.refResolver.attachRef(ref, f.entity.Name)
		}
	}
	// Дефолты по типу: пустой numeric → 0, иначе `Объект.Сумма + 100` в DSL
	// даст concat-строку «<nil>100» (DSL `+` для nil-операнда склеивает
	// строкой), потом форма попытается записать её в PostgreSQL numeric →
	// ERROR 22P02 invalid input syntax for type numeric.
	if v == nil && f.entity != nil {
		for _, fd := range f.entity.Fields {
			if !strings.EqualFold(fd.Name, name) {
				continue
			}
			switch fd.Type {
			case metadata.FieldTypeNumber:
				return float64(0)
			case metadata.FieldTypeBool:
				return false
			}
			break
		}
	}
	return v
}

func (f *formObjectThis) Set(name string, v any) {
	if f == nil || f.obj == nil {
		return
	}
	f.obj.Set(name, v)
}

// formTpProxy — proxy табличной части для рантайма событий формы. В отличие
// от tpProxy (см. dsl_documents.go), привязан напрямую к *runtime.Object, без
// docWriter — потому что в обработчиках формы документ ещё не записан и нет
// открытой транзакции записи.
type formTpProxy struct {
	obj         *runtime.Object
	tpName      string
	tp          *metadata.TablePart
	refResolver *dslRefAttrResolver
}

func (t *formTpProxy) Get(_ string) any    { return nil }
func (t *formTpProxy) Set(_ string, _ any) {}

func (t *formTpProxy) CallMethod(method string, args []any) any {
	if t == nil || t.obj == nil {
		return nil
	}
	switch strings.ToLower(method) {
	case "добавить", "add":
		if t.obj.TablePartRows == nil {
			t.obj.TablePartRows = map[string][]map[string]any{}
		}
		row := map[string]any{}
		t.obj.TablePartRows[t.tpName] = append(t.obj.TablePartRows[t.tpName], row)
		return newRefAwareMapThis(row, t.tp, t.refResolver)
	case "очистить", "clear":
		if t.obj.TablePartRows != nil {
			t.obj.TablePartRows[t.tpName] = nil
		}
	case "количество", "count":
		if t.obj.TablePartRows == nil {
			return float64(0)
		}
		return float64(len(t.obj.TablePartRows[t.tpName]))
	}
	return nil
}

// IterateRows — для `Для Каждого Стр Из Объект.Товары Цикл` интерпретатор
// должен видеть массив строк. Возвращаем срез map'ов; элементы массива
// автоматически оборачиваются в MapThis при доступе через DSL.
func (t *formTpProxy) IterateRows() []map[string]any {
	if t == nil || t.obj == nil || t.obj.TablePartRows == nil {
		return nil
	}
	return t.obj.TablePartRows[t.tpName]
}

func (t *formTpProxy) IterateThis() []interpreter.This {
	rows := t.IterateRows()
	out := make([]interpreter.This, 0, len(rows))
	for _, row := range rows {
		out = append(out, newRefAwareMapThis(row, t.tp, t.refResolver))
	}
	return out
}

func entityField(entity *metadata.Entity, name string) *metadata.Field {
	if entity == nil {
		return nil
	}
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, name) {
			return &entity.Fields[i]
		}
	}
	return nil
}
