package dbcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// #619-1: очистка битой ссылки в измерении регистра накопления обязана пересчитать
// итоги — иначе итоги_* остаются сгруппированы по старому (битому) значению, а
// движения уже с NULL, и Остатки()/Обороты() врут молча. Глобальная сумма при
// этом не меняется, поэтому totalsCheck расхождения не увидит — проверяем ключ
// в таблице итогов напрямую.
func TestRefsFixRecalculatesRegisterTotals(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	broken := uuid.New().String()
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO рег_остатки (id, recorder, recorder_type, line_number, period, вид_движения, контрагент_id, сумма)
		 VALUES (?, ?, 'Реализация', 1, '2026-01-15T00:00:00Z', 'Приход', ?, ?)`,
		uuid.New().String(), uuid.New().String(), broken, "500"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	// Итоги пересчитаны под битое значение измерения (эмулируем предрасчёт плана 80).
	if err := env.DB.RecalcRegisterTotals(ctx, env.Registers[0]); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM итоги_остатки WHERE контрагент_id = ?`, broken).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("подготовка: итоги не содержат битого измерения")
	}

	rep := Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true})
	res := findResult(t, rep, "refs")
	if res.Error != "" {
		t.Fatalf("починка регистра накопления не должна давать ошибку: %s", res.Error)
	}
	// Ссылка в движении занулена…
	var nulls int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM рег_остатки WHERE контрагент_id IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Fatalf("битая ссылка измерения не очищена: NULL-строк %d", nulls)
	}
	// …и итоги пересчитаны: старого (битого) ключа в них больше нет.
	var after int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM итоги_остатки WHERE контрагент_id = ?`, broken).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("итоги не пересчитаны после очистки измерения: строк со старым ключом %d", after)
	}
}

// #619-2: измерение регистра сведений занулить нельзя (NOT NULL + PK). Автоочистка
// его пропускает и сообщает оператору, но НЕ прерывает починку остальных колонок —
// битая ссылка документа в том же прогоне всё равно чистится.
func TestRefsFixSkipsInfoRegDimensionAndContinues(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	// Битая ссылка в измерении регистра сведений (занулить нельзя).
	brokenIR := uuid.New().String()
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO инфо_ценыконтрагентов (контрагент_id, цена, updated_at) VALUES (?, ?, '2026-01-15T00:00:00Z')`,
		brokenIR, "10"); err != nil {
		t.Fatal(err)
	}
	// И обычная битая ссылка документа (её починить можно).
	docBroken := uuid.New().String()
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO реализация (id, контрагент_id, сумма) VALUES (?, ?, ?)`,
		uuid.New().String(), docBroken, "100"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}

	rep := Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true})
	res := findResult(t, rep, "refs")

	// Документная ссылка очищена, несмотря на проблему в регистре сведений.
	if res.Fixed < 1 {
		t.Fatalf("починка прервалась на регистре сведений, документ не тронут: Fixed=%d", res.Fixed)
	}
	var docNulls int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM реализация WHERE контрагент_id IS NULL`).Scan(&docNulls); err != nil {
		t.Fatal(err)
	}
	if docNulls != 1 {
		t.Fatalf("битая ссылка документа не очищена: NULL-строк %d", docNulls)
	}
	// Измерение регистра сведений не занулено и названо в отчёте.
	var irBroken int
	if err := env.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM инфо_ценыконтрагентов WHERE контрагент_id = ?`, brokenIR).Scan(&irBroken); err != nil {
		t.Fatal(err)
	}
	if irBroken != 1 {
		t.Fatalf("измерение регистра сведений занулили (нарушив NOT NULL) или удалили: строк %d", irBroken)
	}
	if !strings.Contains(res.Error, "регистра сведений") {
		t.Fatalf("отчёт не называет непочиненное измерение регистра сведений: %q", res.Error)
	}
}
