package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
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
	// Запись объекта прямо из обработчика формы (Объект.Записать(), аналог
	// ЗаписатьНаСервере в 1С). Без неё команда на ещё не записанной форме
	// упиралась в пустую Ссылка и требовала от пользователя сначала нажать
	// «Записать» — чего в управляемой форме быть не должно.
	srv *Server
	ctx context.Context
	// ctxSrc — «живой» контекст DSL-исполнения: собственная запись объекта тоже
	// должна попадать в открытую модулем транзакцию, а не ждать второго
	// соединения (пул SQLite — одно).
	ctxSrc docsCtxSource
	isNew  bool
	saved  bool
	// writeBlocked prevents a form write lifecycle handler from recursively
	// saving the same object and then letting the outer Save persist it again.
	writeBlocked bool
}

// liveCtx — контекст с открытой DSL-транзакцией, если она есть.
func (f *formObjectThis) liveCtx() context.Context {
	if f.ctxSrc != nil {
		if ctx := f.ctxSrc.Ctx(); ctx != nil {
			return ctx
		}
	}
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

// GetRefUUID сохраняет ссылочную идентичность runtime.Object у формовой
// обёртки. Storage использует этот контракт при записи this/ЭтотОбъект в
// reference-поля сущностей и регистров.
func (f *formObjectThis) GetRefUUID() string {
	if f == nil || f.obj == nil {
		return ""
	}
	return f.obj.GetRefUUID()
}

// String сохраняет строковое представление runtime.Object. В частности, это
// позволяет писать this/ЭтотОбъект в строковые атрибуты регистра так же, как
// до оборачивания объекта для управляемой формы.
func (f *formObjectThis) String() string {
	if f == nil || f.obj == nil {
		return ""
	}
	return f.obj.String()
}

// TypeName делегирует имя типа обёрнутому объекту, чтобы
// ТипЗнч(ЭтотОбъект) в обработчике формы называл «Документ.X», а не
// Go-имя обёртки (issue #1137).
func (f *formObjectThis) TypeName() string {
	if f == nil || f.obj == nil {
		return "Неопределено"
	}
	return f.obj.TypeName()
}

// CallMethod делегирует объектные методы (например, МоментВремени) исходному
// runtime.Object. Специальное поведение формовой обёртки относится к Get/Set и
// табличным частям, остальные возможности объекта должны оставаться доступны.
func (f *formObjectThis) CallMethod(method string, args []any) any {
	if f == nil || f.obj == nil {
		return nil
	}
	switch strings.ToLower(method) {
	case "записать", "write":
		if f.writeBlocked {
			interpreter.RaiseUserError("Записать недоступно внутри обработчика записи формы")
		}
		if err := f.write(); err != nil {
			interpreter.RaiseUserError("Записать(" + f.entity.Name + "): " + err.Error())
		}
		return f.selfRef()
	case "этоновый", "isnew":
		return f.isNew && !f.saved
	}
	return f.obj.CallMethod(method, args)
}

// write сохраняет объект формы через entityservice.Save — тем же путём, что и
// кнопка «Записать»: с хуками ПриЗаписи/ОбработкаПроведения, табличными частями
// и проверкой построчного доступа. Нужна обработчикам команд: на новой форме
// они иначе упирались в незаполненную Ссылка.
func (f *formObjectThis) write() error {
	if f.srv == nil || f.entity == nil {
		return fmt.Errorf("запись из обработчика формы недоступна")
	}
	ctx := f.liveCtx()
	isNew := f.isNew && !f.saved
	if !isNew {
		if err := f.srv.checkDSLRowAccess(ctx, f.entity, "write", f.obj.ID, f.obj.Fields); err != nil {
			return err
		}
	}
	result, err := f.srv.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity:        f.entity,
		ID:            f.obj.ID,
		IsNew:         isNew,
		Fields:        f.obj.Fields,
		TablePartRows: f.obj.TablePartRows,
		Preflight: func(txCtx context.Context, obj *runtime.Object) error {
			if !isNew {
				return nil
			}
			if err := f.srv.autoFillRowAccessFields(txCtx, f.entity, "write", obj.Fields); err != nil {
				return err
			}
			return f.srv.checkDSLRowAccess(txCtx, f.entity, "write", uuid.Nil, obj.Fields)
		},
	})
	if err != nil {
		return err
	}
	if result.DSLError != "" {
		return fmt.Errorf("%s", result.DSLError)
	}
	wasSaved := f.saved
	f.saved = true
	// Ссылка появляется только после записи — до неё её нет ни в объекте, ни
	// у резолвера. Ставим здесь же, чтобы следующая строка обработчика могла
	// сразу писать Модуль.Действие(Объект.Ссылка).
	f.obj.Fields["ссылка"] = f.selfRef()
	f.obj.Fields["reference"] = f.obj.Fields["ссылка"]
	storage.DeferUntilTxRollback(ctx, func() { f.saved = wasSaved })
	return nil
}

func (f *formObjectThis) selfRef() *interpreter.Ref {
	ref := &interpreter.Ref{UUID: f.obj.ID.String(), Type: f.entity.Name, Kind: f.entity.Kind}
	if f.refResolver != nil {
		return f.refResolver.bindRefToContext(ref, f.entity.Name)
	}
	return ref
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
				return &formTpProxy{
					obj: f.obj, tpName: attr.Name,
					tp: formAttributeTablePart(attr), refResolver: f.refResolver,
				}
			}
		}
	}
	// Дальше — обычные поля (через Object.Get который ищет в Fields).
	v := f.obj.Get(name)
	if ref, ok := v.(*interpreter.Ref); ok && f.refResolver != nil {
		if fd := entityField(f.entity, name); fd != nil && fd.RefEntity != "" {
			return f.refResolver.bindRefToContext(ref, fd.RefEntity)
		}
		if f.entity != nil && (strings.EqualFold(name, "Ссылка") || strings.EqualFold(name, "Reference")) {
			return f.refResolver.bindRefToContext(ref, f.entity.Name)
		}
		// Ссылочный реквизит формы (save:false): его нет в entity.Fields, поэтому
		// без привязки к резолверу у ссылки не работали ни .Код/.Наименование,
		// ни ПолучитьОбъект() — читалось Неопределено.
		if refName := formAttrRefEntity(f.form, name); refName != "" {
			return f.refResolver.bindRefToContext(ref, refName)
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

// formAttributeTablePart даёт ValueTable тот же metadata-aware row proxy, что
// и обычной табличной части. Persisted TablePart для неё не существует, но для
// канонизации имён колонок и ссылочных значений достаточно временного описания.
func formAttributeTablePart(attr *metadata.FormAttribute) *metadata.TablePart {
	if attr == nil {
		return nil
	}
	tp := &metadata.TablePart{Name: attr.Name, Fields: make([]metadata.Field, 0, len(attr.Columns))}
	for _, column := range attr.Columns {
		if column == nil {
			continue
		}
		tp.Fields = append(tp.Fields, metadata.Field{
			Name:      column.Name,
			RefEntity: attrRefEntityName(column.TypeRef),
		})
	}
	return tp
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

// KnownMethods реализует interpreter.MethodLister — список тот же, что у
// табличной части в обработчиках и на пути записи (issue #842).
func (t *formTpProxy) KnownMethods() (string, []string) {
	return "ТабличнаяЧасть", runtime.TablePartMethods()
}

func (t *formTpProxy) CallMethod(method string, args []any) any {
	if t == nil || t.obj == nil {
		return nil
	}
	rows := t.IterateRows()
	switch strings.ToLower(method) {
	case "добавить", "add":
		if t.obj.TablePartRows == nil {
			t.obj.TablePartRows = map[string][]map[string]any{}
		}
		row := map[string]any{}
		t.obj.TablePartRows[t.tpName] = append(rows, row)
		return newRefAwareMapThis(row, t.tp, t.refResolver)
	case "очистить", "clear":
		if t.obj.TablePartRows != nil {
			t.obj.TablePartRows[t.tpName] = nil
		}
	case "количество", "count":
		return float64(len(rows))
	case "получить", "get":
		if len(args) > 0 {
			if idx := runtime.RowIndexArg(args[0]); idx >= 0 && idx < len(rows) {
				return newRefAwareMapThis(rows[idx], t.tp, t.refResolver)
			}
		}
	case "удалить", "delete":
		if len(args) > 0 {
			if idx := runtime.RowIndexArg(args[0]); idx >= 0 && idx < len(rows) {
				t.obj.TablePartRows[t.tpName] = append(rows[:idx], rows[idx+1:]...)
			}
		}
	case "итог", "total":
		if len(args) > 0 {
			return runtime.SumRowsColumn(rows, fmt.Sprintf("%v", args[0]))
		}
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

// formAttrRefEntity возвращает имя сущности, на которую ссылается одноимённый
// реквизит формы (CatalogRef.X / DocumentRef.X), либо "" — если такого реквизита
// нет или он не ссылочный.
func formAttrRefEntity(form *metadata.FormModule, name string) string {
	if form == nil {
		return ""
	}
	for _, a := range form.Attributes {
		if a != nil && strings.EqualFold(a.Name, name) {
			return attrRefEntityName(a.TypeRef)
		}
	}
	return ""
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
