package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Компилятор запросов — самое диалектозависимое место платформы: виртуальные
// таблицы разворачиваются в подзапросы, а числа на SQLite лежат в TEXT и
// требуют CAST. При этом до плана 115 пакет internal/query не видел PostgreSQL
// вообще: ни одного теста с тегом integration, ни одного обращения к
// TEST_DATABASE_URL. Сравнение строк SQL расхождения не ловит — сгенерированный
// текст может быть валиден на одном диалекте и падать на другом либо давать
// другой результат.
//
// Эти тесты ИСПОЛНЯЮТ скомпилированный SQL на обоих диалектах и сверяют
// значения (план 115, этап F3).

func matrixRegisters() []*metadata.Register {
	return []*metadata.Register{{
		Name:       "ОстаткиМатрица",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}}
}

// seedMatrixRegister заполняет регистр движениями двух периодов:
// апрель: Стол +5, Стул +2; май: Стол +3, Стул −2.
func seedMatrixRegister(t *testing.T, ctx context.Context, db *storage.DB, reg *metadata.Register) {
	t.Helper()
	apr := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	may := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

	if err := db.WriteMovements(ctx, reg.Name, "Пост", uuid.New(),
		[]map[string]any{
			{"ВидДвижения": "Приход", "Номенклатура": "Стол", "Количество": float64(5)},
			{"ВидДвижения": "Приход", "Номенклатура": "Стул", "Количество": float64(2)},
		}, reg, &apr); err != nil {
		t.Fatalf("движения апреля: %v", err)
	}
	if err := db.WriteMovements(ctx, reg.Name, "Пост", uuid.New(),
		[]map[string]any{
			{"ВидДвижения": "Приход", "Номенклатура": "Стол", "Количество": float64(3)},
			{"ВидДвижения": "Расход", "Номенклатура": "Стул", "Количество": float64(2)},
		}, reg, &may); err != nil {
		t.Fatalf("движения мая: %v", err)
	}
}

// execToMap исполняет скомпилированный запрос и собирает пары «строка → число».
func execToMap(t *testing.T, ctx context.Context, db *storage.DB, r query.Result) map[string]float64 {
	t.Helper()
	rows, err := db.Query(ctx, r.SQL, r.Args...)
	if err != nil {
		t.Fatalf("исполнение: %v\nSQL: %s", err, r.SQL)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var name string
		var val float64
		if err := rows.Scan(&name, &val); err != nil {
			t.Fatalf("скан: %v\nSQL: %s", err, r.SQL)
		}
		out[name] = val
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("курсор: %v", err)
	}
	return out
}

func compileFor(t *testing.T, db *storage.DB, regs []*metadata.Register, text string) query.Result {
	t.Helper()
	r, err := query.Compile(text, query.CompileOpts{Registers: regs, Dialect: db.Dialect()})
	if err != nil {
		t.Fatalf("компиляция: %v\nзапрос: %s", err, text)
	}
	return r
}

// Остатки() без момента: итог по всем движениям. Стол 5+3=8, Стул 2−2=0.
func TestVirtualTableBalancesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		regs := matrixRegisters()
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatalf("миграция регистров: %v", err)
		}
		seedMatrixRegister(t, ctx, db, regs[0])

		got := execToMap(t, ctx, db,
			compileFor(t, db, regs,
				"ВЫБРАТЬ Номенклатура, КоличествоОстаток ИЗ РегистрНакопления.ОстаткиМатрица.Остатки()"))

		if got["Стол"] != 8 {
			t.Errorf("остаток «Стол» = %v, ожидалось 8", got["Стол"])
		}
		// Нулевой остаток может как присутствовать строкой с 0, так и
		// отсутствовать — важно, что он не отрицательный и не «унесённый»
		// знаком расхода в другую сторону.
		if v, ok := got["Стул"]; ok && v != 0 {
			t.Errorf("остаток «Стул» = %v, ожидалось 0", v)
		}
	})
}

// Остатки(&Момент): движения после момента не учитываются. На момент конца
// апреля Стол = 5, Стул = 2 — майские приход и расход ещё не наступили.
func TestVirtualTableBalancesAtMomentMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		regs := matrixRegisters()
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatalf("миграция регистров: %v", err)
		}
		seedMatrixRegister(t, ctx, db, regs[0])

		r, err := query.Compile(
			"ВЫБРАТЬ Номенклатура, КоличествоОстаток ИЗ РегистрНакопления.ОстаткиМатрица.Остатки(&Момент)",
			query.CompileOpts{
				Registers: regs,
				Dialect:   db.Dialect(),
				Params:    map[string]any{"Момент": time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC)},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		got := execToMap(t, ctx, db, r)

		if got["Стол"] != 5 {
			t.Errorf("остаток «Стол» на конец апреля = %v, ожидалось 5 (майский приход не должен учитываться)", got["Стол"])
		}
		if got["Стул"] != 2 {
			t.Errorf("остаток «Стул» на конец апреля = %v, ожидалось 2 (майский расход не должен учитываться)", got["Стул"])
		}
	})
}

// Обороты(&Начало, &Конец): за май Стол +3 (приход), Стул −2 (расход).
// Знак ресурса в оборотах обязан считаться одинаково на обоих диалектах.
func TestVirtualTableTurnoversMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		regs := matrixRegisters()
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatalf("миграция регистров: %v", err)
		}
		seedMatrixRegister(t, ctx, db, regs[0])

		r, err := query.Compile(
			"ВЫБРАТЬ Номенклатура, КоличествоОборот ИЗ РегистрНакопления.ОстаткиМатрица.Обороты(&Начало, &Конец)",
			query.CompileOpts{
				Registers: regs,
				Dialect:   db.Dialect(),
				Params: map[string]any{
					"Начало": time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
					"Конец":  time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
				},
			})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		got := execToMap(t, ctx, db, r)

		if got["Стол"] != 3 {
			t.Errorf("оборот «Стол» за май = %v, ожидалось 3", got["Стол"])
		}
		if got["Стул"] != -2 {
			t.Errorf("оборот «Стул» за май = %v, ожидалось -2 (расход)", got["Стул"])
		}
	})
}
