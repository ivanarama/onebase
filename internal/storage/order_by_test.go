package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Список и подбор идут в порядке, заданном order_by, а не по алфавиту и не по
// порядку появления: у направлений свой порядок — тот, в котором их читает
// руководитель в отчёте («порядок в отчётах» в 1С).
func TestListUsesEntityOrderBy(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		OrderBy: []string{"ПорядокВОтчётах", "Наименование"},
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ПорядокВОтчётах", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	// Заводим в обратном порядке — чтобы «порядок появления» не совпал случайно
	// с ожидаемым и тест доказывал именно сортировку.
	rows := []struct {
		name  string
		order float64
	}{
		{"ЭР", 30},
		{"АВТО", 20},
		{"СМ", 10},
	}
	for _, r := range rows {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
			"наименование":    r.name,
			"порядоквотчётах": r.order,
		}, ent); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.List(ctx, ent.Name, ent, ListParams{RowFilterEvaluated: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"СМ", "АВТО", "ЭР"}
	if len(got) != len(want) {
		t.Fatalf("строк %d, ждали %d", len(got), len(want))
	}
	for i, w := range want {
		if name, _ := got[i]["Наименование"].(string); name != w {
			t.Fatalf("порядок = %v, ждали %v", []any{got[0]["Наименование"], got[1]["Наименование"], got[2]["Наименование"]}, want)
		}
	}
}

// Выбранная пользователем колонка главнее умолчания: order_by задаёт порядок,
// пока сортировку не попросили явно.
func TestExplicitSortBeatsOrderBy(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "order2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		OrderBy: []string{"ПорядокВОтчётах"},
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ПорядокВОтчётах", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		name  string
		order float64
	}{{"ЭР", 10}, {"АВТО", 20}} {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{
			"наименование": r.name, "порядоквотчётах": r.order,
		}, ent); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.List(ctx, ent.Name, ent, ListParams{Sort: "Наименование", RowFilterEvaluated: true})
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := got[0]["Наименование"].(string); name != "АВТО" {
		t.Fatalf("первым идёт %v, а сортировали по наименованию", got[0]["Наименование"])
	}
}

// Число на SQLite хранится текстом (десятичная точность), поэтому без приведения
// «100» сортировалось бы раньше «20». И незаполненный порядок уходит в конец:
// «не задан» — это не «первый».
func TestOrderByNumericAndNullsLast(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "order3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		OrderBy: []string{"ПорядокВОтчётах", "Наименование"},
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ПорядокВОтчётах", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		name  string
		order any
	}{
		{"сотый", 100.0},
		{"двадцатый", 20.0},
		{"без порядка", nil},
		{"третий", 3.0},
	} {
		fields := map[string]any{"наименование": r.name}
		if r.order != nil {
			fields["порядоквотчётах"] = r.order
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), fields, ent); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.List(ctx, ent.Name, ent, ListParams{RowFilterEvaluated: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"третий", "двадцатый", "сотый", "без порядка"}
	for i, w := range want {
		if name, _ := got[i]["Наименование"].(string); name != w {
			names := make([]any, 0, len(got))
			for _, row := range got {
				names = append(names, row["Наименование"])
			}
			t.Fatalf("порядок = %v, ждали %v", names, want)
		}
	}
}
