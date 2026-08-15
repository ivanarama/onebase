package storage_test

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

// Одно и то же число, записанное разными путями, обязано лечь в базу одним и
// тем же текстом (#912).
//
// Значение приезжает в запись то числом (float64 из JSON REST), то строкой
// («100» из HTML-формы или из строки ТЧ, прочитанной обратно). Колонка number на
// SQLite — TEXT, поэтому разница видна прямо в данных: «100.0» против «100».
// Арифметика от этого не ломалась (запросы платформы оборачивают сырые колонки
// в CAST), ломалось сравнение представления — прикладной запрос без CAST, сверка
// выгрузок обмена между узлами, тесты и отчёты по сырым значениям.
//
// Тест матричный не для симметрии: на PostgreSQL колонка NUMERIC нормализует
// значение сама, и защищать надо ровно SQLite. Общий прогон фиксирует, что
// приведение не сделало хуже на PG и не разъехалось между диалектами.
func TestNumberCanonicalWriteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		entity := &metadata.Entity{
			Name: "ЧислоКанон" + suffix,
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Сумма", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2},
				{Name: "Упаковки", Type: metadata.FieldTypeNumber, Length: 6, Scale: 0},
			},
			TableParts: []metadata.TablePart{{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Количество", Type: metadata.FieldTypeNumber},
				},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		// Числом — как приходит из REST; строкой — как из HTML-формы.
		asNumber := uuid.New()
		asText := uuid.New()
		if err := db.Upsert(ctx, entity.Name, asNumber, map[string]any{
			"Количество": float64(100),
			"Сумма":      float64(100),
			"Упаковки":   10.6,
		}, entity); err != nil {
			t.Fatalf("Upsert числом: %v", err)
		}
		if err := db.Upsert(ctx, entity.Name, asText, map[string]any{
			"Количество": "100",
			"Сумма":      "100",
			"Упаковки":   "10.60",
		}, entity); err != nil {
			t.Fatalf("Upsert строкой: %v", err)
		}

		numberText := rawText(t, db, metadata.TableName(entity.Name), "количество", asNumber)
		textText := rawText(t, db, metadata.TableName(entity.Name), "количество", asText)
		if numberText != textText {
			t.Fatalf("шапка: число записано как %q, строка как %q — представление зависит от пути записи",
				numberText, textText)
		}
		if numberText != "100" {
			t.Fatalf("голый number записан как %q, want canonical 100", numberText)
		}
		sumNumber := rawText(t, db, metadata.TableName(entity.Name), "сумма", asNumber)
		sumText := rawText(t, db, metadata.TableName(entity.Name), "сумма", asText)
		if sumNumber != sumText {
			t.Fatalf("шапка number(15,2): число %q, строка %q", sumNumber, sumText)
		}
		if sumNumber != "100.00" {
			t.Fatalf("number(15,2) записан как %q, want fixed scale 100.00", sumNumber)
		}
		if got := rawText(t, db, metadata.TableName(entity.Name), "упаковки", asNumber); got != "11" {
			t.Fatalf("number(6,0) записан как %q, want rounded integer 11", got)
		}

		// Табличная часть — тот же путь, но своя функция записи.
		tp := entity.TableParts[0]
		if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, asNumber,
			[]map[string]any{{"Количество": float64(100)}}, tp); err != nil {
			t.Fatalf("ТЧ числом: %v", err)
		}
		if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, asText,
			[]map[string]any{{"Количество": "100"}}, tp); err != nil {
			t.Fatalf("ТЧ строкой: %v", err)
		}
		tpTable := metadata.TablePartTableName(entity.Name, tp.Name)
		tpNumber := rawTextWhere(t, db, tpTable, "количество", "parent_id", asNumber)
		tpText := rawTextWhere(t, db, tpTable, "количество", "parent_id", asText)
		if tpNumber != tpText {
			t.Fatalf("ТЧ: число записано как %q, строка как %q", tpNumber, tpText)
		}
		if tpNumber != "100" {
			t.Fatalf("ТЧ: canonical number=%q, want 100", tpNumber)
		}

		// Движения регистра — путь мимо Upsert, и именно на нём #912 нашёлся.
		reg := &metadata.Register{
			Name:       "РегКанон" + suffix,
			Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		period := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, entity.Name, asNumber,
			[]map[string]any{{"Склад": "Основной", "Количество": float64(100), "ВидДвижения": "Приход"}},
			reg, &period); err != nil {
			t.Fatalf("движения числом: %v", err)
		}
		if err := db.WriteMovements(ctx, reg.Name, entity.Name, asText,
			[]map[string]any{{"Склад": "Основной", "Количество": "100", "ВидДвижения": "Приход"}},
			reg, &period); err != nil {
			t.Fatalf("движения строкой: %v", err)
		}
		regTable := metadata.RegisterTableName(reg.Name)
		regNumber := rawTextWhere(t, db, regTable, "количество", "recorder", asNumber)
		regText := rawTextWhere(t, db, regTable, "количество", "recorder", asText)
		if regNumber != regText {
			t.Fatalf("движения: число записано как %q, строка как %q", regNumber, regText)
		}
		if regNumber != "100" {
			t.Fatalf("движения: canonical number=%q, want 100", regNumber)
		}
	})
}

// Дробное значение канонизируется так же: «100.50» и 100.5 — одно число.
func TestNumberCanonicalFractionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "ЧислоДробь" + uuid.NewString()[:8],
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Цена", Type: metadata.FieldTypeNumber, Length: 15, Scale: 2},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		a, b := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, entity.Name, a, map[string]any{"Цена": float64(100.5)}, entity); err != nil {
			t.Fatalf("Upsert числом: %v", err)
		}
		if err := db.Upsert(ctx, entity.Name, b, map[string]any{"Цена": "100.50"}, entity); err != nil {
			t.Fatalf("Upsert строкой: %v", err)
		}
		if got, want := rawText(t, db, metadata.TableName(entity.Name), "цена", b),
			rawText(t, db, metadata.TableName(entity.Name), "цена", a); got != want {
			t.Fatalf("дробное: строка записана как %q, число как %q", got, want)
		} else if got != "100.50" {
			t.Fatalf("number(15,2) записан как %q, want fixed scale 100.50", got)
		}
	})
}

func rawText(t *testing.T, db *storage.DB, table, col string, id uuid.UUID) string {
	t.Helper()
	return rawTextWhere(t, db, table, col, "id", id)
}

// rawTextWhere читает СЫРОЙ текст колонки, а не значение через слой чтения:
// проверять надо ровно то, что легло в базу. Слой чтения оба варианта приводит
// к decimal и разницу бы скрыл — именно поэтому дефект и дожил до #880.
func rawTextWhere(t *testing.T, db *storage.DB, table, col, keyCol string, id uuid.UUID) string {
	t.Helper()
	// Ключ сравниваем текстом: на PostgreSQL колонка uuid, на SQLite — строка,
	// и CAST снимает разницу, не заводя в тесте своего idArg.
	sql := fmt.Sprintf("SELECT CAST(%s AS TEXT) FROM %s WHERE CAST(%s AS TEXT) = %s LIMIT 1",
		col, table, keyCol, db.Dialect().Placeholder(1))
	var got *string
	if err := db.QueryRow(context.Background(), sql, id.String()).Scan(&got); err != nil {
		t.Fatalf("чтение %s.%s: %v", table, col, err)
	}
	if got == nil {
		t.Fatalf("%s.%s пусто для %s", table, col, id)
	}
	return *got
}
