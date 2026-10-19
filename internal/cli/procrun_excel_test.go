package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func TestProcrunExcelPreservesSourceAndCatalogValues(t *testing.T) {
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: excel-cli\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Ученики.yaml", `name: Ученики
fields:
  - name: Код
    type: string
  - name: Наименование
    type: string
`)
	writeProcrunFixture(t, dir, "processors/Загрузка.yaml", `name: Загрузка
params:
  - name: Файл
    type: binary
`)
	writeProcrunFixture(t, dir, "src/Загрузка.proc.os", `Процедура Выполнить()
 Строки = ImportExcel(Параметры.Файл);
 Для Инд = 1 По Строки.Количество() - 1 Цикл
  Стр = Строки[Инд];
  Элемент = Справочники.Ученики.Создать();
  Элемент.Код = Стр[0];
  Элемент.Наименование = Стр[1];
  Элемент.Записать();
 КонецЦикла;
КонецПроцедуры
`)
	book, err := excel.ExportList([]string{"Code", "Name"}, [][]any{{"007", "First"}, {"012", "Last"}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.xlsx")
	if err := os.WriteFile(path, book, 0o600); err != nil {
		t.Fatal(err)
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "excel.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().String("proc", "", "")
	cmd.Flags().StringArray("set", nil, "")
	cmd.Flags().StringArray("file", nil, "")
	for flag, value := range map[string]string{
		"project": dir, "sqlite": dbPath, "proc": "Загрузка", "file": "Файл=" + path,
	} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runProcrun(cmd, nil); err != nil {
		t.Fatal(err)
	}
	gotBook, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(gotBook, book) {
		t.Fatalf("procrun changed or removed the source workbook: %v", err)
	}
	cat := proj.Entities[0]
	rows, err := db.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, row := range rows {
		values[row["Код"].(string)] = row["Наименование"].(string)
	}
	if len(rows) != 2 || !reflect.DeepEqual(values, map[string]string{"007": "First", "012": "Last"}) {
		t.Fatalf("procrun imported unexpected values: %v", rows)
	}
}
