package dbcheck

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Прогон doctor на обоих диалектах (пункт чек-листа #888).
//
// Весь пакет проверялся только на SQLite, хотя половина проверок — это сырой
// SQL со сверками сумм: CAST числовых колонок, LEFT JOIN по ссылке, сравнение
// итогов с движениями. На SQLite колонка number хранится как TEXT, на
// PostgreSQL это NUMERIC, и одно и то же выражение ведёт себя по-разному —
// ровно тот класс расхождений, ради которого заведён dbtest.ForEachDialect.
//
// Здесь проверяется и «чисто» на исправной базе (иначе ошибка SQL на PG
// выглядела бы как отсутствие находок), и что каждая проверка ЛОВИТ
// подложенный дефект на обоих диалектах.

type matrixFixture struct {
	env      *Env
	entity   *metadata.Entity
	register *metadata.Register
	account  *metadata.AccountRegister
	partner  uuid.UUID
	docID    uuid.UUID
}

func newMatrixFixture(t *testing.T, db *storage.DB) *matrixFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	partner := &metadata.Entity{
		Name: "Контрагенты" + suffix, Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name: "Реализация" + suffix, Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: metadata.FieldType("reference:" + partner.Name), RefEntity: partner.Name},
			{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2},
		},
	}
	reg := &metadata.Register{
		Name:       "Остатки" + suffix,
		Dimensions: []metadata.Field{{Name: "Контрагент", Type: metadata.FieldType("reference:" + partner.Name), RefEntity: partner.Name}},
		Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
		Totals:     metadata.RegisterTotals{Enabled: true},
	}
	ar := &metadata.AccountRegister{
		Name:      "БухУчёт" + suffix,
		Resources: []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2}},
		Subconto: []metadata.Field{{
			Name: "Контрагент", Type: metadata.FieldType("reference:" + partner.Name), RefEntity: partner.Name,
		}},
		Totals: metadata.RegisterTotals{Enabled: true},
	}

	if err := db.Migrate(ctx, []*metadata.Entity{partner, doc}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatalf("MigrateRegisters: %v", err)
	}
	if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{ar}); err != nil {
		t.Fatalf("MigrateAccountRegisters: %v", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	partnerID := uuid.New()
	if err := db.Upsert(ctx, partner.Name, partnerID,
		map[string]any{"Наименование": "Живой"}, partner); err != nil {
		t.Fatalf("Upsert контрагента: %v", err)
	}
	docID := uuid.New()
	if err := db.Upsert(ctx, doc.Name, docID,
		map[string]any{"Контрагент": partnerID.String(), "Сумма": 500}, doc); err != nil {
		t.Fatalf("Upsert документа: %v", err)
	}
	period := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, docID, []map[string]any{
		{"Контрагент": partnerID.String(), "Сумма": 500, "ВидДвижения": "Приход"},
	}, reg, &period); err != nil {
		t.Fatalf("WriteMovements: %v", err)
	}
	if err := db.WriteAccountMovements(ctx, ar.Name, doc.Name, docID, []map[string]any{
		{"счётдт": "50", "счёткт": "51", "Сумма": 500, "Контрагент": partnerID.String()},
	}, ar, &period); err != nil {
		t.Fatalf("WriteAccountMovements: %v", err)
	}

	return &matrixFixture{
		env: &Env{
			DB:               db,
			Entities:         []*metadata.Entity{partner, doc},
			Registers:        []*metadata.Register{reg},
			AccountRegisters: []*metadata.AccountRegister{ar},
		},
		entity: doc, register: reg, account: ar, partner: partnerID, docID: docID,
	}
}

// Исправная база: ни одна проверка не находит проблем и ни одна не падает с
// ошибкой SQL. Без этого «находок нет» на PostgreSQL могло бы означать
// «запрос не выполнился».
func TestDoctorChecksCleanBaseMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		fx := newMatrixFixture(t, db)
		rep := Run(context.Background(), fx.env, All(), nil)
		if len(rep.Results) != len(All()) {
			t.Fatalf("выполнены не все проверки: %d из %d", len(rep.Results), len(All()))
		}
		for _, res := range rep.Results {
			if res.Error != "" {
				t.Errorf("проверка %s упала: %s", res.Check, res.Error)
			}
			if res.Severity == SeverityError {
				t.Errorf("проверка %s нашла ошибку на исправной базе: %+v", res.Check, res.Findings)
			}
		}
	})
}

// Расхождение итогов регистра накопления с движениями. Сверка идёт сложением
// сырых колонок с CAST — на SQLite это TEXT, на PostgreSQL NUMERIC.
func TestTotalsCheckDetectsSkewMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fx := newMatrixFixture(t, db)
		if err := db.RecalcRegisterTotals(ctx, fx.register); err != nil {
			t.Fatalf("RecalcRegisterTotals: %v", err)
		}
		// Портим итог, а не движение: проверка обязана заметить именно разрыв
		// между предрасчётом и первичными данными.
		totals := metadata.RegisterTotalsTableName(fx.register.Name)
		if _, err := db.Exec(ctx, fmt.Sprintf("UPDATE %s SET сумма = '999'", totals)); err != nil {
			t.Fatalf("порча итогов: %v", err)
		}
		res := findResult(t, Run(ctx, fx.env, []Check{totalsCheck{}}, nil), "totals")
		if res.Severity != SeverityError {
			t.Fatalf("расхождение итогов не найдено: %+v", res)
		}

		// И починка возвращает базу в сходящееся состояние — RecalcRegisterTotals
		// до сих пор гонялся только на SQLite.
		fixed := findResult(t, Run(ctx, fx.env, []Check{totalsCheck{}}, map[string]bool{"totals": true}), "totals")
		if fixed.Error != "" {
			t.Fatalf("пересчёт итогов упал: %s", fixed.Error)
		}
		if fixed.Severity == SeverityError {
			t.Fatalf("после пересчёта итоги всё ещё расходятся: %+v", fixed.Findings)
		}
	})
}

// То же для регистра бухгалтерии: обороты Дт/Кт против проводок.
func TestAccountTotalsCheckDetectsSkewMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fx := newMatrixFixture(t, db)
		if err := db.RecalcAccountRegisterTotals(ctx, fx.account); err != nil {
			t.Fatalf("RecalcAccountRegisterTotals: %v", err)
		}
		totals := metadata.AccountRegTotalsTableName(fx.account.Name)
		if _, err := db.Exec(ctx, fmt.Sprintf("UPDATE %s SET сумма_дт = '777'", totals)); err != nil {
			t.Fatalf("порча итогов бухрегистра: %v", err)
		}
		res := findResult(t, Run(ctx, fx.env, []Check{accountTotalsCheck{}}, nil), "account-totals")
		if res.Severity != SeverityError {
			t.Fatalf("расхождение оборотов не найдено: %+v", res)
		}
		fixed := findResult(t, Run(ctx, fx.env, []Check{accountTotalsCheck{}},
			map[string]bool{"account-totals": true}), "account-totals")
		if fixed.Error != "" {
			t.Fatalf("пересчёт оборотов упал: %s", fixed.Error)
		}
		if fixed.Severity == SeverityError {
			t.Fatalf("после пересчёта обороты всё ещё расходятся: %+v", fixed.Findings)
		}
	})
}

// Движение с удалённым регистратором: удаление документа мимо платформы.
func TestOrphanMovementsCheckMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fx := newMatrixFixture(t, db)
		if _, err := db.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE CAST(id AS TEXT) = %s",
			metadata.TableName(fx.entity.Name), db.Dialect().Placeholder(1)), fx.docID.String()); err != nil {
			t.Fatalf("удаление документа: %v", err)
		}
		res := findResult(t, Run(ctx, fx.env, []Check{orphanMovementsCheck{}}, nil), "orphan-movements")
		if len(res.Findings) == 0 {
			t.Fatalf("осиротевшие движения не найдены: %+v", res)
		}
		fixed := findResult(t, Run(ctx, fx.env, []Check{orphanMovementsCheck{}},
			map[string]bool{"orphan-movements": true}), "orphan-movements")
		if fixed.Error != "" {
			t.Fatalf("удаление осиротевших движений упало: %s", fixed.Error)
		}
		var left int
		if err := db.QueryRow(ctx,
			"SELECT COUNT(*) FROM "+metadata.RegisterTableName(fx.register.Name)).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Fatalf("движения удалённого документа остались: %d", left)
		}
	})
}

// Битая ссылка в измерении регистра и её автоочистка.
//
// Ломаем именно измерение, а не реквизит шапки: у колонки шапки есть внешний
// ключ, и на каждом диалекте его пришлось бы обходить по-своему (PRAGMA против
// session_replication_role) — тест проверял бы обход, а не проверку. У колонок
// регистра внешних ключей нет вовсе, и битая ссылка появляется там обычной
// записью. SQL у проверки один и тот же для любой колонки, а тип колонки тот же
// uuid, что и в шапке, — покрытие диалекта от этого не страдает.
func TestRefsCheckRegisterDimensionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fx := newMatrixFixture(t, db)
		period := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, fx.register.Name, fx.entity.Name, uuid.New(),
			[]map[string]any{{"Контрагент": uuid.New().String(), "Сумма": 10, "ВидДвижения": "Приход"}},
			fx.register, &period); err != nil {
			t.Fatalf("движение с битой ссылкой: %v", err)
		}
		res := findResult(t, Run(ctx, fx.env, []Check{refsCheck{}}, nil), "refs")
		if res.Severity != SeverityError {
			t.Fatalf("битая ссылка не найдена: %+v", res)
		}

		fixed := findResult(t, Run(ctx, fx.env, []Check{refsCheck{}}, map[string]bool{"refs": true}), "refs")
		if fixed.Error != "" {
			t.Fatalf("очистка битой ссылки упала: %s", fixed.Error)
		}
		var broken int
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE контрагент_id IS NOT NULL AND контрагент_id NOT IN (SELECT id FROM %s)",
			metadata.RegisterTableName(fx.register.Name),
			metadata.TableName(fx.env.Entities[0].Name))).Scan(&broken); err != nil {
			t.Fatal(err)
		}
		if broken != 0 {
			t.Fatalf("битых ссылок в измерении осталось: %d", broken)
		}
	})
}
