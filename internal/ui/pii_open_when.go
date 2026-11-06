package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// recordFieldDecisions — решения маски для формы ОДНОЙ записи. Поле с
// pii_open_when, условие которого истинно по СОХРАНЁННОЙ записи stored,
// открыто: в форме оно показывается и записывается настоящим значением, как
// в 1С сокрытие личных данных заявки действует только вне оформления.
//
// stored — строка из хранилища, а не присланное браузером: подменой статуса в
// запросе поле не раскрыть. Поле, которое роль прячет целиком (hide),
// условие не открывает. Ошибка вычисления оставляет маску (fail-closed).
//
// Только для формы объекта: списки, отчёты, печать, REST и запросы DSL
// по-прежнему получают fieldDecisions без условия.
func (s *Server) recordFieldDecisions(ctx context.Context, entity *metadata.Entity, stored map[string]any) map[string]access.FieldDecision {
	return s.openPIIByRecord(entity, s.fieldDecisions(ctx, entity), stored)
}

// openPIIByRecord снимает с decisions поля, открытые условием pii_open_when на
// записи stored. Исходная карта не меняется.
func (s *Server) openPIIByRecord(entity *metadata.Entity, decisions map[string]access.FieldDecision, stored map[string]any) map[string]access.FieldDecision {
	if len(decisions) == 0 || entity == nil || stored == nil || s.interp == nil {
		return decisions
	}
	var row compose.Row
	var opened map[string]bool
	for _, f := range entity.Fields {
		if !f.PII || f.PIIOpenWhen == "" {
			continue
		}
		for key, decision := range decisions {
			if !strings.EqualFold(key, f.Name) || !decision.Masked() || decision.Hidden() {
				continue
			}
			if row == nil {
				row = compose.Row(managedFormHeaderValues(entity, stored))
			}
			ok, err := newInterpEvaluator(s.interp).EvalBool(f.PIIOpenWhen, row)
			if err != nil {
				uiLog().Warn("pii_open_when не вычислено — поле остаётся под маской",
					"entity", entity.Name, "field", f.Name, "err", err)
				continue
			}
			if ok {
				if opened == nil {
					opened = map[string]bool{}
				}
				opened[key] = true
			}
		}
	}
	if len(opened) == 0 {
		return decisions
	}
	out := make(map[string]access.FieldDecision, len(decisions))
	for key, decision := range decisions {
		if !opened[key] {
			out[key] = decision
		}
	}
	return out
}

// entityHasPIIOpenWhen — есть ли у сущности хоть одно условие раскрытия: без
// него форме не нужно лишний раз читать сохранённую запись.
func entityHasPIIOpenWhen(entity *metadata.Entity) bool {
	if entity == nil {
		return false
	}
	for _, f := range entity.Fields {
		if f.PII && f.PIIOpenWhen != "" {
			return true
		}
	}
	return false
}
