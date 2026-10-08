package dbcheck

import (
	"context"
	"fmt"
)

// ── Движения непроведённых документов ────────────────────────────────────────

type unpostedMovementsCheck struct{}

func (unpostedMovementsCheck) Name() string { return "unposted-movements" }
func (unpostedMovementsCheck) Title() string {
	return "Движения непроведённых документов"
}
func (unpostedMovementsCheck) CanFix() bool { return true }

// Проводимый документ со снятым признаком проведения не должен иметь движений:
// отмена проведения и пометка удаления снимают их вместе с признаком. Если
// признак снял путь, который движений не трогал, остатки считают документ,
// которого в списке проведённых нет, а проверка сирот его не видит — регистратор
// жив. Так было с загрузкой пакета обмена: приёмник ставил «не проведён», а
// движения прежнего перепроведения оставались.
func (c unpostedMovementsCheck) Run(ctx context.Context, env *Env) Result {
	stats, err := env.DB.UnpostedRecorderMovements(ctx, env.Registers, env.AccountRegisters, env.Entities)
	if err != nil {
		return failed(c, err)
	}
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	total := 0
	for _, s := range stats {
		total += s.Count
		res.Findings = append(res.Findings, Finding{
			Object: s.RegisterName,
			Detail: "документ «" + s.RecorderType + "» не проведён, а его движения в регистре",
			Count:  s.Count,
		})
	}
	if total == 0 {
		return ok(c, "движений непроведённых документов нет")
	}
	res.Severity = SeverityError
	res.Summary = fmt.Sprintf("движений непроведённых документов: %d", total)
	res.FixHint = "onebase doctor --fix unposted-movements — удалить их и пересчитать итоги; " +
		"если документ на самом деле должен быть проведён, перепроведите его"
	return res
}

// Fix удаляет движения непроведённых документов и пересчитывает итоги в одной
// транзакции: признак проведения — то, что видит пользователь, и остатки
// приводятся к нему.
func (c unpostedMovementsCheck) Fix(ctx context.Context, env *Env, _ Result) (int, error) {
	deleted, err := env.DB.DeleteUnpostedRecorderMovementsAndRecalcTotals(
		ctx, env.Registers, env.AccountRegisters, env.Entities,
	)
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}
