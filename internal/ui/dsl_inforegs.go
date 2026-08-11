package ui

import (
	"context"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// infoRegsRoot — DSL-глобал РегистрыСведений / InfoRegisters (план 119A).
//
//	Запись = РегистрыСведений.СостояниеОбменов.СоздатьМенеджерЗаписи();
//	Запись.Узел = Узел;                     // измерение
//	Запись.Состояние = "Готов";             // ресурс
//	Запись.Записать();
//
//	Запись.Прочитать();                     // заполняет ресурсы по ключу
//	Если Запись.Выбран() Тогда … КонецЕсли;
//	Запись.Удалить();
//
// До этого записать регистр сведений из конфигурации было нечем: движения
// документа умеют только подчинённые регистратору, а независимый заполнялся
// руками через форму или обменом. Читать при этом можно было всегда
// (РегистрСведений.X.СрезПоследних в языке запросов) — асимметрия по записи.
type infoRegsRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newInfoRegsRoot(s *Server, ctxSrc docsCtxSource) *infoRegsRoot {
	return &infoRegsRoot{s: s, ctxSrc: ctxSrc}
}

func (r *infoRegsRoot) Get(name string) any {
	ir := r.s.reg.GetInfoRegister(name)
	if ir == nil {
		return nil
	}
	return &infoRegProxy{s: r.s, ctxSrc: r.ctxSrc, ir: ir}
}

func (r *infoRegsRoot) Set(_ string, _ any) {}

type infoRegProxy struct {
	s      *Server
	ctxSrc docsCtxSource
	ir     *metadata.InfoRegister
}

func (p *infoRegProxy) Get(_ string) any    { return nil }
func (p *infoRegProxy) Set(_ string, _ any) {}

func (p *infoRegProxy) ctx() context.Context {
	if p.ctxSrc != nil {
		return p.ctxSrc.Ctx()
	}
	return context.Background()
}

func (p *infoRegProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "создатьменеджерзаписи", "createrecordmanager":
		// Регистр, подчинённый регистратору, программной записью не трогаем:
		// его строки принадлежат проведению документа, и ближайшее
		// перепроведение снесло бы записанное без предупреждения. Отказ на
		// создании менеджера, а не на Записать(), — ошибка приходит там, где
		// написана неверная строка.
		if p.ir.Recorder {
			interpreter.RaiseUserError("РегистрыСведений." + p.ir.Name +
				": регистр подчинён регистратору, пишите движениями в ОбработкаПроведения документа")
		}
		return newInfoRegRecord(p.s, p.ctxSrc, p.ir)
	}
	interpreter.RaiseUserError("РегистрыСведений." + p.ir.Name + ": неизвестный метод «" + method +
		"» (доступен СоздатьМенеджерЗаписи)")
	return nil
}

// infoRegRecord — менеджер записи. Значения измерений/ресурсов/периода
// набираются присваиванием, как у объекта документа.
type infoRegRecord struct {
	s        *Server
	ctxSrc   docsCtxSource
	ir       *metadata.InfoRegister
	values   map[string]any
	period   *time.Time
	selected bool // Прочитать() нашла запись
}

func newInfoRegRecord(s *Server, ctxSrc docsCtxSource, ir *metadata.InfoRegister) *infoRegRecord {
	return &infoRegRecord{s: s, ctxSrc: ctxSrc, ir: ir, values: map[string]any{}}
}

func (r *infoRegRecord) ctx() context.Context {
	if r.ctxSrc != nil {
		return r.ctxSrc.Ctx()
	}
	return context.Background()
}

func (r *infoRegRecord) Get(name string) any {
	if isPeriodName(name) {
		if r.period == nil {
			return time.Time{}
		}
		return *r.period
	}
	if f := infoRegField(r.ir, name); f != nil {
		return r.values[f.Name]
	}
	return nil
}

func (r *infoRegRecord) Set(name string, v any) {
	if isPeriodName(name) {
		if t, ok := v.(time.Time); ok {
			r.period = &t
			return
		}
		if v == nil {
			r.period = nil
			return
		}
		interpreter.RaiseUserError("РегистрыСведений." + r.ir.Name + ".Период: ожидается дата")
		return
	}
	f := infoRegField(r.ir, name)
	if f == nil {
		// Опечатка в имени измерения тихо ушла бы в никуда, а запись легла бы
		// с пустым ключом — то есть не туда, где её потом ищут.
		interpreter.RaiseUserError("РегистрыСведений." + r.ir.Name + ": нет измерения или ресурса «" + name + "»")
		return
	}
	r.values[f.Name] = v
}

func (r *infoRegRecord) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "записать", "write":
		dims, res := r.split()
		if err := r.s.infoRegWrite(r.ctx(), r.ir, dims, res, r.period, r.access("write")); err != nil {
			interpreter.RaiseUserError("Записать(" + r.ir.Name + "): " + err.Error())
		}
		r.selected = true
		return nil
	case "прочитать", "read":
		dims, _ := r.split()
		row, err := r.s.store.InfoRegGetExact(r.ctx(), r.ir, dims, r.period)
		if err != nil {
			interpreter.RaiseUserError("Прочитать(" + r.ir.Name + "): " + err.Error())
		}
		if row == nil {
			r.selected = false
			return false
		}
		if err := r.checkRead(row); err != nil {
			interpreter.RaiseUserError("Прочитать(" + r.ir.Name + "): " + err.Error())
		}
		for _, f := range r.ir.Resources {
			if v, ok := row[f.Name]; ok {
				r.values[f.Name] = v
			}
		}
		r.selected = true
		return true
	case "удалить", "delete":
		dims, _ := r.split()
		if err := r.s.infoRegRemove(r.ctx(), r.ir, dims, r.period, r.access("delete")); err != nil {
			interpreter.RaiseUserError("Удалить(" + r.ir.Name + "): " + err.Error())
		}
		r.selected = false
		return nil
	case "выбран", "selected":
		return r.selected
	}
	interpreter.RaiseUserError("МенеджерЗаписи(" + r.ir.Name + "): неизвестный метод «" + method +
		"» (доступны Записать, Прочитать, Удалить, Выбран)")
	return nil
}

// split разделяет набранные значения на измерения и ресурсы.
func (r *infoRegRecord) split() (dims, resources map[string]any) {
	dims = make(map[string]any, len(r.ir.Dimensions))
	resources = make(map[string]any, len(r.ir.Resources))
	for _, f := range r.ir.Dimensions {
		dims[f.Name] = r.values[f.Name]
	}
	for _, f := range r.ir.Resources {
		resources[f.Name] = r.values[f.Name]
	}
	return dims, resources
}

// access отдаёт проверку строковой политики для записи/удаления. В доверенном
// контексте (проведение, миграции) проверка не нужна — как у регистров
// накопления.
func (r *infoRegRecord) access(op string) infoRegAccess {
	if isTrustedDSLContext(r.ctx()) {
		return nil
	}
	return func(ctx context.Context, row map[string]any) error {
		dec, err := r.s.rowDecisionFor(ctx, "inforeg", r.ir.Name, op,
			storage.InfoRegisterPredicateEntity(r.ir))
		if err != nil {
			return err
		}
		if !dec.Allowed {
			return interpreter.ErrRowAccessDenied
		}
		if dec.Unrestricted || dec.Predicate == nil {
			return nil
		}
		if !r.s.matchRowPredicate(ctx, row, dec.Predicate) {
			return interpreter.ErrRowAccessDenied
		}
		return nil
	}
}

// checkRead применяет строковую политику к прочитанной записи: чтение через
// объект не должно показывать то, что скрыто в списке и в запросах.
func (r *infoRegRecord) checkRead(row map[string]any) error {
	allow := r.access("read")
	if allow == nil {
		return nil
	}
	return allow(r.ctx(), row)
}

func infoRegField(ir *metadata.InfoRegister, name string) *metadata.Field {
	for i := range ir.Dimensions {
		if strings.EqualFold(ir.Dimensions[i].Name, name) {
			return &ir.Dimensions[i]
		}
	}
	for i := range ir.Resources {
		if strings.EqualFold(ir.Resources[i].Name, name) {
			return &ir.Resources[i]
		}
	}
	return nil
}

func isPeriodName(name string) bool {
	low := strings.ToLower(name)
	return low == "период" || low == "period"
}
