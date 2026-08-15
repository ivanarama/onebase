package dbcheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// testEnv собирает настоящий проект на диске и мигрированную базу: проверки
// работают против метаданных и схемы, подменять их заглушками бессмысленно.
func testEnv(t *testing.T) *Env {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config/app.yaml", "name: Тест\n")
	write("catalogs/контрагенты.yaml", `name: Контрагенты
title: Контрагенты
fields:
  - { name: Наименование, type: string }
`)
	write("documents/реализация.yaml", `name: Реализация
title: Реализация
fields:
  - { name: Контрагент, type: reference:Контрагенты }
  - { name: Сумма, type: number(15,2) }
`)
	write("registers/остатки.yaml", `name: Остатки
title: Остатки
totals: { enabled: true }
dimensions:
  - { name: Контрагент, type: reference:Контрагенты }
resources:
  - { name: Сумма, type: number(15,2) }
`)
	write("inforegs/ценыконтрагентов.yaml", `name: ЦеныКонтрагентов
title: Цены контрагентов
dimensions:
  - { name: Контрагент, type: reference:Контрагенты }
resources:
  - { name: Цена, type: number(15,2) }
`)
	write("accounts/основной.yaml", `name: Основной
title: Основной план счетов
accounts:
  - { code: "50", name: Касса, kind: active }
  - { code: "51", name: Расчётный счёт, kind: active }
`)
	write("accountregs/бухучёт.yaml", `name: БухУчёт
title: Бухгалтерский учёт
accounts: Основной
totals: { enabled: true }
subconto:
  - { name: Контрагент, type: reference:Контрагенты }
resources:
  - { name: Сумма, type: number(15,2) }
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}
	return FromProject(db, proj)
}

// insertBrokenRef вписывает ссылку на несуществующий объект.
//
// Приходится снимать контроль внешних ключей: у колонок, созданных вместе с
// таблицей, есть FOREIGN KEY, и обычной записью такую строку не сделать. Именно
// так битые ссылки и появляются в жизни — восстановлением части данных,
// правкой в обход платформы, приездом ссылки по обмену; плюс колонка, добавленная
// в существующую таблицу (AddColumnIfMissing), внешнего ключа не получает вовсе.
func insertBrokenRef(t *testing.T, env *Env, docID, refID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := env.DB.Exec(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO реализация (id, контрагент_id, сумма) VALUES (?, ?, ?)`, docID, refID, "100"); err != nil {
		t.Fatal(err)
	}
}

func findResult(t *testing.T, rep Report, name string) Result {
	t.Helper()
	for _, r := range rep.Results {
		if r.Check == name {
			return r
		}
	}
	t.Fatalf("в отчёте нет проверки %q", name)
	return Result{}
}

// Битая ссылка — ссылка на объект, которого нет. Проверка обязана найти её,
// назвать поле и показать значение.
func TestRefsCheckFindsBrokenReference(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	good := uuid.New()
	if _, err := env.DB.Exec(ctx, `INSERT INTO контрагенты (id, наименование) VALUES (?, ?)`, good.String(), "Живой"); err != nil {
		t.Fatal(err)
	}
	missing := uuid.New()
	insertBrokenRef(t, env, uuid.New().String(), missing.String())
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO реализация (id, контрагент_id, сумма) VALUES (?, ?, ?)`,
		uuid.New().String(), good.String(), "200"); err != nil {
		t.Fatal(err)
	}

	rep := Run(ctx, env, []Check{refsCheck{}}, nil)
	res := findResult(t, rep, "refs")
	if res.Severity != SeverityError {
		t.Fatalf("битая ссылка должна быть ошибкой, получено %q (%s)", res.Severity, res.Summary)
	}
	if len(res.Findings) != 1 || res.Findings[0].Count != 1 {
		t.Fatalf("ожидалась одна находка с одной строкой: %+v", res.Findings)
	}
	if res.Findings[0].Object != "Реализация.Контрагент" {
		t.Errorf("находка не называет поле: %q", res.Findings[0].Object)
	}
	if len(res.Findings[0].Examples) != 1 || res.Findings[0].Examples[0] != missing.String() {
		t.Errorf("в примерах нет битого значения: %+v", res.Findings[0].Examples)
	}

	// Живая ссылка не пострадала: проверка ничего не меняет.
	var n int
	if err := env.DB.QueryRow(ctx, `SELECT COUNT(*) FROM реализация WHERE контрагент_id IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("проверка изменила данные: осталось ссылок %d из 2", n)
	}
}

// --fix refs очищает только битые ссылки, живые не трогает.
func TestRefsCheckFixClearsOnlyBroken(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	good := uuid.New()
	if _, err := env.DB.Exec(ctx, `INSERT INTO контрагенты (id, наименование) VALUES (?, ?)`, good.String(), "Живой"); err != nil {
		t.Fatal(err)
	}
	brokenDoc := uuid.New()
	goodDoc := uuid.New()
	insertBrokenRef(t, env, brokenDoc.String(), uuid.New().String())
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO реализация (id, контрагент_id, сумма) VALUES (?, ?, ?)`,
		goodDoc.String(), good.String(), "100"); err != nil {
		t.Fatal(err)
	}

	rep := Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true})
	res := findResult(t, rep, "refs")
	if res.Fixed != 1 {
		t.Fatalf("ожидалась одна исправленная ссылка, получено %d (%s)", res.Fixed, res.Error)
	}

	var broken *string
	if err := env.DB.QueryRow(ctx, `SELECT контрагент_id FROM реализация WHERE id = ?`, brokenDoc.String()).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != nil {
		t.Fatalf("битая ссылка не очищена: %q", *broken)
	}
	var alive *string
	if err := env.DB.QueryRow(ctx, `SELECT контрагент_id FROM реализация WHERE id = ?`, goodDoc.String()).Scan(&alive); err != nil {
		t.Fatal(err)
	}
	if alive == nil || *alive != good.String() {
		t.Fatal("живая ссылка пострадала при починке")
	}

	// Повторный прогон — чисто.
	rep = Run(ctx, env, []Check{refsCheck{}}, nil)
	if got := findResult(t, rep, "refs"); got.Severity != SeverityOK {
		t.Fatalf("после починки проверка должна быть чистой: %+v", got)
	}
}

// Без --fix не меняется ничего — это главное свойство диагностики.
func TestRunWithoutFixChangesNothing(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	docID := uuid.New()
	ref := uuid.New()
	insertBrokenRef(t, env, docID.String(), ref.String())

	Run(ctx, env, All(), nil)

	var got string
	if err := env.DB.QueryRow(ctx, `SELECT контрагент_id FROM реализация WHERE id = ?`, docID.String()).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != ref.String() {
		t.Fatalf("прогон без --fix изменил данные: %q", got)
	}
}

// Итоги отстали от движений: движение вписано мимо пересчёта — ровно то, что
// делает восстановление части данных или прямой SQL.
func TestTotalsCheckFindsAndRecalculates(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	partner := uuid.New()
	if _, err := env.DB.Exec(ctx, `INSERT INTO контрагенты (id, наименование) VALUES (?, ?)`, partner.String(), "Живой"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(ctx,
		`INSERT INTO рег_остатки (id, recorder, recorder_type, line_number, period, вид_движения, контрагент_id, сумма)
		 VALUES (?, ?, 'Реализация', 1, '2026-01-15T00:00:00Z', 'Приход', ?, ?)`,
		uuid.New().String(), uuid.New().String(), partner.String(), "500"); err != nil {
		t.Fatal(err)
	}

	rep := Run(ctx, env, []Check{totalsCheck{}}, nil)
	res := findResult(t, rep, "totals")
	if res.Severity != SeverityError || len(res.Findings) != 1 {
		t.Fatalf("расхождение итогов не найдено: %+v", res)
	}
	if res.Findings[0].Object != "Остатки.Сумма" {
		t.Errorf("находка не называет ресурс: %q", res.Findings[0].Object)
	}

	rep = Run(ctx, env, []Check{totalsCheck{}}, map[string]bool{"totals": true})
	if res := findResult(t, rep, "totals"); res.Fixed != 1 {
		t.Fatalf("итоги не пересчитаны: %+v", res)
	}
	rep = Run(ctx, env, []Check{totalsCheck{}}, nil)
	if res := findResult(t, rep, "totals"); res.Severity != SeverityOK {
		t.Fatalf("после пересчёта итоги должны сходиться: %+v", res)
	}
}

// insertAccountEntry вписывает проводку бухрегистра МИМО пересчёта итогов —
// ровно то, что делает восстановление части данных или прямой SQL.
func insertAccountEntry(t *testing.T, env *Env, sum string) {
	t.Helper()
	if _, err := env.DB.Exec(context.Background(),
		`INSERT INTO акк_бухучёт (id, period, регистратор, регистратор_тип, счётдт, счёткт, сумма)
		 VALUES (?, '2026-01-15T00:00:00Z', ?, 'Реализация', '50', '51', ?)`,
		uuid.New().String(), uuid.New().String(), sum); err != nil {
		t.Fatal(err)
	}
}

// #613: доктор обязан ловить разъехавшиеся итоги регистра БУХГАЛТЕРИИ — раньше
// он их не проверял вовсе, и испорченные итоги_акк_* оставались невидимы.
func TestAccountTotalsCheckFindsAndRecalculates(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	insertAccountEntry(t, env, "500")

	rep := Run(ctx, env, []Check{accountTotalsCheck{}}, nil)
	res := findResult(t, rep, "account-totals")
	if res.Severity != SeverityError || len(res.Findings) != 1 {
		t.Fatalf("расхождение итогов бухрегистра не найдено: %+v", res)
	}
	if res.Findings[0].Object != "БухУчёт.Сумма" {
		t.Errorf("находка не называет ресурс: %q", res.Findings[0].Object)
	}

	rep = Run(ctx, env, []Check{accountTotalsCheck{}}, map[string]bool{"account-totals": true})
	if res := findResult(t, rep, "account-totals"); res.Fixed != 1 {
		t.Fatalf("итоги бухрегистра не пересчитаны: %+v", res)
	}
	rep = Run(ctx, env, []Check{accountTotalsCheck{}}, nil)
	if res := findResult(t, rep, "account-totals"); res.Severity != SeverityOK {
		t.Fatalf("после пересчёта итоги бухрегистра должны сходиться: %+v", res)
	}
}

// --fix totals (регистры накопления) не должен рапортовать об успехе, оставляя
// бухгалтерскую часть нетронутой: это разные проверки, и account-totals обязана
// по-прежнему видеть расхождение.
func TestFixTotalsDoesNotTouchAccountTotals(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	insertAccountEntry(t, env, "500")

	// Чиним только totals — бухгалтерских итогов он касаться не должен.
	Run(ctx, env, []Check{totalsCheck{}}, map[string]bool{"totals": true})

	rep := Run(ctx, env, []Check{accountTotalsCheck{}}, nil)
	if res := findResult(t, rep, "account-totals"); res.Severity != SeverityError {
		t.Fatalf("--fix totals молча «починил» бухрегистр: %+v", res)
	}
}

// Здоровая база — тишина по всем проверкам.
func TestAllChecksOnHealthyBase(t *testing.T) {
	ctx := context.Background()
	env := testEnv(t)

	rep := Run(ctx, env, All(), nil)
	if rep.Worst() != SeverityOK {
		for _, r := range rep.Results {
			if r.Severity != SeverityOK {
				t.Errorf("%s: %s (%s)", r.Check, r.Summary, r.Error)
			}
		}
		t.Fatal("на здоровой базе проверки должны быть чистыми")
	}
	if len(rep.Results) != len(All()) {
		t.Fatalf("в отчёте %d проверок из %d", len(rep.Results), len(All()))
	}
}

func TestSelectUnknownCheck(t *testing.T) {
	checks, unknown := Select([]string{"refs", "чего-то-нет"})
	if len(checks) != 1 || checks[0].Name() != "refs" {
		t.Fatalf("выбраны не те проверки: %+v", checks)
	}
	if len(unknown) != 1 || unknown[0] != "чего-то-нет" {
		t.Fatalf("неизвестная проверка не названа: %+v", unknown)
	}
	if all, unknown := Select(nil); len(all) != len(All()) || unknown != nil {
		t.Fatal("пустой список должен означать «все проверки»")
	}
}
