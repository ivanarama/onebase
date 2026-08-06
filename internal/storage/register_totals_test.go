package storage

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// toF приводит значение остатка (int64/float64/[]byte/string из SQLite) к float64.
func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case nil:
		return 0
	case []byte:
		var f float64
		_, _ = fmt.Sscanf(string(x), "%g", &f)
		return f
	case string:
		var f float64
		_, _ = fmt.Sscanf(x, "%g", &f)
		return f
	default:
		return 0
	}
}

// onTheFlyBalances считает остатки «как genBalances»: SUM знаковых ресурсов по
// измерениям напрямую из движений. Возвращает map[ключ-кортежа] -> сумма ресурсов.
func onTheFlyBalances(ctx context.Context, t *testing.T, db *DB, reg *metadata.Register) map[string][]float64 {
	t.Helper()
	dims := dimColNames(reg.Dimensions)
	var sel []string
	sel = append(sel, dims...)
	for _, r := range reg.Resources {
		sel = append(sel, SignedResourceSum(metadata.ColumnName(r)))
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM " + metadata.RegisterTableName(reg.Name)
	if len(dims) > 0 {
		q += " GROUP BY " + strings.Join(dims, ", ")
	}
	q += " HAVING COUNT(*) > 0"
	return scanBalances(ctx, t, db, q, len(dims), len(reg.Resources))
}

// storedTotals читает содержимое таблицы итогов, агрегируя помесячные обороты в
// текущий остаток (SUM по всем месяцам на кортеж измерений) — так его можно
// сравнить с расчётом остатков на лету.
func storedTotals(ctx context.Context, t *testing.T, db *DB, reg *metadata.Register) map[string][]float64 {
	t.Helper()
	dims := dimColNames(reg.Dimensions)
	var sel []string
	sel = append(sel, dims...)
	for _, r := range reg.Resources {
		sel = append(sel, "SUM("+metadata.ColumnName(r)+")")
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM " + metadata.RegisterTotalsTableName(reg.Name)
	if len(dims) > 0 {
		q += " GROUP BY " + strings.Join(dims, ", ")
	}
	// HAVING COUNT(*)>0 — как в onTheFlyBalances: для регистра без измерений
	// (нет GROUP BY) агрегат по пустой таблице иначе дал бы фиктивную строку.
	q += " HAVING COUNT(*) > 0"
	return scanBalances(ctx, t, db, q, len(dims), len(reg.Resources))
}

func scanBalances(ctx context.Context, t *testing.T, db *DB, q string, nDims, nRes int) map[string][]float64 {
	t.Helper()
	rows, err := db.Query(ctx, q)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer rows.Close()
	out := make(map[string][]float64)
	for rows.Next() {
		dest := make([]any, nDims+nRes)
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var keyParts []string
		for i := 0; i < nDims; i++ {
			keyParts = append(keyParts, fmt.Sprintf("%v", dest[i]))
		}
		res := make([]float64, nRes)
		for i := 0; i < nRes; i++ {
			res[i] = toF(dest[nDims+i])
		}
		out[strings.Join(keyParts, "|")] = res
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// assertTotalsMatch — центральный инвариант: содержимое итогов совпадает с
// расчётом остатков на лету. Строки с нулевым остатком присутствуют в обоих
// (движения есть, сумма 0) или отсутствуют в обоих (движений нет).
func assertTotalsMatch(ctx context.Context, t *testing.T, db *DB, reg *metadata.Register, stage string) {
	t.Helper()
	want := onTheFlyBalances(ctx, t, db, reg)
	got := storedTotals(ctx, t, db, reg)
	if len(want) != len(got) {
		t.Fatalf("[%s] число строк итогов %d != на лету %d\nитоги=%v\nна лету=%v", stage, len(got), len(want), got, want)
	}
	for key, w := range want {
		g, ok := got[key]
		if !ok {
			t.Fatalf("[%s] кортеж %q есть на лету, но нет в итогах", stage, key)
		}
		for i := range w {
			if math.Abs(w[i]-g[i]) > 1e-9 {
				t.Fatalf("[%s] кортеж %q ресурс %d: итоги=%g, на лету=%g", stage, key, i, g[i], w[i])
			}
		}
	}
}

func totalsTestReg() *metadata.Register {
	return &metadata.Register{
		Name: "ОстаткиТоваров",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		Totals: metadata.RegisterTotals{Enabled: true},
	}
}

func setupTotalsDB(ctx context.Context, t *testing.T, reg *metadata.Register) *DB {
	t.Helper()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "totals.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}
	return db
}

func mv(vid, nom, skl string, kol, sum float64) map[string]any {
	return map[string]any{"ВидДвижения": vid, "Номенклатура": nom, "Склад": skl, "Количество": kol, "Сумма": sum}
}

// TestRegisterTotals_MatchesOnTheFly проверяет инвариант на осмысленной
// последовательности: проведение, перепроведение (замена строк), отмена
// (пустые движения), проведение задним числом и в другой склад.
func TestRegisterTotals_MatchesOnTheFly(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()
	doc := "ПоступлениеТоваров"

	recA, recB, recC := uuid.New(), uuid.New(), uuid.New()

	write := func(rec uuid.UUID, rows []map[string]any, stage string) {
		if err := db.WriteMovements(ctx, reg.Name, doc, rec, rows, reg, nil); err != nil {
			t.Fatalf("[%s] WriteMovements: %v", stage, err)
		}
		assertTotalsMatch(ctx, t, db, reg, stage)
	}

	write(recA, []map[string]any{mv("Приход", "Стол", "Осн", 5, 500), mv("Приход", "Стул", "Осн", 3, 150)}, "A: приход")
	write(recB, []map[string]any{mv("Расход", "Стол", "Осн", 2, 200)}, "B: расход")
	write(recC, []map[string]any{mv("Приход", "Стол", "Доп", 10, 1000)}, "C: другой склад")
	// перепроведение A: Стол 5→7, строка Стул убрана (её остаток должен обнулиться)
	write(recA, []map[string]any{mv("Приход", "Стол", "Осн", 7, 700)}, "A': перепроведение")
	// отмена C: Стол@Доп должен исчезнуть
	write(recC, nil, "C': отмена")
	// движение, обнуляющее остаток Стол@Осн (7-2-5=0): строка должна остаться с 0
	write(recB, []map[string]any{mv("Расход", "Стол", "Осн", 5, 500)}, "B': ноль-остаток")
}

// TestRegisterTotals_Randomized — рандомизированный прогон с фиксированным
// сидом: множество регистраторов, случайные приходы/расходы по небольшому
// пространству кортежей, периодические перезаписи/отмены. Инвариант держится
// после каждой операции.
func TestRegisterTotals_Randomized(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()
	doc := "Документ"

	rng := rand.New(rand.NewSource(42))
	noms := []string{"Товар1", "Товар2", "Товар3"}
	skls := []string{"СкладА", "СкладБ"}
	var recorders []uuid.UUID
	for i := 0; i < 8; i++ {
		recorders = append(recorders, uuid.New())
	}

	for step := 0; step < 120; step++ {
		rec := recorders[rng.Intn(len(recorders))]
		var rows []map[string]any
		if rng.Intn(5) != 0 { // 20% операций — отмена (пустые движения)
			n := 1 + rng.Intn(3)
			for k := 0; k < n; k++ {
				vid := "Приход"
				if rng.Intn(2) == 0 {
					vid = "Расход"
				}
				rows = append(rows, mv(vid,
					noms[rng.Intn(len(noms))], skls[rng.Intn(len(skls))],
					float64(1+rng.Intn(20)), float64(10*(1+rng.Intn(20)))))
			}
		}
		if err := db.WriteMovements(ctx, reg.Name, doc, rec, rows, reg, nil); err != nil {
			t.Fatalf("шаг %d: WriteMovements: %v", step, err)
		}
		assertTotalsMatch(ctx, t, db, reg, fmt.Sprintf("rnd-%d", step))
	}
}

func TestRegisterTotals_DedupTuplesNoStringCollision(t *testing.T) {
	tuples := [][]any{
		{"a b", "c"},
		{"a", "b c"},
		{"a", "b c"},
		{nil, "x"},
		{nil, "x"},
	}
	got := dedupTuples(tuples)
	if len(got) != 3 {
		t.Fatalf("dedupTuples collapsed distinct tuples: got %d tuples: %#v", len(got), got)
	}
}

// TestRegisterTotals_NoDimensions — регистр без измерений: единственная строка
// итога (или её отсутствие при нулевой истории).
func TestRegisterTotals_NoDimensions(t *testing.T) {
	ctx := context.Background()
	reg := &metadata.Register{
		Name:      "ОбщийСчётчик",
		Resources: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeNumber}},
		Totals:    metadata.RegisterTotals{Enabled: true},
	}
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()
	doc := "Док"
	rec := uuid.New()

	if err := db.WriteMovements(ctx, reg.Name, doc, rec,
		[]map[string]any{{"ВидДвижения": "Приход", "Значение": float64(10)}}, reg, nil); err != nil {
		t.Fatal(err)
	}
	assertTotalsMatch(ctx, t, db, reg, "без измерений: приход")
	if rows, err := db.GetBalances(ctx, reg.Name, reg, RegFilter{}); err != nil || len(rows) != 1 {
		t.Fatalf("GetBalances без измерений: rows=%v err=%v", rows, err)
	}

	if err := db.WriteMovements(ctx, reg.Name, doc, rec, nil, reg, nil); err != nil {
		t.Fatal(err)
	}
	assertTotalsMatch(ctx, t, db, reg, "без измерений: отмена")
}

// TestRegisterTotals_WithinTransaction воспроизводит реальный путь проведения:
// entityservice оборачивает WriteMovements в db.WithTx. На SQLite это одно
// соединение — поддержка итогов (курсор distinctDimTuples + последующие exec)
// не должна упираться в открытый курсор/дедлок.
func TestRegisterTotals_WithinTransaction(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()

	err := db.WithTx(ctx, func(ctx context.Context) error {
		return db.WriteMovements(ctx, reg.Name, "Док", uuid.New(),
			[]map[string]any{mv("Приход", "Товар1", "СкладА", 5, 50), mv("Приход", "Товар2", "СкладА", 3, 30)},
			reg, nil)
	})
	if err != nil {
		t.Fatalf("WithTx/WriteMovements: %v", err)
	}
	assertTotalsMatch(ctx, t, db, reg, "в транзакции")
}

// TestRecalcRegisterTotals — полный пересчёт восстанавливает итоги, совпадающие с
// инкрементально поддержанными (и с расчётом на лету).
func TestRecalcRegisterTotals(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()

	for i := 0; i < 5; i++ {
		if err := db.WriteMovements(ctx, reg.Name, "Док", uuid.New(),
			[]map[string]any{mv("Приход", "Товар1", "СкладА", float64(i+1), 0), mv("Расход", "Товар2", "СкладБ", float64(i), 0)},
			reg, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecalcRegisterTotals(ctx, reg); err != nil {
		t.Fatalf("RecalcRegisterTotals: %v", err)
	}
	assertTotalsMatch(ctx, t, db, reg, "после полного пересчёта")
}

func TestRegisterTotals_RebuildAfterDisabledWrites(t *testing.T) {
	ctx := context.Background()
	enabled := totalsTestReg()
	db := setupTotalsDB(ctx, t, enabled)
	defer db.Close()

	if err := db.WriteMovements(ctx, enabled.Name, "Док", uuid.New(),
		[]map[string]any{mv("Приход", "Товар1", "СкладА", 5, 50)}, enabled, nil); err != nil {
		t.Fatal(err)
	}
	disabled := totalsTestReg()
	disabled.Totals.Enabled = false
	if err := db.MigrateRegisters(ctx, []*metadata.Register{disabled}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteMovements(ctx, disabled.Name, "Док", uuid.New(),
		[]map[string]any{mv("Приход", "Товар1", "СкладА", 3, 30)}, disabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{enabled}); err != nil {
		t.Fatal(err)
	}
	assertTotalsMatch(ctx, t, db, enabled, "re-enabled")
}

func TestRegisterTotals_RebuildAfterSchemaChange(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()
	if err := db.WriteMovements(ctx, reg.Name, "Док", uuid.New(),
		[]map[string]any{mv("Приход", "Товар1", "СкладА", 5, 50)}, reg, nil); err != nil {
		t.Fatal(err)
	}

	changed := totalsTestReg()
	changed.Dimensions = append(changed.Dimensions, metadata.Field{Name: "Партия", Type: metadata.FieldTypeString})
	changed.Resources = append(changed.Resources, metadata.Field{Name: "Вес", Type: metadata.FieldTypeNumber})
	if err := db.MigrateRegisters(ctx, []*metadata.Register{changed}); err != nil {
		t.Fatalf("schema migration/rebuild: %v", err)
	}
	row := mv("Приход", "Товар2", "СкладБ", 2, 20)
	row["Партия"] = "P1"
	row["Вес"] = 7
	if err := db.WriteMovements(ctx, changed.Name, "Док", uuid.New(), []map[string]any{row}, changed, nil); err != nil {
		t.Fatal(err)
	}
	assertTotalsMatch(ctx, t, db, changed, "schema changed")
}

func TestRegisterTotals_SQLiteUsesUTCMonth(t *testing.T) {
	ctx := context.Background()
	reg := totalsTestReg()
	db := setupTotalsDB(ctx, t, reg)
	defer db.Close()

	moscow := time.FixedZone("UTC+3", 3*60*60)
	period := time.Date(2026, 3, 1, 0, 30, 0, 0, moscow) // 2026-02-28 21:30 UTC
	if err := db.WriteMovements(ctx, reg.Name, "Док", uuid.New(),
		[]map[string]any{mv("Приход", "Товар1", "СкладА", 1, 10)}, reg, &period); err != nil {
		t.Fatal(err)
	}
	var month string
	if err := db.QueryRow(ctx, "SELECT "+totalsMonthCol+" FROM "+metadata.RegisterTotalsTableName(reg.Name)).Scan(&month); err != nil {
		t.Fatal(err)
	}
	if month != "2026-02" {
		t.Fatalf("UTC month bucket=%q, want 2026-02", month)
	}
}
