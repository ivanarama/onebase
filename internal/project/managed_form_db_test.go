package project

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestLoadFromDB_MixedCaseManagedFormPreservesColumnOrder(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	files := []configdb.ConfigFile{
		{
			Path: "documents/заказ.yaml",
			Content: []byte(`name: Заказ
posting: false
tableparts:
  - name: Строки
    fields:
      - name: Первая
        type: string
      - name: Вторая
        type: string
`),
		},
		{
			Path: "forms/ЗаКаЗ/объекта.form.yaml",
			Content: []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТаблицаСтроки
    data_path: Объект.Строки
    children:
      - kind: Колонка
        name: КолонкаВторая
        data_path: Объект.Строки.Вторая
      - kind: Колонка
        name: КолонкаПервая
        data_path: Объект.Строки.Первая
`),
		},
	}
	if err := repo.SaveFiles(ctx, files, configdb.VersionOptions{}); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}

	proj, err := LoadFromDB(ctx, repo)
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	t.Cleanup(proj.Close)

	var entity *metadata.Entity
	for _, candidate := range proj.Entities {
		if candidate.Name == "Заказ" {
			entity = candidate
			break
		}
	}
	if entity == nil {
		t.Fatal("document Заказ was not loaded from configdb")
	}
	if len(entity.Forms) != 1 || !entity.Forms[0].IsManaged() {
		t.Fatalf("managed form was not loaded: %#v", entity.Forms)
	}
	table := entity.Forms[0].GetElementByName("ТаблицаСтроки")
	if table == nil || table.Kind != metadata.FormElementTablePart {
		t.Fatalf("table part element was not loaded: %#v", table)
	}

	got := make([]string, 0, len(table.Children))
	for _, column := range table.Children {
		if column != nil && column.Kind == metadata.FormElementColumn {
			got = append(got, column.DataPath)
		}
	}
	want := []string{"Объект.Строки.Вторая", "Объект.Строки.Первая"}
	if len(got) != len(want) {
		t.Fatalf("column order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("column order = %v, want %v", got, want)
		}
	}
}
