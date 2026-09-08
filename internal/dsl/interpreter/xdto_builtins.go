package interpreter

// DSL-объект СериализаторXDTO — сериализация объекта конфигурации в XML формата
// 1С:Предприятия и обратный разбор:
//
//	Текст = СериализаторXDTO.ЗаписатьXML(Док);
//	Док2  = СериализаторXDTO.ПрочитатьXML(Текст);
//
// Имя и методы намеренно совпадают с 1С: код, переносимый с платформы, обычно
// зовёт СериализаторXDTO.ЗаписатьXML/ПрочитатьXML, и приёмник на той стороне
// ждёт ровно этот формат.
//
// Объект контекстный, а не глобальная функция: чтобы собрать XML, нужны
// метаданные (порядок и типы реквизитов, состав табличных частей), а чтобы
// разобрать — тем более, потому что тип ссылки в XML не записан.

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/xdto"
)

// XDTOEntityLookup — то, что сериализатору нужно от реестра конфигурации.
// Реализуется *runtime.Registry.
type XDTOEntityLookup interface {
	GetEntity(name string) *metadata.Entity
}

// XDTOObjectSource реализуют DSL-обёртки объектов (документ, элемент
// справочника), отдавая сериализатору внутреннее представление. Без него
// сериализатор видел бы только Get/Set и не смог бы прочитать табличные части.
type XDTOObjectSource interface {
	XDTOObject() *runtime.Object
}

// XDTOSerializer — DSL-объект СериализаторXDTO.
type XDTOSerializer struct {
	reg XDTOEntityLookup
}

// NewXDTOSerializer создаёт объект для инъекции в extraVars.
func NewXDTOSerializer(reg XDTOEntityLookup) *XDTOSerializer {
	return &XDTOSerializer{reg: reg}
}

func (s *XDTOSerializer) TypeName() string { return "СериализаторXDTO" }

func (s *XDTOSerializer) Get(_ string) any { return nil }

func (s *XDTOSerializer) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записатьxml", "writexml":
		return s.write(args)
	case "прочитатьxml", "readxml":
		return s.read(args)
	}
	panic(userError{Msg: "СериализаторXDTO: неизвестный метод «" + method + "», доступны ЗаписатьXML и ПрочитатьXML"})
}

func (s *XDTOSerializer) write(args []any) any {
	if len(args) == 0 || args[0] == nil {
		panic(userError{Msg: "СериализаторXDTO.ЗаписатьXML: ожидается объект"})
	}
	obj := asRuntimeObject(args[0])
	if obj == nil {
		panic(userError{Msg: fmt.Sprintf("СериализаторXDTO.ЗаписатьXML: %v — не объект конфигурации; сериализуются элементы справочников и документы", args[0])})
	}
	ent := s.entityOf(obj)
	text, err := xdto.Write(ent, obj, optionsOf(obj))
	if err != nil {
		panic(userError{Msg: err.Error()})
	}
	return text
}

func (s *XDTOSerializer) read(args []any) any {
	if len(args) == 0 {
		panic(userError{Msg: "СериализаторXDTO.ПрочитатьXML: ожидается текст XML"})
	}
	text, ok := args[0].(string)
	if !ok {
		panic(userError{Msg: "СериализаторXDTO.ПрочитатьXML: ожидается строка"})
	}
	if s.reg == nil {
		panic(userError{Msg: "СериализаторXDTO.ПрочитатьXML: реестр конфигурации недоступен"})
	}
	obj, opts, err := xdto.Read(text, s.reg.GetEntity)
	if err != nil {
		panic(userError{Msg: err.Error()})
	}
	obj.Fields["deletion_mark"] = opts.DeletionMark
	obj.Fields["posted"] = opts.Posted
	return obj
}

func (s *XDTOSerializer) entityOf(obj *runtime.Object) *metadata.Entity {
	if s.reg == nil {
		panic(userError{Msg: "СериализаторXDTO: реестр конфигурации недоступен"})
	}
	ent := s.reg.GetEntity(obj.Type)
	if ent == nil {
		panic(userError{Msg: "СериализаторXDTO: объект «" + obj.Type + "» не найден в конфигурации"})
	}
	return ent
}

// asRuntimeObject достаёт внутреннее представление из того, что пришло из DSL:
// сам объект, DSL-обёртка документа/элемента либо ссылка, уже развёрнутая в
// объект вызывающим кодом.
func asRuntimeObject(v any) *runtime.Object {
	switch t := v.(type) {
	case *runtime.Object:
		return t
	case XDTOObjectSource:
		return t.XDTOObject()
	}
	return nil
}

// optionsOf восстанавливает пометку удаления и проведённость: в реквизитах
// объекта их нет, но у записи, прочитанной из БД, они приезжают служебными
// ключами вместе с id и версией.
func optionsOf(obj *runtime.Object) xdto.Options {
	return xdto.Options{
		DeletionMark: flagValue(obj.Fields["deletion_mark"]),
		Posted:       flagValue(obj.Fields["posted"]),
	}
}

// flagValue читает служебный признак записи. Отдельно от общего truthy пакета:
// тот считает истиной любую непустую строку, а здесь строка приходит из БД и
// «false» обязана остаться ложью.
func flagValue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t == "true" || t == "1"
	}
	return false
}
