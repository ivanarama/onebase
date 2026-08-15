package dbcheck

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// #910: storage.CheckRefs учитывает ссылочные ресурсы обоих регистров, поэтому
// doctor обязан уметь объяснить, какая именно ссылка блокирует удаление.
// Ресурс регистра накопления можно очистить с пересчётом итогов; ресурс
// бухрегистра не трогаем автоматически — это изменило бы уже проведённую
// операцию. Обе ветки раньше не имели даже SQLite-теста, а тип колонки на
// PostgreSQL отличается (uuid вместо TEXT), поэтому проверяем их матрично.
func TestRefsRegisterResourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		partner := &metadata.Entity{
			Name: "Контрагенты" + suffix,
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{
				Name: "Наименование", Type: metadata.FieldTypeString,
			}},
		}
		refType := metadata.FieldType("reference:" + partner.Name)
		regResource := metadata.Field{
			Name: "Куратор", Type: refType, RefEntity: partner.Name,
		}
		reg := &metadata.Register{
			Name:      "Назначения" + suffix,
			Resources: []metadata.Field{regResource},
		}
		accountResource := metadata.Field{
			Name: "Аудитор", Type: refType, RefEntity: partner.Name,
		}
		account := &metadata.AccountRegister{
			Name:      "БухУчёт" + suffix,
			Resources: []metadata.Field{accountResource},
		}

		if err := db.Migrate(ctx, []*metadata.Entity{partner}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{account}); err != nil {
			t.Fatalf("MigrateAccountRegisters: %v", err)
		}

		live := uuid.New()
		broken := uuid.New()
		if err := db.Upsert(ctx, partner.Name, live,
			map[string]any{"Наименование": "Живой"}, partner); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		period := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Документ", uuid.New(), []map[string]any{
			{"Куратор": live},
			{"Куратор": broken},
		}, reg, &period); err != nil {
			t.Fatalf("WriteMovements: %v", err)
		}
		if err := db.WriteAccountMovements(ctx, account.Name, "Документ", uuid.New(), []map[string]any{
			{"счётдт": "50", "счёткт": "51", "Аудитор": live},
			{"счётдт": "50", "счёткт": "51", "Аудитор": broken},
		}, account, &period); err != nil {
			t.Fatalf("WriteAccountMovements: %v", err)
		}

		env := &Env{
			DB: db, Entities: []*metadata.Entity{partner},
			Registers:        []*metadata.Register{reg},
			AccountRegisters: []*metadata.AccountRegister{account},
		}
		res := findResult(t, Run(ctx, env, []Check{refsCheck{}}, nil), "refs")
		if res.Severity != SeverityError || len(res.Findings) != 2 {
			t.Fatalf("ссылочные ресурсы регистров не найдены: %+v", res)
		}
		findings := make(map[string]Finding, len(res.Findings))
		for _, finding := range res.Findings {
			findings[finding.Object] = finding
		}
		regFinding, ok := findings[reg.Name+"."+regResource.Name]
		if !ok || regFinding.Count != 1 {
			t.Fatalf("нет одной битой ссылки в ресурсе регистра накопления: %+v", res.Findings)
		}
		accountFinding, ok := findings[account.Name+"."+accountResource.Name]
		if !ok || accountFinding.Count != 1 || !strings.Contains(accountFinding.Detail, "ресурс бухрегистра") {
			t.Fatalf("ресурс бухрегистра не назван ручной находкой: %+v", res.Findings)
		}

		fixed := findResult(t, Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true}), "refs")
		if fixed.Fixed != 1 {
			t.Fatalf("ресурс регистра накопления не очищен: %+v", fixed)
		}
		if !strings.Contains(fixed.Error, "ресурс бухрегистра") {
			t.Fatalf("ручная находка бухрегистра потеряна при --fix: %q", fixed.Error)
		}

		var regBroken, regLive int
		regTable := metadata.RegisterTableName(reg.Name)
		regColumn := metadata.ColumnName(regResource)
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+regTable+" WHERE "+regColumn+" IS NULL").Scan(&regBroken); err != nil {
			t.Fatalf("проверка очищенного ресурса: %v", err)
		}
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+regTable+" WHERE "+regColumn+" IS NOT NULL").Scan(&regLive); err != nil {
			t.Fatalf("проверка живого ресурса: %v", err)
		}
		if regBroken != 1 || regLive != 1 {
			t.Fatalf("очистка ресурса регистра затронула не те строки: NULL=%d, non-NULL=%d", regBroken, regLive)
		}

		var accountRefs int
		accountTable := metadata.AccountRegTableName(account.Name)
		accountColumn := metadata.ColumnName(accountResource)
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+accountTable+" WHERE "+accountColumn+" IS NOT NULL").Scan(&accountRefs); err != nil {
			t.Fatalf("проверка ресурса бухрегистра: %v", err)
		}
		if accountRefs != 2 {
			t.Fatalf("--fix изменил ресурс бухрегистра: непустых ссылок %d из 2", accountRefs)
		}
	})
}
