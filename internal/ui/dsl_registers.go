package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// accumRegsRoot — DSL-глобал РегистрыНакопления / AccumulationRegisters.
//
//	РегистрыНакопления.ОстаткиТоваров.Остатки()              → Массив строк остатков
//	РегистрыНакопления.ОстаткиТоваров.Движения()             → все движения
//	РегистрыНакопления.ОстаткиТоваров.ВыбратьПоРегистратору(Док) → движения документа
//
// Чтение использует существующий storage API (GetBalances/GetMovements).
// Запись наборов записей и параметры периода/отбора у
// Остатки()/Обороты() — следующий шаг (см. roadmap, write-side).
type accumRegsRoot struct {
	s      *Server
	ctxSrc docsCtxSource
}

func newAccumRegsRoot(s *Server, ctxSrc docsCtxSource) *accumRegsRoot {
	return &accumRegsRoot{s: s, ctxSrc: ctxSrc}
}

func (r *accumRegsRoot) Get(name string) any {
	reg := r.s.reg.GetRegister(name)
	if reg == nil {
		return nil
	}
	return &accumRegProxy{s: r.s, ctxSrc: r.ctxSrc, reg: reg}
}

func (r *accumRegsRoot) Set(_ string, _ any) {}

type accumRegProxy struct {
	s      *Server
	ctxSrc docsCtxSource
	reg    *metadata.Register
}

func (p *accumRegProxy) Get(_ string) any    { return nil }
func (p *accumRegProxy) Set(_ string, _ any) {}

func (p *accumRegProxy) ctx() context.Context {
	if p.ctxSrc != nil {
		return p.ctxSrc.Ctx()
	}
	return context.Background()
}

func (p *accumRegProxy) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "остатки", "balances":
		if registerBalancesProtected(p.s.registerFieldDecisions(p.ctx(), p.reg), p.reg) {
			interpreter.RaiseUserError("Остатки(" + p.reg.Name + "): защищённое поле нельзя использовать для группировки или расчёта")
		}
		filter, err := p.rowFilter()
		if err != nil {
			interpreter.RaiseUserError("Остатки(" + p.reg.Name + "): " + err.Error())
		}
		rows, err := p.s.store.GetBalances(p.ctx(), p.reg.Name, p.reg, filter)
		if err != nil {
			interpreter.RaiseUserError("Остатки(" + p.reg.Name + "): " + err.Error())
		}
		// Та же маска полей, что в списках UI (#859): политика на регистр не
		// должна зависеть от того, читают его глазами или из модуля.
		p.s.maskRegisterRecords(p.ctx(), p.reg, rows)
		return rowsToArray(rows)
	case "движения", "выбрать", "select":
		filter, err := p.rowFilter()
		if err != nil {
			interpreter.RaiseUserError("Движения(" + p.reg.Name + "): " + err.Error())
		}
		rows, err := p.s.store.GetMovements(p.ctx(), p.reg.Name, p.reg, filter)
		if err != nil {
			interpreter.RaiseUserError("Движения(" + p.reg.Name + "): " + err.Error())
		}
		p.s.maskRegisterRecords(p.ctx(), p.reg, rows)
		return rowsToArray(rows)
	case "выбратьпорегистратору", "selectbyrecorder":
		if len(args) == 0 {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): не передан регистратор")
		}
		decisions := p.s.registerFieldDecisions(p.ctx(), p.reg)
		if registerFieldProtected(decisions, "recorder") || registerFieldProtected(decisions, "recorder_type") {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): защищённый регистратор нельзя использовать для отбора")
		}
		filter, err := p.rowFilter()
		if err != nil {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): " + err.Error())
		}
		id, recorderType, err := recorderIdentity(args[0])
		if err != nil {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): " + err.Error())
		}
		recorderEntity := p.s.reg.GetEntity(recorderType)
		if recorderEntity == nil || recorderEntity.Kind != metadata.KindDocument {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): ссылка должна указывать на известный тип документа")
		}
		// Ref.Type is normally the canonical metadata name, but GetEntity also
		// accepts case-insensitive/sluggified names. Always filter by the stored
		// canonical recorder_type after resolving it.
		filter = registerRecorderFilter(filter, id, recorderEntity.Name)
		rows, err := p.s.store.GetMovements(p.ctx(), p.reg.Name, p.reg, filter)
		if err != nil {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): " + err.Error())
		}
		for _, row := range rows {
			delete(row, "recorder")
			delete(row, "recorder_type")
		}
		p.s.maskRegisterRecords(p.ctx(), p.reg, rows)
		return rowsToArray(rows)
	}
	return nil
}

func (p *accumRegProxy) rowFilter() (storage.RegFilter, error) {
	if isTrustedDSLContext(p.ctx()) {
		return storage.RegFilter{}, nil
	}
	dec, err := p.s.rowDecisionFor(p.ctx(), "register", p.reg.Name, "read", storage.RegisterPredicateEntity(p.reg))
	if err != nil {
		return storage.RegFilter{}, err
	}
	if !dec.Allowed {
		return storage.RegFilter{}, interpreter.ErrRowAccessDenied
	}
	if dec.Unrestricted {
		return storage.RegFilter{}, nil
	}
	return storage.RegFilter{RowFilter: dec.Predicate}, nil
}

// rowsToArray оборачивает строки движений/остатков в Массив строк (*MapThis),
// чтобы в DSL работали Количество()/Получить()/«Для Каждого» и Стр.Колонка.
func rowsToArray(rows []map[string]any) *interpreter.Array {
	items := make([]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, &interpreter.MapThis{M: r})
	}
	return interpreter.NewArray(items)
}

// recorderIdentity accepts only a typed DSL reference and extracts its UUID
// and document type. A bare UUID cannot identify a registrar on its own.
// Тип не декоративный: разные таблицы документов могут содержать одинаковый
// UUID, а движение идентифицирует регистратора парой (recorder_type, recorder).
func recorderIdentity(v any) (uuid.UUID, string, error) {
	ref, ok := v.(*interpreter.Ref)
	if !ok || ref == nil {
		return uuid.UUID{}, "", fmt.Errorf("ожидается типизированная ссылка на документ; UUID-строка неоднозначна")
	}
	recorderType := strings.TrimSpace(ref.Type)
	if recorderType == "" {
		return uuid.UUID{}, "", fmt.Errorf("у ссылки не указан тип документа")
	}
	id, err := uuid.Parse(ref.UUID)
	if err != nil {
		return uuid.UUID{}, "", fmt.Errorf("некорректный UUID регистратора")
	}
	return id, recorderType, nil
}
