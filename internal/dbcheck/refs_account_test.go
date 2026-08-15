package dbcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Битая ссылка в субконто бухрегистра (#910, остаток от #881).
//
// Внешних ключей у колонок регистра бухгалтерии нет, и проверка ссылочной
// целостности обходила его стороной, хотя субконто хранит ровно такие же
// ссылки: удалённый прямым SQL контрагент оставался в проводках, а показать
// это было нечем.
//
// Битую ссылку вставляем в обход FK — так она и появляется в жизни: прямой
// SQL, восстановление части копии, приехавший обменом узел.
func insertBrokenSubconto(t *testing.T, env *Env) string {
	t.Helper()
	ctx := context.Background()
	broken := uuid.New().String()
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO акк_бухучёт (id, period, регистратор, регистратор_тип, счётдт, счёткт, сумма, субконто1)
		 VALUES (?, '2026-01-15T00:00:00Z', ?, 'Реализация', '50', '51', '100', ?)`,
		uuid.New().String(), uuid.New().String(), broken); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	return broken
}

func TestRefs_СубконтоБухрегистраПопадаетВОтчёт(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)
	insertBrokenSubconto(t, env)

	rep := Run(ctx, env, []Check{refsCheck{}}, nil)
	res := findResult(t, rep, "refs")
	if res.Severity != SeverityError {
		t.Fatalf("битая ссылка в субконто не найдена: %+v", res)
	}
	var found bool
	for _, f := range res.Findings {
		if strings.Contains(f.Object, "БухУчёт.Контрагент") {
			found = true
		}
	}
	if !found {
		t.Fatalf("в отчёте нет субконто бухрегистра: %+v", res.Findings)
	}
}

// Автоочистка субконто не трогает: занулить его технически можно, но это меняет
// аналитику уже проведённой операции. doctor обязан сказать об этом вслух, а не
// молча оставить строку «починенной».
func TestRefs_СубконтоНеЗануляетсяАвтоматически(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)
	broken := insertBrokenSubconto(t, env)

	rep := Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true})
	res := findResult(t, rep, "refs")
	if res.Error == "" || !strings.Contains(res.Error, "субконто") {
		t.Fatalf("починка промолчала про субконто: %q", res.Error)
	}
	var left int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM акк_бухучёт WHERE субконто1 = ?`, broken).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("проводка изменена автоочисткой: строк с битым субконто %d", left)
	}
}
