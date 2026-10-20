package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// SQLite принимает пустую строку в колонке любого типа, и в базе, которую
// пополняли загрузчики мимо платформы, «не заполнено» в поле даты, числа или
// булева лежит именно так. PostgreSQL такое значение отвергает, и перенос
// базы SQLite → PostgreSQL падал на первой же строке:
// «invalid input syntax for type timestamp with time zone: ""».
func TestUniversalImportEmptyStringInTypedColumns_Matrix(t *testing.T) {
	entity := &metadata.Entity{
		Name: "ПустыеЗначения",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
			{Name: "ДатаЗагрузки", Type: metadata.FieldTypeDate},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Архивный", Type: metadata.FieldTypeBool},
		},
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: ПустыеЗначения\nfields:\n  - name: Наименование\n    type: string\n  - name: Комментарий\n    type: string\n" +
		"  - name: ДатаЗагрузки\n    type: date\n  - name: Количество\n    type: number\n  - name: Архивный\n    type: bool\n"
	if err := os.WriteFile(filepath.Join(cfg, "catalogs", "ПустыеЗначения.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source := newSQLite(t, "empty-typed")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	// Ровно так пишут внешние загрузчики: пустая строка вместо NULL.
	if _, err := source.Exec(ctx, "INSERT INTO пустыезначения (id, наименование, комментарий, датазагрузки, количество, архивный) VALUES (?, 'Элемент', '', '', '', '')", id.String()); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	for _, receiver := range []struct {
		name       string
		preMigrate bool
	}{
		{name: "fresh"},
		{name: "pre_migrated", preMigrate: true},
	} {
		t.Run(receiver.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
				target.SetFilesDir(t.TempDir())
				if receiver.preMigrate {
					if err := target.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
					ImportOptions{ExchangeMode: ExchangeRestoreClone}); err != nil {
					t.Fatal("import:", err)
				}

				// На свежем приёмнике тип колонки должен задать YAML из архива.
				var boolType string
				wantBoolType := "INTEGER"
				typeQuery := "SELECT type FROM pragma_table_info('пустыезначения') WHERE name = 'архивный'"
				if target.IsPostgres() {
					wantBoolType = "boolean"
					typeQuery = "SELECT data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'пустыезначения' AND column_name = 'архивный'"
				}
				if err := target.QueryRow(ctx, typeQuery).Scan(&boolType); err != nil {
					t.Fatal("тип булевой колонки:", err)
				}
				if boolType != wantBoolType {
					t.Fatalf("тип булевой колонки = %q, ожидался %q", boolType, wantBoolType)
				}

				// GetByID нормализует значения: SQL сохраняет различие NULL и "".
				var name, text string
				var date, number, boolean any
				query := "SELECT наименование, комментарий, датазагрузки, количество, архивный FROM пустыезначения WHERE id = " + target.Dialect().Placeholder(1)
				if err := target.QueryRow(ctx, query, id.String()).Scan(&name, &text, &date, &number, &boolean); err != nil {
					t.Fatalf("сырые значения после импорта: %v", err)
				}
				if name != "Элемент" || text != "" {
					t.Fatalf("строковые поля после импорта: Наименование=%q, Комментарий=%q", name, text)
				}
				var wantEmpty any = ""
				if target.IsPostgres() {
					wantEmpty = nil
				}
				for _, field := range []struct {
					name  string
					value any
				}{
					{"ДатаЗагрузки", date},
					{"Количество", number},
					{"Архивный", boolean},
				} {
					if field.value != wantEmpty {
						t.Errorf("%s после импорта = %#v, ожидалось %#v", field.name, field.value, wantEmpty)
					}
				}
			})
		})
	}
}
