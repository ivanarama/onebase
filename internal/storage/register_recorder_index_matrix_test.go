package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Удаление движений документа идёт по индексу регистратора, а не полным
// просмотром таблицы регистра.
//
// Движения адресуются парой (recorder, recorder_type): их удаляет каждое
// проведение, перепроведение, отмена проведения и удаление документа. У таблиц
// рег_* и инфо_* индекса по этой паре не было, и каждое проведение просматривало
// регистр целиком — на 500 тыс. движений ~150 мс на один регистр против долей
// миллисекунды с индексом.
//
// Проверка идёт через публичный путь схемы (MigrateRegisters /
// MigrateInfoRegisters) и по базе, созданной ДО исправления: таблица регистра
// уже есть, индекса на ней нет. Именно так выглядит рабочая база, которую
// обновляют на новую платформу.
func TestRegisterMovementsDeleteUsesRecorderIndex_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name: "ОстаткиТоваров",
			Dimensions: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Склад", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		// База до исправления: таблица движений есть, индекса по регистратору нет.
		if _, err := db.Exec(ctx, storage.CreateRegisterSQL(db.Dialect(), reg)); err != nil {
			t.Fatalf("старая таблица регистра: %v", err)
		}
		table := metadata.RegisterTableName(reg.Name)
		if plan := deleteByRecorderPlan(t, db, table); planUsesIndex(db, plan) {
			t.Fatalf("предпосылка теста: у старой таблицы индекса по регистратору быть не должно, план:\n%s", plan)
		}

		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		if plan := deleteByRecorderPlan(t, db, table); !planUsesIndex(db, plan) {
			t.Fatalf("удаление движений документа просматривает регистр целиком, план:\n%s", plan)
		}
		// Повторная миграция идемпотентна.
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("повторная MigrateRegisters: %v", err)
		}
	})
}

// Регистр сведений получает частичный индекс по регистратору, и он переживает
// смену первичного ключа: на SQLite fixInfoRegPK пересоздаёт таблицу целиком,
// поэтому индекс строится после неё.
func TestInfoRegisterDeleteUsesRecorderIndex_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "ЦеныНоменклатуры",
			Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
		}
		table := metadata.InfoRegTableName(ir.Name)
		// База до исправления: непериодический регистр без индекса.
		if _, err := db.Exec(ctx, storage.CreateInfoRegisterSQL(db.Dialect(), ir)); err != nil {
			t.Fatalf("старая таблица регистра сведений: %v", err)
		}
		if plan := deleteByRecorderPlan(t, db, table); planUsesIndex(db, plan) {
			t.Fatalf("предпосылка теста: у старой таблицы индекса по регистратору быть не должно, план:\n%s", plan)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatalf("MigrateInfoRegisters: %v", err)
		}
		if plan := deleteByRecorderPlan(t, db, table); !planUsesIndex(db, plan) {
			t.Fatalf("удаление записей регистратора просматривает регистр сведений целиком, план:\n%s", plan)
		}

		// Регистр стал периодическим — первичный ключ меняется, SQLite
		// пересоздаёт таблицу. Индекс по регистратору обязан остаться.
		periodic := *ir
		periodic.Periodic = true
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{&periodic}); err != nil {
			t.Fatalf("MigrateInfoRegisters (periodic): %v", err)
		}
		if plan := deleteByRecorderPlan(t, db, table); !planUsesIndex(db, plan) {
			t.Fatalf("после смены ключа регистра сведений индекс по регистратору потерян, план:\n%s", plan)
		}
	})
}

// deleteByRecorderPlan возвращает план того удаления, которым storage снимает
// движения документа перед записью новых (register.go, inforeg.go). Значения
// подставлены литералами: план нужен без исполнения, а параметры в EXPLAIN
// диалекты принимают по-разному.
func deleteByRecorderPlan(t *testing.T, db *storage.DB, table string) string {
	t.Helper()
	ctx := context.Background()
	stmt := fmt.Sprintf(
		"DELETE FROM %s WHERE recorder = '00000000-0000-0000-0000-000000000001' AND recorder_type = 'Реализация'",
		table)
	var lines []string
	err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		query := "EXPLAIN QUERY PLAN " + stmt
		if db.Dialect().Name() == "postgres" {
			// На пустой таблице PostgreSQL честно выберет полный просмотр при
			// любом индексе. Запрет seqscan оставляет полный просмотр только
			// там, где другого пути нет, — то есть когда индекса нет.
			if _, err := db.Exec(txCtx, "SET LOCAL enable_seqscan = off"); err != nil {
				return err
			}
			query = "EXPLAIN " + stmt
		}
		rows, err := db.Query(txCtx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals := make([]any, len(rows.FieldNames()))
			ptrs := make([]any, len(vals))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			// План — последняя колонка: detail у SQLite, «QUERY PLAN» у PostgreSQL.
			lines = append(lines, fmt.Sprint(vals[len(vals)-1]))
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("план удаления из %s: %v", table, err)
	}
	return strings.Join(lines, "\n")
}

func planUsesIndex(db *storage.DB, plan string) bool {
	if db.Dialect().Name() == "postgres" {
		return strings.Contains(plan, "Index Scan") && !strings.Contains(plan, "Seq Scan")
	}
	return strings.Contains(plan, "USING INDEX") || strings.Contains(plan, "USING COVERING INDEX")
}
