package dbcheck

// Проверка итогов регистра бухгалтерии (стык планов 114 и 80).
//
// totalsCheck сверяет итоги регистров НАКОПЛЕНИЯ, но кэш итогов бухрегистра
// (итоги_акк_*) он не покрывал вовсе — а Остатки()/Обороты() бухрегистра теперь
// читают именно его, так что разъехавшийся кэш даёт неверную отчётность молча
// (#613). Здесь — парная проверка с той же семантикой и тем же средством
// починки (RecalcAccountRegisterTotals).

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

type accountTotalsCheck struct{}

func (accountTotalsCheck) Name() string  { return "account-totals" }
func (accountTotalsCheck) Title() string { return "Итоги регистра бухгалтерии" }
func (accountTotalsCheck) CanFix() bool  { return true }

// Итоги бухрегистра строятся разворотом КАЖДОЙ проводки на две половины —
// в дебет счётдт и в кредит счёткт — безусловно (storage.insertAccountTotalsSelectSQL
// с пустыми условиями при полном пересчёте). Поэтому глобально сумма ресурса по
// проводкам обязана совпасть И с суммой дебетового оборота в итогах, И с суммой
// кредитового: каждая строка проводки кладёт ресурс ровно раз в каждую половину.
// Группировка по (счёт, субконто, месяц) на эти глобальные суммы не влияет, так
// что разворот по субконто разбирать не нужно — расхождение он бы не скрыл.
func (c accountTotalsCheck) Run(ctx context.Context, env *Env) Result {
	res := Result{Check: c.Name(), Title: c.Title(), Severity: SeverityOK}
	checked := 0
	for _, ar := range env.AccountRegisters {
		if !ar.TotalsUsable() {
			continue
		}
		src := metadata.AccountRegTableName(ar.Name)
		totals := metadata.AccountRegTotalsTableName(ar.Name)
		if !env.DB.HasTable(ctx, src) || !env.DB.HasTable(ctx, totals) {
			continue
		}
		for _, r := range ar.Resources {
			checked++
			// Сырая колонка ресурса на SQLite — TEXT, поэтому CAST обязателен по
			// обе стороны сверки (тот же приём, что в totalsCheck).
			col := metadata.ColumnName(r)
			moveExpr := "SUM(CAST(" + quoteIdent(col) + " AS NUMERIC))"
			// Колонки оборотов в итогах — <ресурс>_дт и <ресурс>_кт (зеркалят
			// storage.accountTotalsDebitCol/CreditCol).
			debitExpr := "SUM(CAST(" + quoteIdent(col+"_дт") + " AS NUMERIC))"
			creditExpr := "SUM(CAST(" + quoteIdent(col+"_кт") + " AS NUMERIC))"

			fromMoves, err := sumExpr(ctx, env, src, moveExpr)
			if err != nil {
				return failed(c, err)
			}
			debit, err := sumExpr(ctx, env, totals, debitExpr)
			if err != nil {
				return failed(c, err)
			}
			credit, err := sumExpr(ctx, env, totals, creditExpr)
			if err != nil {
				return failed(c, err)
			}
			if fromMoves.Equal(debit) && fromMoves.Equal(credit) {
				continue
			}
			res.Findings = append(res.Findings, Finding{
				Object: ar.Name + "." + r.Name,
				Detail: fmt.Sprintf("проводки %s, итоги Дт %s / Кт %s",
					fromMoves.String(), debit.String(), credit.String()),
			})
		}
	}
	if len(res.Findings) == 0 {
		return ok(c, fmt.Sprintf("проверено ресурсов: %d, итоги сходятся с проводками", checked))
	}
	res.Severity = SeverityError
	res.Summary = fmt.Sprintf("итоги бухрегистра расходятся с проводками: %d ресурс(ов)", len(res.Findings))
	res.FixHint = "onebase doctor --fix account-totals (то же делает onebase recalc-totals)"
	return res
}

func (c accountTotalsCheck) Fix(ctx context.Context, env *Env, res Result) (int, error) {
	// Пересчитываем только бухрегистры с находками — полный пересчёт по всей
	// базе просить незачем (та же логика, что у totalsCheck).
	broken := map[string]bool{}
	for _, f := range res.Findings {
		broken[strings.ToLower(strings.SplitN(f.Object, ".", 2)[0])] = true
	}
	fixed := 0
	for _, ar := range env.AccountRegisters {
		if !ar.TotalsUsable() || !broken[strings.ToLower(ar.Name)] {
			continue
		}
		if err := env.DB.RecalcAccountRegisterTotals(ctx, ar); err != nil {
			return fixed, fmt.Errorf("пересчёт итогов бухрегистра %s: %w", ar.Name, err)
		}
		fixed++
	}
	return fixed, nil
}
