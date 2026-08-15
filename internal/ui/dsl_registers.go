package ui

import (
	"context"
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
// Чтение использует существующий storage API (GetBalances/GetMovements/
// GetDocumentMovements). Запись наборов записей и параметры периода/отбора у
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
		id, ok := recorderID(args[0])
		if !ok {
			return rowsToArray(nil)
		}
		byReg, err := p.s.store.GetDocumentMovements(p.ctx(), id, []*metadata.Register{p.reg})
		if err != nil {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): " + err.Error())
		}
		rows, err := p.filterRows(byReg[p.reg.Name])
		if err != nil {
			interpreter.RaiseUserError("ВыбратьПоРегистратору(" + p.reg.Name + "): " + err.Error())
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

func (p *accumRegProxy) filterRows(rows []map[string]any) ([]map[string]any, error) {
	filter, err := p.rowFilter()
	if err != nil || filter.RowFilter == nil {
		return rows, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if p.s.matchRowPredicate(p.ctx(), row, filter.RowFilter) {
			out = append(out, row)
		}
	}
	return out, nil
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

// recorderID извлекает UUID документа-регистратора из ссылки или строки.
func recorderID(v any) (uuid.UUID, bool) {
	switch x := v.(type) {
	case *interpreter.Ref:
		if id, err := uuid.Parse(x.UUID); err == nil {
			return id, true
		}
	case string:
		if id, err := uuid.Parse(x); err == nil {
			return id, true
		}
	}
	return uuid.UUID{}, false
}
