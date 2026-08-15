package dbcheck

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Матричный прогон проверки ссылочной целостности бухрегистра (#910).
//
// У колонок акк_* внешних ключей нет ни на SQLite, ни на PostgreSQL — именно
// поэтому битая ссылка туда попадает обычным INSERT и живёт незамеченной. Тест
// это фиксирует буквально: обхода FK здесь не нужно, и если он когда-нибудь
// понадобится, значит схема изменилась и проверку надо пересматривать.
func TestRefsAccountRegisterMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		partner := &metadata.Entity{
			Name: "Контрагенты" + suffix, Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		ar := &metadata.AccountRegister{
			Name:      "БухУчёт" + suffix,
			Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
			Subconto: []metadata.Field{{
				Name: "Контрагент", Type: metadata.FieldType("reference:" + partner.Name), RefEntity: partner.Name,
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{partner}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
			t.Fatalf("MigrateAccountRegisters: %v", err)
		}

		live := uuid.New()
		if err := db.Upsert(ctx, partner.Name, live, map[string]any{"Наименование": "Живой"}, partner); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		broken := uuid.New()
		table := metadata.AccountRegTableName(ar.Name)
		ph := func(n int) string { return db.Dialect().Placeholder(n) }
		// UUID уходит в запрос строкой: на SQLite колонка TEXT, на PostgreSQL —
		// uuid, и текстовый литерал он приводит сам. Своего idArg тесту не нужно.
		insert := fmt.Sprintf(
			`INSERT INTO %s (id, period, регистратор, регистратор_тип, счётдт, счёткт, сумма, субконто1)
			 VALUES (%s, %s, %s, 'Реализация', '50', '51', '100', %s)`,
			table, ph(1), ph(2), ph(3), ph(4))
		period := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
		// Живая проводка и битая — чтобы проверка считала именно битые, а не все.
		for _, subconto := range []uuid.UUID{live, broken} {
			if _, err := db.Exec(ctx, insert,
				uuid.New().String(), period, uuid.New().String(),
				subconto.String()); err != nil {
				t.Fatalf("вставка проводки: %v", err)
			}
		}

		env := &Env{
			DB:               db,
			Entities:         []*metadata.Entity{partner},
			AccountRegisters: []*metadata.AccountRegister{ar},
		}
		rep := Run(ctx, env, []Check{refsCheck{}}, nil)
		res := findResult(t, rep, "refs")
		if res.Severity != SeverityError {
			t.Fatalf("битая ссылка в субконто не найдена: %+v", res)
		}
		if len(res.Findings) != 1 || res.Findings[0].Count != 1 {
			t.Fatalf("ожидалась одна битая ссылка из двух проводок, получено: %+v", res.Findings)
		}
		if !strings.Contains(res.Findings[0].Object, ar.Name+".Контрагент") {
			t.Fatalf("находка указывает не на субконто: %+v", res.Findings[0])
		}
	})
}
