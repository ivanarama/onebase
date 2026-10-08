package configcheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
)

// onebase check исполняет запросы модулей — и корректный запрос с
// разыменованием до ССЫЛОЧНОГО реквизита («Исполнитель.Учётка») отвергал
// как «no such column: ref_исполнитель.учётка»: колонка такого реквизита —
// учётка_id (#1784). Конфигурация выглядела ошибочной, хотя ошибки в ней нет.
func TestCheckModuleQueries_RefAttributeDereference(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "учётка.yaml"), `name: Учётка
fields:
  - name: Наименование
    type: string`)
	mkFile(t, filepath.Join(dir, "catalogs", "сотрудник.yaml"), `name: Сотрудник
fields:
  - name: Наименование
    type: string
  - name: Учётка
    type: reference:Учётка`)
	mkFile(t, filepath.Join(dir, "documents", "задача.yaml"), `name: Задача
fields:
  - name: Наименование
    type: string
  - name: Исполнитель
    type: reference:Сотрудник`)
	mkFile(t, filepath.Join(dir, "src", "мои.os"), `Процедура МоиЗадачи(Учётка)
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Документ.Задача ГДЕ Исполнитель.Учётка = &Учётка";
КонецПроцедуры`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	ctx := context.Background()
	dbPath := filepath.Join(dir, "schema.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { db.Close(); _ = os.Remove(dbPath) }()
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	issues := CheckModuleQueries(proj, func(sql string) error {
		return db.ValidateQuery(ctx, sql)
	})
	if len(issues) != 0 {
		t.Fatalf("корректный запрос с разыменованием до ссылочного реквизита отвергнут: %+v", issues)
	}
}
