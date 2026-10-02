package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// регистр бухгалтерии с двумя субконто для тестов.
func subcontoReg() *metadata.AccountRegister {
	return &metadata.AccountRegister{
		Name:     "БухУчёт",
		Accounts: "Основной",
		Resources: []metadata.Field{
			{Name: "Сумма", Type: "number"},
		},
		Subconto: []metadata.Field{
			{Name: "Контрагент", Type: "string"},
			{Name: "Номенклатура", Type: "string"},
		},
	}
}

func newAccountTestDB(t *testing.T, ar *metadata.AccountRegister) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatal(err)
	}
	ensureTestChart(t, db, ar.Accounts, "41", "60", "19.3")
	return db, ctx
}

// Дв.Субконто1 / Дв.Субконто.<Имя> должны попасть в колонки субконто<N>.
func TestAccountReg_Subconto_Write(t *testing.T) {
	ar := subcontoReg()
	db, ctx := newAccountTestDB(t, ar)
	period := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	rows := []map[string]any{
		// краткая форма по номеру (как Дв.Субконто1 = ...)
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто1": "Поставщик-А"},
		// именованная форма (как Дв.Субконто.Номенклатура = ...) → плоский ключ
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто_номенклатура": "Товар-X"},
	}
	if err := db.WriteAccountMovements(ctx, ar.Name, "ПоступлениеТоваров", uuid.New(), rows, ar, &period); err != nil {
		t.Fatalf("WriteAccountMovements: %v", err)
	}

	var cnt1, cnt2 int
	r := db.QueryRow(ctx, "SELECT COUNT(*) FROM акк_бухучёт WHERE субконто1='Поставщик-А'")
	if err := r.Scan(&cnt1); err != nil {
		t.Fatal(err)
	}
	if cnt1 != 1 {
		t.Errorf("субконто1 (по номеру): ожидалась 1 строка с Поставщик-А, получили %d", cnt1)
	}
	r = db.QueryRow(ctx, "SELECT COUNT(*) FROM акк_бухучёт WHERE субконто2='Товар-X'")
	if err := r.Scan(&cnt2); err != nil {
		t.Fatal(err)
	}
	if cnt2 != 1 {
		t.Errorf("субконто2 (по имени Дв.Субконто.Номенклатура): ожидалась 1 строка с Товар-X, получили %d", cnt2)
	}
}

// Разворот остатков по субконто: суммы группируются по аналитике.
func TestAccountReg_Balance_BySubconto(t *testing.T) {
	ar := subcontoReg()
	db, ctx := newAccountTestDB(t, ar)
	period := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	rows := []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто2": "Товар-X"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто2": "Товар-Y"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(300), "субконто2": "Товар-X"},
	}
	if err := db.WriteAccountMovements(ctx, ar.Name, "ПоступлениеТоваров", uuid.New(), rows, ar, &period); err != nil {
		t.Fatalf("WriteAccountMovements: %v", err)
	}

	got := map[string]float64{}
	r, err := db.Query(ctx,
		"SELECT субконто2, SUM(сумма) FROM акк_бухучёт WHERE счётдт='41' GROUP BY субконто2")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var товар string
		var сумма float64
		if err := r.Scan(&товар, &сумма); err != nil {
			t.Fatal(err)
		}
		got[товар] = сумма
	}
	if got["Товар-X"] != 1300 {
		t.Errorf("Товар-X: ожидалось 1300, получили %v", got["Товар-X"])
	}
	if got["Товар-Y"] != 500 {
		t.Errorf("Товар-Y: ожидалось 500, получили %v", got["Товар-Y"])
	}
}

// AccountBalances разворачивает остатки по субконто: счёт с аналитикой даёт строку
// на каждую комбинацию субконто, счёт без аналитики — одну строку с пустыми субконто.
func TestAccountBalances_BySubconto(t *testing.T) {
	ar := subcontoReg()
	ar.Accounts = "Основной"
	db, ctx := newAccountTestDB(t, ar)

	if err := db.EnsureAccountsTable(ctx); err != nil {
		t.Fatal(err)
	}
	chart := &metadata.ChartOfAccounts{
		Name: "Основной",
		Accounts: []metadata.Account{
			{Code: "41", Kind: "active"},
			{Code: "60", Kind: "passive"},
			{Code: "19.3", Kind: "active"},
		},
	}
	if err := db.SyncAccounts(ctx, []*metadata.ChartOfAccounts{chart}); err != nil {
		t.Fatal(err)
	}

	period := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто2": "Товар-X"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто2": "Товар-Y"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(300), "субконто2": "Товар-X"},
		// счёт без аналитики
		{"счётдт": "19.3", "счёткт": "60", "сумма": float64(200)},
	}
	if err := db.WriteAccountMovements(ctx, ar.Name, "ПоступлениеТоваров", uuid.New(), rows, ar, &period); err != nil {
		t.Fatalf("WriteAccountMovements: %v", err)
	}

	asOf := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	balances, err := db.AccountBalances(ctx, ar.Name, "Основной", asOf, ar.Resources, ar.Subconto)
	if err != nil {
		t.Fatalf("AccountBalances: %v", err)
	}

	// Соберём сальдо счёта 41 в разрезе субконто2.
	saldo41 := map[string]float64{}
	var count193 int
	var saldo193 float64
	for _, b := range balances {
		code, _ := b["code"].(string)
		switch code {
		case "41":
			товар, _ := b["субконто2"].(string)
			saldo41[товар] = toFloat(b["сумма"]) // AccountBalances отдаёт decimal (план 42)
		case "19.3":
			count193++
			saldo193 = toFloat(b["сумма"])
		}
	}

	if saldo41["Товар-X"] != 1300 {
		t.Errorf("счёт 41 / Товар-X: ожидалось сальдо 1300, получили %v", saldo41["Товар-X"])
	}
	if saldo41["Товар-Y"] != 500 {
		t.Errorf("счёт 41 / Товар-Y: ожидалось сальдо 500, получили %v", saldo41["Товар-Y"])
	}
	// Счёт без аналитики — ровно одна строка с пустым субконто.
	if count193 != 1 {
		t.Errorf("счёт 19.3 без аналитики: ожидалась 1 строка, получили %d", count193)
	}
	if saldo193 != 200 {
		t.Errorf("счёт 19.3: ожидалось сальдо 200, получили %v", saldo193)
	}
}

// id движения должен заполняться: на нём держится HAVING в .Обороты()
// (SUM(r.id IS NOT NULL) > 0). Без id колонка была NULL и обороты были пусты.
func TestAccountReg_MovementHasID(t *testing.T) {
	ar := subcontoReg()
	db, ctx := newAccountTestDB(t, ar)
	period := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	rows := []map[string]any{
		{"счётдт": "41", "счёткт": "60", "сумма": float64(1000), "субконто2": "Товар-X"},
		{"счётдт": "41", "счёткт": "60", "сумма": float64(500), "субконто2": "Товар-Y"},
	}
	if err := db.WriteAccountMovements(ctx, ar.Name, "Doc", uuid.New(), rows, ar, &period); err != nil {
		t.Fatalf("WriteAccountMovements: %v", err)
	}

	var nullIDs int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM акк_бухучёт WHERE id IS NULL").Scan(&nullIDs); err != nil {
		t.Fatal(err)
	}
	if nullIDs != 0 {
		t.Errorf("есть движения с NULL id: %d (ломает .Обороты())", nullIDs)
	}
}

// Присваивание несуществующему субконто — ошибка проведения, не тихий no-op.
func TestAccountReg_UnknownSubconto_Errors(t *testing.T) {
	ar := subcontoReg() // объявлено два субконто
	db, ctx := newAccountTestDB(t, ar)
	period := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		row  map[string]any
	}{
		{"номер вне диапазона", map[string]any{"счётдт": "41", "счёткт": "60", "сумма": float64(1), "субконто9": "X"}},
		{"неизвестное имя", map[string]any{"счётдт": "41", "счёткт": "60", "сумма": float64(1), "субконто_договор": "X"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := db.WriteAccountMovements(ctx, ar.Name, "Doc", uuid.New(), []map[string]any{c.row}, ar, &period)
			if err == nil {
				t.Fatalf("ожидалась ошибка проведения для %s, но её нет", c.name)
			}
		})
	}
}
