package storage_test

// Единая точка автонумерации (план 117C). До неё расчёт жил в пяти местах и
// расходился молча; здесь же чинится Д13 — недетерминированный выбор периода.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func numDoc(num *metadata.Numerator) *metadata.Entity {
	return &metadata.Entity{
		Name: "Реализация", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "ДатаОплаты", Type: metadata.FieldTypeDate},
		},
		Numerator: num,
	}
}

// Д13: у документа с ДВУМЯ датами период брался перебором map — в один прогон
// из «Даты», в другой из «ДатыОплаты», то есть счётчик был случайным. Теперь
// выбор детерминирован: «Дата» приоритетна.
func TestPeriodKey_DeterministicWithTwoDates(t *testing.T) {
	ent := numDoc(&metadata.Numerator{Prefix: "Р-", Length: 6, Period: "year"})
	fields := map[string]any{
		"Дата":       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"ДатаОплаты": time.Date(2031, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	// Сто прогонов: при переборе map расхождение всплыло бы почти сразу.
	for i := 0; i < 100; i++ {
		if got := storage.PeriodKeyFor(ent, ent.Numerator, fields); got != "2026" {
			t.Fatalf("прогон %d: ключ периода = %q, ожидался 2026 (дата документа)", i, got)
		}
	}
}

// Если «Даты» нет, берётся первый реквизит-дата в порядке ОБЪЯВЛЕНИЯ, а не
// случайный из map.
func TestPeriodKey_FallsBackToDeclarationOrder(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Акт", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "ДатаНачала", Type: metadata.FieldTypeDate},
			{Name: "ДатаОкончания", Type: metadata.FieldTypeDate},
		},
		Numerator: &metadata.Numerator{Period: "year"},
	}
	fields := map[string]any{
		"ДатаНачала":    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"ДатаОкончания": time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for i := 0; i < 50; i++ {
		if got := storage.PeriodKeyFor(ent, ent.Numerator, fields); got != "2026" {
			t.Fatalf("прогон %d: ключ = %q, ожидался 2026 (первый объявленный)", i, got)
		}
	}
}

// Маски даты в префиксе: DEVELOPER.md обещал их с плана 07, а кода не было.
func TestExpandPrefix_DateMasks(t *testing.T) {
	d := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"{YYYY}-":      "2026-",
		"РТ-{YY}{MM}-": "РТ-2608-",
		"{DD}.{MM}/":   "12.08/",
		"безмасок-":    "безмасок-",
	}
	for in, want := range cases {
		if got := storage.ExpandPrefix(in, d); got != want {
			t.Errorf("ExpandPrefix(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

// Автонумерация целиком: счётчик, период, маска и формат — на обоих диалектах,
// потому что счётчик живёт в БД (INSERT … ON CONFLICT).
func TestGenerateNumberMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := numDoc(&metadata.Numerator{Prefix: "РТ-{YYYY}-", Length: 4, Period: "year"})
		if err := db.EnsureNumeratorSchema(ctx); err != nil {
			t.Fatalf("схема нумератора: %v", err)
		}
		fields := map[string]any{"Дата": time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
		var got []string
		for i := 0; i < 3; i++ {
			v, err := db.GenerateNumber(ctx, ent, fields)
			if err != nil {
				t.Fatalf("GenerateNumber: %v", err)
			}
			got = append(got, v)
		}
		want := []string{"РТ-2026-0001", "РТ-2026-0002", "РТ-2026-0003"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("номера = %v, ожидались %v", got, want)
		}
		// Другой год — свой счётчик, снова с единицы.
		next, err := db.GenerateNumber(ctx, ent, map[string]any{"Дата": time.Date(2027, 1, 9, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		if next != "РТ-2027-0001" {
			t.Errorf("после смены года номер = %q, ожидался РТ-2027-0001", next)
		}
	})
}

// Код справочника выдаётся только при объявленном numerator:. Раздача кодов
// всем справочникам молча изменила бы данные всех конфигураций.
func TestAutoNumberField_ByKind(t *testing.T) {
	doc := numDoc(nil)
	if got := storage.AutoNumberField(doc); got != "Номер" {
		t.Errorf("документ без numerator: поле = %q, ожидался Номер (legacy-счётчик)", got)
	}
	cat := &metadata.Entity{Name: "Контрагенты", Kind: metadata.KindCatalog}
	if got := storage.AutoNumberField(cat); got != "" {
		t.Errorf("справочник без numerator: поле = %q, ожидалось пусто", got)
	}
	cat.Numerator = &metadata.Numerator{Prefix: "К-", Period: "none"}
	if got := storage.AutoNumberField(cat); got != metadata.StandardCodeField {
		t.Errorf("справочник с numerator: поле = %q, ожидался Код", got)
	}
}

// Код справочника: период none — счётчик сквозной, без сброса по годам.
func TestGenerateCatalogCodeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.EnsureNumeratorSchema(ctx); err != nil {
			t.Fatalf("схема: %v", err)
		}
		cat := &metadata.Entity{
			Name: "Контрагенты" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
			Fields:    []metadata.Field{{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString}},
			Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none"},
		}
		first, err := db.GenerateNumber(ctx, cat, map[string]any{})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		second, err := db.GenerateNumber(ctx, cat, map[string]any{"Дата": time.Now().AddDate(1, 0, 0)})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		if first != "К-000001" || second != "К-000002" {
			t.Errorf("коды = %q, %q; ожидались К-000001 и К-000002 (сброса по годам у справочника нет)", first, second)
		}
	})
}

func TestGenerateNumber_NoNumeratorIsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "num.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	v, err := db.GenerateNumber(ctx, &metadata.Entity{Name: "X", Kind: metadata.KindCatalog}, nil)
	if err != nil || v != "" {
		t.Errorf("без numerator: получено %q, err=%v", v, err)
	}
}

func TestSetAutoNumberValueCompareAndSetMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "AutoNumberCAS", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		empty := ""
		spaces := " \t "
		cases := []struct {
			name     string
			initial  any
			expected *string
		}{
			{name: "null", initial: nil, expected: nil},
			{name: "empty", initial: "", expected: &empty},
			{name: "whitespace", initial: spaces, expected: &spaces},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				id := uuid.New()
				if err := db.Upsert(ctx, ent.Name, id, map[string]any{
					metadata.StandardCodeField: tc.initial,
					"Наименование":             tc.name,
				}, ent); err != nil {
					t.Fatalf("Upsert: %v", err)
				}
				updated, err := db.SetAutoNumberValue(ctx, ent, id, metadata.StandardCodeField, tc.expected, "К-000001")
				if err != nil {
					t.Fatalf("SetAutoNumberValue: %v", err)
				}
				if !updated {
					t.Fatal("точное пустое значение не обновлено")
				}
				row, err := db.GetByID(ctx, ent.Name, id, ent)
				if err != nil {
					t.Fatalf("GetByID: %v", err)
				}
				if got := row[metadata.StandardCodeField]; got != "К-000001" {
					t.Fatalf("код = %v, ожидался К-000001", got)
				}
				updated, err = db.SetAutoNumberValue(ctx, ent, id, metadata.StandardCodeField, tc.expected, "К-000002")
				if err != nil {
					t.Fatalf("повторный SetAutoNumberValue: %v", err)
				}
				if updated {
					t.Fatal("устаревший expected повторно обновил строку")
				}
			})
		}

		raceID := uuid.New()
		if err := db.Upsert(ctx, ent.Name, raceID, map[string]any{
			metadata.StandardCodeField: "", "Наименование": "race",
		}, ent); err != nil {
			t.Fatalf("Upsert(race): %v", err)
		}
		if err := db.Upsert(ctx, ent.Name, raceID, map[string]any{
			metadata.StandardCodeField: "РУЧНОЙ", "Наименование": "race",
		}, ent); err != nil {
			t.Fatalf("ручное заполнение: %v", err)
		}
		updated, err := db.SetAutoNumberValue(ctx, ent, raceID, metadata.StandardCodeField, &empty, "К-000003")
		if err != nil {
			t.Fatalf("SetAutoNumberValue(race): %v", err)
		}
		if updated {
			t.Fatal("CAS перезаписал конкурентно заполненный код")
		}
		row, err := db.GetByID(ctx, ent.Name, raceID, ent)
		if err != nil {
			t.Fatalf("GetByID(race): %v", err)
		}
		if got := row[metadata.StandardCodeField]; got != "РУЧНОЙ" {
			t.Fatalf("конкурентный код изменён: %v", got)
		}

		deletedID := uuid.New()
		if err := db.Upsert(ctx, ent.Name, deletedID, map[string]any{
			metadata.StandardCodeField: "", "Наименование": "deleted",
		}, ent); err != nil {
			t.Fatalf("Upsert(deleted): %v", err)
		}
		if err := db.Delete(ctx, ent.Name, deletedID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		updated, err = db.SetAutoNumberValue(ctx, ent, deletedID, metadata.StandardCodeField, &empty, "К-000004")
		if err != nil {
			t.Fatalf("SetAutoNumberValue(deleted): %v", err)
		}
		if updated {
			t.Fatal("удалённая строка отмечена как обновлённая")
		}
	})
}
