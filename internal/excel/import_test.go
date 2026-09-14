package excel

import (
	"os"
	"path/filepath"
	"testing"
)

// ImportRows выравнивает строки по самой длинной. Без этого обращение по
// индексу колонки зависело бы от того, где в конкретной строке файла
// закончились данные, и прикладной код падал бы на «коротких» строках.
func TestImportRows_PadsRaggedRowsAndKeepsText(t *testing.T) {
	data, err := ExportList(
		[]string{"Код", "ФИО", "Класс"},
		[][]any{
			{"007", "Иванов Иван", "5А"},
			{"012", "Петрова Анна"},
		},
	)
	if err != nil {
		t.Fatalf("сборка книги: %v", err)
	}
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := ImportRows(path)
	if err != nil {
		t.Fatalf("ImportRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("строк %d, ждали 3 (заголовок и две строки данных)", len(rows))
	}
	for i, row := range rows {
		if len(row) != 3 {
			t.Fatalf("строка %d имеет %d ячеек, ждали 3", i, len(row))
		}
	}
	// Ведущий ноль выживает только у текстовой ячейки: приведение к числу его
	// теряет, и код ученика «007» становится «7».
	if rows[1][0] != "007" {
		t.Fatalf("код с ведущим нулём потерян: %q", rows[1][0])
	}
	if rows[2][2] != "" {
		t.Fatalf("недостающая ячейка должна быть пустой, получено %q", rows[2][2])
	}
}

func TestImportRows_MissingFile(t *testing.T) {
	if _, err := ImportRows(filepath.Join(t.TempDir(), "нет.xlsx")); err == nil {
		t.Fatal("ожидалась ошибка на отсутствующем файле")
	}
}
