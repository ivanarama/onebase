package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func totalsAccountReg() *metadata.AccountRegister {
	return &metadata.AccountRegister{
		Name:      "БухИтоги",
		Accounts:  "Основной",
		Resources: []metadata.Field{{Name: "Сумма", Type: "number"}},
		Subconto:  []metadata.Field{{Name: "Номенклатура", Type: "string"}},
		Totals:    metadata.RegisterTotals{Enabled: true},
	}
}

type acctDtKt struct{ dt, kt float64 }

func scanAcctDtKt(t *testing.T, db *DB, ctx context.Context, q string) map[string]acctDtKt {
	t.Helper()
	rows, err := db.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]acctDtKt{}
	for rows.Next() {
		var code string
		var d, k float64
		if err := rows.Scan(&code, &d, &k); err != nil {
			t.Fatal(err)
		}
		out[code] = acctDtKt{d, k}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func scanCodeSum(t *testing.T, db *DB, ctx context.Context, q string) map[string]float64 {
	t.Helper()
	rows, err := db.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var code string
		var v float64
		if err := rows.Scan(&code, &v); err != nil {
			t.Fatal(err)
		}
		out[code] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Полный пересчёт итогов бухрегистра совпадает с расчётом оборотов на лету: для
// каждого счёта Σ помесячных Дт/Кт из итогов == SUM(сумма) проводок по счётдт/счёткт.
// Это ключевой инвариант разворота проводки на дебетовую и кредитовую половины.
func TestAccountTotals_RecalcMatchesOnTheFly(t *testing.T) {
	ar := totalsAccountReg()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatal(err)
	}
	ensureTestChart(t, db, ar.Accounts, "41", "51", "60", "62", "90")

	writeDoc := func(p time.Time, rows []map[string]any) {
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", uuid.New(), rows, ar, &p); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	june := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	july := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	writeDoc(june, []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто1": "Товар-X"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто1": "Товар-Y"},
		{"счётдт": "51", "счёткт": "62", "сумма": float64(700)},
	})
	writeDoc(july, []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(300), "субконто1": "Товар-X"},
		{"счётдт": "62", "счёткт": "90", "сумма": float64(700)},
	})

	if err := db.RecalcAccountRegisterTotals(ctx, ar); err != nil {
		t.Fatalf("recalc: %v", err)
	}

	fromTotals := scanAcctDtKt(t, db, ctx,
		"SELECT счёт, COALESCE(SUM(сумма_дт),0), COALESCE(SUM(сумма_кт),0) FROM итоги_акк_бухитоги GROUP BY счёт")
	onTheFlyDt := scanCodeSum(t, db, ctx, "SELECT счётдт, SUM(сумма) FROM акк_бухитоги GROUP BY счётдт")
	onTheFlyKt := scanCodeSum(t, db, ctx, "SELECT счёткт, SUM(сумма) FROM акк_бухитоги GROUP BY счёткт")

	codes := map[string]bool{}
	for c := range onTheFlyDt {
		codes[c] = true
	}
	for c := range onTheFlyKt {
		codes[c] = true
	}
	if len(codes) == 0 {
		t.Fatal("нет проводок — тест ничего не проверил")
	}
	for c := range codes {
		if fromTotals[c].dt != onTheFlyDt[c] {
			t.Errorf("счёт %s: оборот Дт итогов=%v, на лету=%v", c, fromTotals[c].dt, onTheFlyDt[c])
		}
		if fromTotals[c].kt != onTheFlyKt[c] {
			t.Errorf("счёт %s: оборот Кт итогов=%v, на лету=%v", c, fromTotals[c].kt, onTheFlyKt[c])
		}
	}

	// Помесячность: счёт 41 участвует в июне и июле → две строки месяцев.
	var months int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(DISTINCT месяц) FROM итоги_акк_бухитоги WHERE счёт='41'").Scan(&months); err != nil {
		t.Fatal(err)
	}
	if months != 2 {
		t.Errorf("счёт 41: ожидалось 2 месяца в итогах, получили %d", months)
	}
}

func snapshotAcctTotals(t *testing.T, db *DB, ctx context.Context, table string) map[string]acctDtKt {
	t.Helper()
	rows, err := db.Query(ctx, "SELECT счёт, COALESCE(субконто1,'∅'), месяц, сумма_дт, сумма_кт FROM "+table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]acctDtKt{}
	for rows.Next() {
		var acc, sub, month string
		var dt, kt float64
		if err := rows.Scan(&acc, &sub, &month, &dt, &kt); err != nil {
			t.Fatal(err)
		}
		out[acc+"|"+sub+"|"+month] = acctDtKt{dt, kt}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Инкрементальная поддержка итогов в write-path согласована с полным пересчётом:
// после проведения, перепроведения задним числом и отмены итоги, поддержанные
// WriteAccountMovements в транзакции, совпадают с RecalcAccountRegisterTotals с нуля.
func TestAccountTotals_IncrementalMatchesRecalc(t *testing.T) {
	ar := totalsAccountReg()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatal(err)
	}
	ensureTestChart(t, db, ar.Accounts, "41", "51", "60", "62", "90")
	const table = "итоги_акк_бухитоги"

	doc1, doc2 := uuid.New(), uuid.New()
	may := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	july := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	post := func(doc uuid.UUID, p time.Time, rows []map[string]any) {
		if err := db.WriteAccountMovements(ctx, ar.Name, "Док", doc, rows, ar, &p); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	post(doc1, june, []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто1": "Товар-X"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто1": "Товар-Y"},
	})
	post(doc2, july, []map[string]any{
		{"счётдт": "51", "счёткт": "62", "сумма": float64(700)},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(300), "субконто1": "Товар-X"},
	})
	// перепроведение doc1 задним числом (май), другие суммы и субконто
	post(doc1, may, []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(200), "субконто1": "Товар-Z"},
	})
	// отмена проведения doc2 (rows=nil) — его вклад в итоги должен исчезнуть
	post(doc2, july, nil)

	incremental := snapshotAcctTotals(t, db, ctx, table)
	if err := db.RecalcAccountRegisterTotals(ctx, ar); err != nil {
		t.Fatalf("recalc: %v", err)
	}
	full := snapshotAcctTotals(t, db, ctx, table)

	if len(incremental) != len(full) {
		t.Fatalf("число строк итогов: инкремент=%d, пересчёт=%d\ninc=%v\nfull=%v",
			len(incremental), len(full), incremental, full)
	}
	for k, v := range full {
		if incremental[k] != v {
			t.Errorf("ключ %s: инкремент=%v, пересчёт=%v", k, incremental[k], v)
		}
	}
}

// При выключённых итогах таблица итогов не создаётся (не платим за поддержку).
func TestAccountTotals_DisabledNoTable(t *testing.T) {
	ar := totalsAccountReg()
	ar.Totals.Enabled = false
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='итоги_акк_бухитоги'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("итоги выключены, но таблица итогов создана")
	}
}
