package dbcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// addMovement вписывает движение с заданным типом регистратора.
func addMovement(t *testing.T, env *Env, recorderType string, recorder uuid.UUID, sum string) {
	t.Helper()
	partner := uuid.New()
	if _, err := env.DB.Exec(context.Background(),
		`INSERT INTO контрагенты (id, наименование) VALUES (?, ?)`, partner.String(), "Контрагент"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(context.Background(),
		`INSERT INTO рег_остатки (id, recorder, recorder_type, line_number, period, вид_движения, контрагент_id, сумма)
		 VALUES (?, ?, ?, 1, '2026-01-15T00:00:00Z', 'Приход', ?, ?)`,
		uuid.New().String(), recorder.String(), recorderType, partner.String(), sum); err != nil {
		t.Fatal(err)
	}
}

func movementCount(t *testing.T, env *Env) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM рег_остатки`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Регресс: движения документа, которого нет в конфигурации, НЕ удаляются.
//
// Тип регистратора хранится строкой, поэтому он расходится с конфигурацией при
// обычном переименовании документа или при запуске против другой конфигурации.
// Раньше очистка выполняла безусловный DELETE по такому типу — то есть
// переименование документа стирало всю его историю движений.
func TestOrphanMovementsKeepsUnknownRecorderType(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	addMovement(t, env, "СтароеИмяДокумента", uuid.New(), "500")
	if got := movementCount(t, env); got != 1 {
		t.Fatalf("подготовка: движений %d", got)
	}

	rep := Run(ctx, env, []Check{orphanMovementsCheck{}}, map[string]bool{"orphan-movements": true})
	res := findResult(t, rep, "orphan-movements")

	if got := movementCount(t, env); got != 1 {
		t.Fatalf("движения документа вне конфигурации удалены: осталось %d из 1", got)
	}
	if res.Severity != SeverityWarn {
		t.Errorf("ожидалось предупреждение, получено %q (%s)", res.Severity, res.Summary)
	}
	if res.Fixed != 0 {
		t.Errorf("чинить тут нечего, а Fixed=%d", res.Fixed)
	}
	if len(res.Findings) != 1 || !strings.Contains(res.Findings[0].Detail, "нет в конфигурации") {
		t.Fatalf("находка не объясняет ситуацию: %+v", res.Findings)
	}
	if !strings.Contains(res.FixHint, "переименован") {
		t.Errorf("подсказка не называет самую частую причину: %q", res.FixHint)
	}
}

// А вот движение известного документа, которого больше нет, — настоящая
// сирота: его удаляют и пересчитывают итоги.
func TestOrphanMovementsDeletesRealOrphans(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	addMovement(t, env, "Реализация", uuid.New(), "500") // документа с таким id нет
	addMovement(t, env, "НетТакогоДокумента", uuid.New(), "700")

	rep := Run(ctx, env, []Check{orphanMovementsCheck{}}, nil)
	res := findResult(t, rep, "orphan-movements")
	if res.Severity != SeverityError {
		t.Fatalf("сирота — это ошибка, получено %q", res.Severity)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("ожидались обе находки (сирота и вне конфигурации): %+v", res.Findings)
	}

	rep = Run(ctx, env, []Check{orphanMovementsCheck{}}, map[string]bool{"orphan-movements": true})
	res = findResult(t, rep, "orphan-movements")
	if res.Fixed != 1 {
		t.Fatalf("ожидалась одна удалённая сирота, получено %d", res.Fixed)
	}
	if got := movementCount(t, env); got != 1 {
		t.Fatalf("должно было остаться движение документа вне конфигурации: осталось %d", got)
	}
	// Отчёт после починки показывает то, что осталось, а не «теперь всё хорошо».
	if res.Severity != SeverityWarn {
		t.Errorf("после починки осталось расхождение — ожидалось предупреждение, получено %q", res.Severity)
	}
}

// Удалить движения выбывшего документа всё-таки можно — но только назвав его
// явно: угадывать это по расхождению метаданных нельзя.
func TestDeleteMovementsOfUnknownRecorderTypeIsExplicit(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	addMovement(t, env, "СовсемУбранныйДокумент", uuid.New(), "500")
	addMovement(t, env, "ДругойДокумент", uuid.New(), "700")

	deleted, err := env.DB.DeleteMovementsOfUnknownRecorderType(ctx, env.Registers, []string{"СовсемУбранныйДокумент"})
	if err != nil {
		t.Fatalf("DeleteMovementsOfUnknownRecorderType: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("ожидалось одно удалённое движение, получено %d", deleted)
	}
	if got := movementCount(t, env); got != 1 {
		t.Fatalf("удалено лишнее: осталось %d из 1", got)
	}
	// Пустой список — ничего не делаем: это защита от «удалить всё» по пустому
	// аргументу, ровно та ошибка, которой страдал прежний код.
	if n, _ := env.DB.DeleteMovementsOfUnknownRecorderType(ctx, env.Registers, nil); n != 0 {
		t.Fatalf("пустой список типов удалил %d движений", n)
	}
	if n, _ := env.DB.DeleteMovementsOfUnknownRecorderType(ctx, env.Registers, []string{"", "   "}); n != 0 {
		t.Fatalf("пустой тип удалил %d движений", n)
	}
}

// Сухой прогон --forget-document (#610): CountMovementsOfRecorderType считает
// объём необратимого удаления, ничего не трогая.
func TestCountMovementsOfRecorderTypeDoesNotDelete(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	addMovement(t, env, "СовсемУбранныйДокумент", uuid.New(), "500")
	addMovement(t, env, "СовсемУбранныйДокумент", uuid.New(), "600")
	addMovement(t, env, "ДругойДокумент", uuid.New(), "700")

	n, err := env.DB.CountMovementsOfRecorderType(ctx, env.Registers, []string{"СовсемУбранныйДокумент"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("сухой прогон насчитал %d, ожидалось 2", n)
	}
	// Ничего не удалено: все три движения на месте.
	if got := movementCount(t, env); got != 3 {
		t.Fatalf("сухой прогон изменил данные: осталось %d из 3", got)
	}
	// Пустой/служебный список — ноль.
	if n, err := env.DB.CountMovementsOfRecorderType(ctx, env.Registers, nil); err != nil || n != 0 {
		t.Fatalf("пустой список насчитал %d, err=%v", n, err)
	}
}

// addAccountEntry вписывает проводку бухрегистра с заданным регистратором.
func addAccountEntry(t *testing.T, env *Env, recorderType string, recorder uuid.UUID, sum string) {
	t.Helper()
	if _, err := env.DB.Exec(context.Background(),
		`INSERT INTO акк_бухучёт (id, period, регистратор, регистратор_тип, счётдт, счёткт, сумма)
		 VALUES (?, '2026-01-15T00:00:00Z', ?, ?, '50', '51', ?)`,
		uuid.New().String(), recorder.String(), recorderType, sum); err != nil {
		t.Fatal(err)
	}
}

func accountEntryCount(t *testing.T, env *Env) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM акк_бухучёт`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Проводки бухрегистра проверяются той же проверкой, что и движения
// накопления (#881).
//
// Doctor обходил только env.Registers, поэтому проводки удалённого документа не
// находила ни одна проверка: они оставались в акк_* навсегда, а обороты —
// перекошенными на их сумму. Половина машинерии при этом уже была — #640
// добавил проверку итогов бухрегистра.
//
// Различение двух случаев обязано работать и здесь: «известный документ, записи
// нет» — сирота, «типа нет в конфигурации» — переименование, данные целы.
func TestOrphanAccountEntries_СиротаУдаляетсяНеизвестныйТипНет(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	addAccountEntry(t, env, "Реализация", uuid.New(), "500")         // документ известен, записи нет → сирота
	addAccountEntry(t, env, "НетТакогоДокумента", uuid.New(), "700") // типа нет в конфигурации → не трогаем
	if got := accountEntryCount(t, env); got != 2 {
		t.Fatalf("подготовка: проводок %d", got)
	}

	rep := Run(ctx, env, []Check{orphanMovementsCheck{}}, nil)
	res := findResult(t, rep, "orphan-movements")
	if res.Severity != SeverityError {
		t.Fatalf("осиротевшая проводка не замечена вовсе: severity=%q summary=%q", res.Severity, res.Summary)
	}
	var sawOrphan, sawUnknown bool
	for _, f := range res.Findings {
		if f.Object != "БухУчёт" {
			continue
		}
		if strings.Contains(f.Detail, "не существует") {
			sawOrphan = true
		}
		if strings.Contains(f.Detail, "нет в конфигурации") {
			sawUnknown = true
		}
	}
	if !sawOrphan {
		t.Errorf("в отчёте нет осиротевшей проводки бухрегистра: %+v", res.Findings)
	}
	if !sawUnknown {
		t.Errorf("в отчёте нет проводки документа вне конфигурации: %+v", res.Findings)
	}

	rep = Run(ctx, env, []Check{orphanMovementsCheck{}}, map[string]bool{"orphan-movements": true})
	res = findResult(t, rep, "orphan-movements")
	if got := accountEntryCount(t, env); got != 1 {
		t.Fatalf("после починки проводок %d, ожидалась 1 (сирота удалена, чужой тип цел)", got)
	}
	var left string
	if err := env.DB.QueryRow(ctx, `SELECT регистратор_тип FROM акк_бухучёт`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != "НетТакогоДокумента" {
		t.Errorf("удалена не та проводка: осталась %q", left)
	}
	if res.Fixed == 0 {
		t.Error("Fixed=0 при удалённой проводке — починка не отражена в отчёте")
	}
}
