package dbcheck

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Знак движения в сверке итогов.
//
// Таблица итогов заполняется ЗНАКОВОЙ суммой (storage.SignedResourceSum):
// приход плюсом, расход минусом. Сверка обязана суммировать движения тем же
// выражением — иначе на любом регистре с расходом она объявляет ошибкой
// исправную базу: итоги 100−30=70 против «движений» 100+30=130.
//
// Цена такой ложной тревоги высока: doctor выходит с ненулевым кодом, а
// --fix totals её не снимает — пересчёт даёт те же (правильные) итоги, проверка
// перезапускается и снова красная. Администратору остаётся либо перестать
// доверять диагностике, либо «чинить» здоровую базу.

// movement вписывает одно движение регистра Остатки.
func movement(t *testing.T, env *Env, doc uuid.UUID, ctr uuid.UUID, line int, kind, sum string) {
	t.Helper()
	if _, err := env.DB.Exec(context.Background(),
		`INSERT INTO рег_остатки (id, recorder, recorder_type, line_number, period, вид_движения, контрагент_id, сумма)
		 VALUES (?, ?, 'Реализация', ?, '2026-01-15T00:00:00Z', ?, ?, ?)`,
		uuid.New().String(), doc.String(), line, kind, ctr.String(), sum); err != nil {
		t.Fatal(err)
	}
}

// seedIncomeAndExpense готовит обычную жизнь регистра остатков: приход и расход
// по одному контрагенту, итоги пересчитаны самой платформой.
func seedIncomeAndExpense(t *testing.T, env *Env) {
	t.Helper()
	ctx := context.Background()
	ctr := uuid.New()
	if _, err := env.DB.Exec(ctx, `INSERT INTO контрагенты (id, наименование) VALUES (?, ?)`,
		ctr.String(), "Живой"); err != nil {
		t.Fatal(err)
	}
	doc := uuid.New()
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO реализация (id, контрагент_id, сумма) VALUES (?, ?, ?)`,
		doc.String(), ctr.String(), "100"); err != nil {
		t.Fatal(err)
	}
	movement(t, env, doc, ctr, 1, "Приход", "100")
	movement(t, env, doc, ctr, 2, "Расход", "30")
	for _, reg := range env.Registers {
		if err := env.DB.RecalcRegisterTotals(ctx, reg); err != nil {
			t.Fatal(err)
		}
	}
}

// Здоровая база с расходом: проверка обязана молчать. Без учёта знака здесь
// «итоги 70, движения 130» и severity=error.
func TestTotalsCheckIgnoresNothingWhenSignsMatch(t *testing.T) {
	env := testEnv(t)
	seedIncomeAndExpense(t, env)

	res := totalsCheck{}.Run(context.Background(), env)
	if res.Severity != SeverityOK {
		t.Fatalf("ложная тревога на здоровой базе: %s / %s (находки: %+v)",
			res.Severity, res.Summary, res.Findings)
	}
}

// Обратная сторона: настоящее расхождение проверка по-прежнему видит. Иначе
// «починку» знака можно было бы изобразить, просто перестав сравнивать.
func TestTotalsCheckStillFindsRealMismatch(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)
	seedIncomeAndExpense(t, env)

	// Портим итоги мимо пересчёта — ровно то, ради чего проверка и написана.
	if _, err := env.DB.Exec(ctx, `UPDATE итоги_остатки SET сумма = сумма + 5`); err != nil {
		t.Fatal(err)
	}
	res := totalsCheck{}.Run(ctx, env)
	if res.Severity != SeverityError {
		t.Fatalf("испорченные итоги не найдены: %s / %s", res.Severity, res.Summary)
	}

	// И чинятся: после пересчёта проверка снова чистая.
	n, err := (totalsCheck{}).Fix(ctx, env, res)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("пересчитано регистров: %d, ожидался 1", n)
	}
	if after := (totalsCheck{}).Run(ctx, env); after.Severity != SeverityOK {
		t.Fatalf("после починки проверка осталась красной: %s / %s", after.Severity, after.Summary)
	}
}
