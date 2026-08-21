package ui

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"context"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// TestParseTablePartRows_TpJSON проверяет ветку tp_json.{TPName} (план 48):
// если в форме есть tp_json.Товары, строки парсятся из JSON, а не из
// именованных инпутов tp.Товары.{idx}.{field}.
func TestParseTablePartRows_TpJSON(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Документ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Номенклатура", Type: metadata.FieldTypeString},
					{Name: "Количество", Type: metadata.FieldTypeNumber},
					{Name: "Цена", Type: metadata.FieldTypeNumber},
					{Name: "Сумма", Type: metadata.FieldTypeNumber},
				},
			},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	// Send tp_json.Товары with JSON array of rows
	body := url.Values{}
	body.Set("Наименование", "Тест")
	body.Set("tp_json.Товары", `[{"Номенклатура":"Гвозди","Количество":10,"Цена":5.5,"Сумма":55},{"Номенклатура":"Молоток","Количество":2,"Цена":150,"Сумма":300}]`)

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	tpRows, err := parseTablePartRows(req, ent)
	if err != nil {
		t.Fatalf("parseTablePartRows: %v", err)
	}
	rows := tpRows["Товары"]
	if len(rows) != 2 {
		t.Fatalf("Товары: got %d rows, want 2", len(rows))
	}

	// First row
	if rows[0]["Номенклатура"] != "Гвозди" {
		t.Errorf("row0 Номенклатура=%v, want Гвозди", rows[0]["Номенклатура"])
	}
	if qty, ok := rows[0]["Количество"].(float64); !ok || qty != 10 {
		t.Errorf("row0 Количество=%v (%T), want 10 (float64)", rows[0]["Количество"], rows[0]["Количество"])
	}
	if sum, ok := rows[0]["Сумма"].(float64); !ok || sum != 55 {
		t.Errorf("row0 Сумма=%v, want 55", rows[0]["Сумма"])
	}

	// Second row
	if rows[1]["Номенклатура"] != "Молоток" {
		t.Errorf("row1 Номенклатура=%v, want Молоток", rows[1]["Номенклатура"])
	}
	if qty, ok := rows[1]["Количество"].(float64); !ok || qty != 2 {
		t.Errorf("row1 Количество=%v, want 2", rows[1]["Количество"])
	}
}

// TestParseTablePartRows_TpJSON_EmptyRows проверяет, что пустые строки
// фильтруются (все поля пустые = строка пропускается).
func TestParseTablePartRows_TpJSON_EmptyRows(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Документ2",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Строки",
				Fields: []metadata.Field{
					{Name: "Товар", Type: metadata.FieldTypeString},
					{Name: "Кол", Type: metadata.FieldTypeNumber},
				},
			},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	body := url.Values{}
	body.Set("tp_json.Строки", `[{"Товар":"Есть","Кол":1},{"Товар":"","Кол":""},{"Товар":"Тоже","Кол":3}]`)

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	tpRows, err := parseTablePartRows(req, ent)
	if err != nil {
		t.Fatalf("parseTablePartRows: %v", err)
	}
	rows := tpRows["Строки"]
	if len(rows) != 2 {
		t.Fatalf("Строки: got %d rows, want 2 (empty row filtered)", len(rows))
	}
	if rows[0]["Товар"] != "Есть" {
		t.Errorf("row0 Товар=%v, want Есть", rows[0]["Товар"])
	}
	if rows[1]["Товар"] != "Тоже" {
		t.Errorf("row1 Товар=%v, want Тоже", rows[1]["Товар"])
	}
}

// TestParseTablePartRows_LegacyNotBroken проверяет, что без tp_json
// старый путь именованных инпутов продолжает работать (backward-compat).
func TestParseTablePartRows_LegacyNotBroken(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ent := &metadata.Entity{
		Name: "Документ3",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Позиции",
				Fields: []metadata.Field{
					{Name: "Название", Type: metadata.FieldTypeString},
					{Name: "Кол", Type: metadata.FieldTypeNumber},
				},
			},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}

	// Legacy named inputs: tp.Позиции.0.Название, tp.Позиции.0.Кол
	body := url.Values{}
	body.Set("tp.Позиции.0.Название", "Легаси")
	body.Set("tp.Позиции.0.Кол", "5")

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	tpRows, err := parseTablePartRows(req, ent)
	if err != nil {
		t.Fatalf("parseTablePartRows: %v", err)
	}
	rows := tpRows["Позиции"]
	if len(rows) != 1 {
		t.Fatalf("Позиции: got %d rows, want 1", len(rows))
	}
	if rows[0]["Название"] != "Легаси" {
		t.Errorf("row0 Название=%v, want Легаси", rows[0]["Название"])
	}
}
